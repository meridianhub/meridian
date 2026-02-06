// Package grpc provides gRPC client adapters for external service communication.
package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/services/utilization-metering-consumer/domain"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Static errors for validation
var (
	// ErrServiceNameRequired is returned when ServiceName is not provided in client configuration
	ErrServiceNameRequired = fmt.Errorf("ServiceName is required for position keeping client")
)

// PositionKeepingGRPCClient implements domain.PositionKeepingClient using gRPC.
type PositionKeepingGRPCClient struct {
	conn        *grpc.ClientConn
	client      positionkeepingv1.PositionKeepingServiceClient
	timeout     time.Duration
	logger      *slog.Logger
	retryConfig sharedclients.RetryConfig
}

// ClientConfig holds configuration for the Position Keeping gRPC client.
type ClientConfig struct {
	// ServiceName is the Kubernetes service name (e.g., "position-keeping").
	// Required for DNS-based load balancing.
	ServiceName string

	// Namespace is the Kubernetes namespace (defaults to "default" if empty).
	Namespace string

	// Port is the service port number (Position Keeping uses 50053).
	Port int

	// Timeout is the default timeout for RPC calls (defaults to 5 seconds).
	Timeout time.Duration

	// Logger is used for structured logging.
	Logger *slog.Logger

	// RetryConfig configures retry behavior for transient failures.
	// If nil, uses DefaultRetryConfig (3 retries, 100ms-1s exponential backoff).
	RetryConfig *sharedclients.RetryConfig

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// NewPositionKeepingClient creates a new Position Keeping gRPC client.
//
// The client uses DNS-based service discovery and round-robin load balancing.
// RecordMeasurement calls include retry logic with exponential backoff for
// transient failures (UNAVAILABLE, INTERNAL) and skip retries for permanent
// errors (INVALID_ARGUMENT).
//
// Example:
//
//	config := &grpc.ClientConfig{
//	    ServiceName: "position-keeping",
//	    Namespace:   "default",
//	    Port:        50053,
//	    Timeout:     5 * time.Second,
//	    Logger:      logger,
//	}
//	client, err := grpc.NewPositionKeepingClient(config)
func NewPositionKeepingClient(cfg *ClientConfig) (*PositionKeepingGRPCClient, error) {
	if cfg.ServiceName == "" {
		return nil, ErrServiceNameRequired
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	// Use provided retry config or default
	retryConfig := sharedclients.DefaultRetryConfig()
	if cfg.RetryConfig != nil {
		retryConfig = *cfg.RetryConfig
	}
	// Override max interval to 1s per task requirements
	if retryConfig.MaxInterval > 1*time.Second {
		retryConfig.MaxInterval = 1 * time.Second
	}

	// Create gRPC connection using shared platform client
	conn, err := platformgrpc.NewClient(context.Background(), platformgrpc.ClientConfig{
		ServiceName: cfg.ServiceName,
		Namespace:   cfg.Namespace,
		Port:        cfg.Port,
		DialOptions: cfg.DialOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	client := &PositionKeepingGRPCClient{
		conn:        conn,
		client:      positionkeepingv1.NewPositionKeepingServiceClient(conn),
		timeout:     cfg.Timeout,
		logger:      cfg.Logger,
		retryConfig: retryConfig,
	}

	return client, nil
}

// RecordMeasurement sends a utilization measurement to Position Keeping.
//
// This method maps the domain Measurement to RecordMeasurementRequest proto
// and calls the Position Keeping service with retry logic for transient failures.
//
// Error handling:
//   - INVALID_ARGUMENT errors are logged and not retried (bad data)
//   - UNAVAILABLE/INTERNAL errors are retried with exponential backoff
//   - Context cancellation stops retries immediately
//
// Metrics are recorded for success/failure via the domain.Record* functions.
func (c *PositionKeepingGRPCClient) RecordMeasurement(ctx context.Context, measurement *auditdomain.Measurement) error {
	// Apply timeout
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Propagate context metadata
	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

	// Build the proto request from domain measurement
	req := c.buildRecordMeasurementRequest(measurement)

	var lastErr error
	err := sharedclients.Retry(ctx, c.retryConfig, func() error {
		startTime := time.Now()

		resp, err := c.client.RecordMeasurement(ctx, req)
		if err != nil {
			lastErr = err
			c.handleRecordMeasurementError(err, measurement)
			return err
		}

		// Success - record metrics
		duration := time.Since(startTime).Seconds()
		domain.RecordMeasurementRecorded(measurement.Attributes["service"], measurement.AssetCode)
		c.logger.Debug("recorded measurement to Position Keeping",
			"measurement_id", resp.MeasurementId,
			"position_state_id", resp.PositionStateId,
			"duration_seconds", duration,
		)
		return nil
	})
	if err != nil {
		// Return the underlying error, not the wrapped retry error
		if lastErr != nil {
			return lastErr
		}
		return err
	}

	return nil
}

// buildRecordMeasurementRequest converts a domain Measurement to a proto request.
//
// This method maps the auditdomain.Measurement to the RecordMeasurementRequest proto,
// leveraging the Universal Asset System's typed instrument definitions to provide
// richer metadata for Position Keeping.
//
// The mapping uses:
//   - AssetCode as measurement_type (e.g., "MERIDIAN-CURRENT-ACCOUNT-OPS")
//   - Quantity.String() as value (decimal precision preserved)
//   - Instrument-derived unit from the measurement type attribute
//   - Instrument metadata for typed quantity reconstruction on Position Keeping side
func (c *PositionKeepingGRPCClient) buildRecordMeasurementRequest(measurement *auditdomain.Measurement) *positionkeepingv1.RecordMeasurementRequest {
	// Map measurement fields to proto request
	// measurement_type is derived from AssetCode (e.g., "MERIDIAN-CURRENT-ACCOUNT-OPS")
	// The AssetCode already encodes the resource type
	measurementType := measurement.AssetCode

	// Value is the quantity as a decimal string
	value := measurement.Quantity.String()

	// Get the typed instrument based on the unit attribute.
	// This provides proper instrument metadata for Position Keeping to reconstruct
	// typed quantities using the Universal Asset System.
	unitAttr := "operation" // default unit type
	if attr, ok := measurement.Attributes["unit"]; ok {
		unitAttr = attr
	}
	instrument := domain.InstrumentForMeasurementType(unitAttr)

	// Use the instrument's dimension as the unit (e.g., "COUNT", "DATA", "COMPUTE")
	// This is more semantically accurate than the raw unit attribute
	unit := instrument.Dimension

	// Timestamp from the period start (audit events are point-in-time)
	timestamp := timestamppb.New(measurement.Period.Start)

	// Build metadata from measurement attributes
	metadata := make(map[string]string)
	for k, v := range measurement.Attributes {
		metadata[k] = v
	}
	// Add source info to metadata
	metadata["source"] = measurement.Source
	metadata["quality_score"] = fmt.Sprintf("%d", measurement.QualityScore)

	// Add instrument metadata for typed quantity reconstruction.
	// Position Keeping can use these fields to create properly typed quantities
	// using the Universal Asset System's InstrumentAmount proto or domain types.
	metadata["instrument_code"] = instrument.Code
	metadata["instrument_version"] = fmt.Sprintf("%d", instrument.Version)
	metadata["instrument_dimension"] = instrument.Dimension
	metadata["instrument_precision"] = fmt.Sprintf("%d", instrument.Precision)

	// Position state ID is the AccountID (the billing account for this tenant)
	positionStateID := measurement.AccountID.String()

	return &positionkeepingv1.RecordMeasurementRequest{
		MeasurementType: measurementType,
		Value:           value,
		Unit:            unit,
		Timestamp:       timestamp,
		Metadata:        metadata,
		PositionStateId: positionStateID,
	}
}

// handleRecordMeasurementError logs and records metrics for RecordMeasurement errors.
// Returns the error categorization for retry decisions.
func (c *PositionKeepingGRPCClient) handleRecordMeasurementError(err error, measurement *auditdomain.Measurement) {
	st, ok := status.FromError(err)
	if !ok {
		// Non-gRPC error
		domain.RecordPositionKeepingAPIError("unknown")
		c.logger.Error("non-gRPC error recording measurement",
			"error", err,
			"measurement_id", measurement.ID,
		)
		return
	}

	//exhaustive:ignore
	switch st.Code() {
	case codes.InvalidArgument:
		// Bad data - log and skip (will not be retried by sharedclients.IsRetryable)
		domain.RecordPositionKeepingAPIError("invalid_request")
		c.logger.Warn("invalid measurement data, skipping",
			"error", st.Message(),
			"measurement_id", measurement.ID,
			"asset_code", measurement.AssetCode,
		)
	case codes.Unavailable:
		// Transient - will be retried
		domain.RecordPositionKeepingAPIError("grpc_unavailable")
		c.logger.Warn("Position Keeping service unavailable, will retry",
			"error", st.Message(),
			"measurement_id", measurement.ID,
		)
	case codes.Internal:
		// Server error - will be retried
		domain.RecordPositionKeepingAPIError("grpc_internal")
		c.logger.Warn("Position Keeping internal error, will retry",
			"error", st.Message(),
			"measurement_id", measurement.ID,
		)
	case codes.DeadlineExceeded:
		// Timeout - will be retried
		domain.RecordPositionKeepingAPIError("timeout")
		c.logger.Warn("Position Keeping request timed out, will retry",
			"measurement_id", measurement.ID,
		)
	case codes.ResourceExhausted:
		// Rate limited - will be retried
		domain.RecordPositionKeepingAPIError("rate_limited")
		c.logger.Warn("Position Keeping rate limited, will retry",
			"measurement_id", measurement.ID,
		)
	default:
		// Other errors (OK, Canceled, Unknown, NotFound, AlreadyExists, PermissionDenied,
		// FailedPrecondition, Aborted, OutOfRange, Unimplemented, DataLoss, Unauthenticated)
		domain.RecordPositionKeepingAPIError(st.Code().String())
		c.logger.Error("unexpected error recording measurement",
			"code", st.Code().String(),
			"error", st.Message(),
			"measurement_id", measurement.ID,
		)
	}
}

// Close releases the gRPC client connection.
func (c *PositionKeepingGRPCClient) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close position keeping client connection: %w", err)
		}
	}
	return nil
}
