package validator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	// DefaultReferenceDataPort is the default gRPC port for the Reference Data service.
	DefaultReferenceDataPort = 50051

	// DefaultDeprecatorTimeout is the default timeout for deprecator operations.
	DefaultDeprecatorTimeout = 30 * time.Second
)

// InstrumentDeprecatorConfig holds configuration for the InstrumentDeprecator.
type InstrumentDeprecatorConfig struct {
	// Target is the gRPC server address for Reference Data (e.g., "localhost:50051").
	// If set, overrides Kubernetes DNS-based discovery.
	Target string

	// ServiceName is the Kubernetes service name for Reference Data.
	ServiceName string

	// Namespace is the Kubernetes namespace (defaults to "default").
	Namespace string

	// Port is the Reference Data service port (defaults to 50051).
	Port int

	// Timeout is the default timeout for RPC calls (defaults to 30s).
	Timeout time.Duration

	// Logger is used for structured logging.
	Logger *slog.Logger

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// applyDefaults sets default values for unspecified configuration fields.
func (cfg *InstrumentDeprecatorConfig) applyDefaults() {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultDeprecatorTimeout
	}
	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultReferenceDataPort
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
}

// InstrumentDeprecator handles deprecation of instrument versions
// by calling the Reference Data service.
type InstrumentDeprecator struct {
	client      referencedatav1.ReferenceDataServiceClient
	conn        *grpc.ClientConn
	timeout     time.Duration
	logger      *slog.Logger
	retryConfig clients.RetryConfig
}

// NewInstrumentDeprecator creates a new InstrumentDeprecator with a gRPC client.
func NewInstrumentDeprecator(ctx context.Context, cfg InstrumentDeprecatorConfig) (*InstrumentDeprecator, error) {
	cfg.applyDefaults()

	conn, err := createReferenceDataConnection(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create reference data connection: %w", err)
	}

	return &InstrumentDeprecator{
		client:      referencedatav1.NewReferenceDataServiceClient(conn),
		conn:        conn,
		timeout:     cfg.Timeout,
		logger:      cfg.Logger,
		retryConfig: clients.DefaultRetryConfig(),
	}, nil
}

// createReferenceDataConnection creates a gRPC connection based on configuration.
func createReferenceDataConnection(ctx context.Context, cfg InstrumentDeprecatorConfig) (*grpc.ClientConn, error) {
	if cfg.ServiceName != "" {
		return platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
			ServiceName: cfg.ServiceName,
			Namespace:   cfg.Namespace,
			Port:        cfg.Port,
			DialOptions: cfg.DialOptions,
		})
	}

	if cfg.Target != "" {
		dialOpts := cfg.DialOptions
		if dialOpts == nil {
			dialOpts = []grpc.DialOption{
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			}
		}
		return grpc.NewClient(cfg.Target, dialOpts...)
	}

	return nil, ErrTargetOrServiceRequired
}

// DeprecateInstrumentRequest contains parameters for deprecating an instrument version.
type DeprecateInstrumentRequest struct {
	// InstrumentCode is the instrument code to deprecate.
	InstrumentCode string

	// InstrumentVersion is the version to deprecate.
	InstrumentVersion int

	// SuccessorID is the optional UUID of the replacement instrument.
	// When provided, the deprecated instrument will point to this successor.
	SuccessorID string
}

// DeprecateInstrumentResponse contains the result of the deprecation operation.
type DeprecateInstrumentResponse struct {
	// Instrument is the deprecated instrument definition.
	Instrument *referencedatav1.InstrumentDefinition
}

// DeprecateInstrument marks an instrument version as DEPRECATED.
//
// This operation:
// 1. Verifies the instrument exists and is in ACTIVE status
// 2. Calls the Reference Data service to deprecate the instrument
// 3. Returns the updated instrument definition
//
// Once deprecated, the instrument cannot be used for new transactions.
func (d *InstrumentDeprecator) DeprecateInstrument(ctx context.Context, req DeprecateInstrumentRequest) (*DeprecateInstrumentResponse, error) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrMissingTenantContext
	}

	d.logger.Info("deprecating instrument",
		"step", "instrument_deprecation",
		"instrument_code", req.InstrumentCode,
		"instrument_version", req.InstrumentVersion,
		"successor_id", req.SuccessorID,
		"tenant_id", tenantID.String(),
	)

	// Step 1: Verify the instrument exists and check its current status
	currentInstrument, err := d.retrieveInstrument(ctx, req.InstrumentCode, req.InstrumentVersion)
	if err != nil {
		return nil, err
	}

	// Validate current status
	if currentInstrument.Status == referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED {
		d.logger.Info("instrument already deprecated",
			"step", "instrument_deprecation",
			"instrument_code", req.InstrumentCode,
			"instrument_version", req.InstrumentVersion,
			"result", "already_deprecated",
		)
		return nil, &InstrumentAlreadyDeprecatedError{
			InstrumentCode:    req.InstrumentCode,
			InstrumentVersion: req.InstrumentVersion,
		}
	}

	if currentInstrument.Status != referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE {
		return nil, &InstrumentNotActiveError{
			InstrumentCode:    req.InstrumentCode,
			InstrumentVersion: req.InstrumentVersion,
			CurrentStatus:     currentInstrument.Status.String(),
		}
	}

	// Step 2: Call the deprecation API
	deprecatedInstrument, err := d.callDeprecateAPI(ctx, req)
	if err != nil {
		return nil, &ValidationError{
			Operation: "instrument_deprecation",
			Message:   "failed to deprecate instrument via Reference Data service",
			Cause:     err,
		}
	}

	d.logger.Info("instrument deprecated successfully",
		"step", "instrument_deprecation",
		"instrument_code", req.InstrumentCode,
		"instrument_version", req.InstrumentVersion,
		"deprecated_at", deprecatedInstrument.DeprecatedAt.AsTime(),
		"result", "success",
	)

	return &DeprecateInstrumentResponse{
		Instrument: deprecatedInstrument,
	}, nil
}

// retrieveInstrument fetches the current instrument definition.
func (d *InstrumentDeprecator) retrieveInstrument(ctx context.Context, code string, version int) (*referencedatav1.InstrumentDefinition, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	var instrument *referencedatav1.InstrumentDefinition
	var lastErr error

	err := clients.Retry(ctx, d.retryConfig, func() error {
		resp, err := d.client.RetrieveInstrument(ctx, &referencedatav1.RetrieveInstrumentRequest{
			Code:    code,
			Version: int32(version),
		})
		if err != nil {
			lastErr = err
			return err
		}
		instrument = resp.Instrument
		return nil
	})
	if err != nil {
		if lastErr != nil {
			st, ok := status.FromError(lastErr)
			if ok && st.Code() == codes.NotFound {
				return nil, &InstrumentNotFoundError{
					InstrumentCode:    code,
					InstrumentVersion: version,
				}
			}
		}
		return nil, fmt.Errorf("failed to retrieve instrument: %w", err)
	}

	return instrument, nil
}

// callDeprecateAPI calls the Reference Data service to deprecate the instrument.
func (d *InstrumentDeprecator) callDeprecateAPI(ctx context.Context, req DeprecateInstrumentRequest) (*referencedatav1.InstrumentDefinition, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	protoReq := &referencedatav1.DeprecateInstrumentRequest{
		Code:        req.InstrumentCode,
		Version:     int32(req.InstrumentVersion),
		SuccessorId: req.SuccessorID,
	}

	var instrument *referencedatav1.InstrumentDefinition
	var lastErr error

	// Deprecation is idempotent if the instrument is already deprecated,
	// but not if it's in a different state, so we use retry with caution.
	err := clients.Retry(ctx, d.retryConfig, func() error {
		resp, err := d.client.DeprecateInstrument(ctx, protoReq)
		if err != nil {
			lastErr = err
			return err
		}
		instrument = resp.Instrument
		return nil
	})
	if err != nil {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, err
	}

	return instrument, nil
}

// RetrieveInstrument fetches an instrument definition without modifying it.
// This is useful for validation and inspection.
func (d *InstrumentDeprecator) RetrieveInstrument(ctx context.Context, code string, version int) (*referencedatav1.InstrumentDefinition, error) {
	return d.retrieveInstrument(ctx, code, version)
}

// Close releases the gRPC connection.
func (d *InstrumentDeprecator) Close() error {
	if d.conn != nil {
		if err := d.conn.Close(); err != nil {
			return fmt.Errorf("failed to close reference data connection: %w", err)
		}
	}
	return nil
}

// InstrumentDeprecatorInterface defines the interface for instrument deprecation.
// This allows for easy mocking in tests.
type InstrumentDeprecatorInterface interface {
	DeprecateInstrument(ctx context.Context, req DeprecateInstrumentRequest) (*DeprecateInstrumentResponse, error)
	RetrieveInstrument(ctx context.Context, code string, version int) (*referencedatav1.InstrumentDefinition, error)
	Close() error
}

// Ensure InstrumentDeprecator implements the interface.
var _ InstrumentDeprecatorInterface = (*InstrumentDeprecator)(nil)

// MockInstrumentDeprecator is a mock implementation for testing.
type MockInstrumentDeprecator struct {
	DeprecateFunc func(ctx context.Context, req DeprecateInstrumentRequest) (*DeprecateInstrumentResponse, error)
	RetrieveFunc  func(ctx context.Context, code string, version int) (*referencedatav1.InstrumentDefinition, error)
	CloseFunc     func() error
}

// DeprecateInstrument implements InstrumentDeprecatorInterface.
func (m *MockInstrumentDeprecator) DeprecateInstrument(ctx context.Context, req DeprecateInstrumentRequest) (*DeprecateInstrumentResponse, error) {
	if m.DeprecateFunc != nil {
		return m.DeprecateFunc(ctx, req)
	}
	return &DeprecateInstrumentResponse{}, nil
}

// RetrieveInstrument implements InstrumentDeprecatorInterface.
func (m *MockInstrumentDeprecator) RetrieveInstrument(ctx context.Context, code string, version int) (*referencedatav1.InstrumentDefinition, error) {
	if m.RetrieveFunc != nil {
		return m.RetrieveFunc(ctx, code, version)
	}
	return &referencedatav1.InstrumentDefinition{Code: code, Version: int32(version)}, nil
}

// Close implements InstrumentDeprecatorInterface.
func (m *MockInstrumentDeprecator) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return nil
}

// NewMockInstrumentDeprecator creates a mock deprecator with configurable behavior.
func NewMockInstrumentDeprecator() *MockInstrumentDeprecator {
	return &MockInstrumentDeprecator{}
}

// NewMockInstrumentDeprecatorWithError creates a mock that returns the specified error.
func NewMockInstrumentDeprecatorWithError(err error) *MockInstrumentDeprecator {
	return &MockInstrumentDeprecator{
		DeprecateFunc: func(ctx context.Context, _ DeprecateInstrumentRequest) (*DeprecateInstrumentResponse, error) {
			if _, ok := tenant.FromContext(ctx); !ok {
				return nil, ErrMissingTenantContext
			}
			return nil, err
		},
		RetrieveFunc: func(ctx context.Context, _ string, _ int) (*referencedatav1.InstrumentDefinition, error) {
			if _, ok := tenant.FromContext(ctx); !ok {
				return nil, ErrMissingTenantContext
			}
			return nil, err
		},
	}
}

// IsInstrumentDeprecated is a helper that checks if an instrument is already deprecated.
func IsInstrumentDeprecated(err error) bool {
	if err == nil {
		return false
	}
	var deprecatedErr *InstrumentAlreadyDeprecatedError
	return errors.As(err, &deprecatedErr)
}

// IsInstrumentNotFound is a helper that checks if an instrument was not found.
func IsInstrumentNotFound(err error) bool {
	if err == nil {
		return false
	}
	var notFoundErr *InstrumentNotFoundError
	return errors.As(err, &notFoundErr)
}
