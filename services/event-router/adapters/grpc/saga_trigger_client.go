// Package grpc provides gRPC client adapters for external service communication.
package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// ErrSagaTriggerConfigRequired is returned when a nil config is passed to NewSagaTriggerClient.
var ErrSagaTriggerConfigRequired = fmt.Errorf("SagaTriggerClientConfig is required")

// ErrSagaTriggerServiceNameRequired is returned when ServiceName is not provided.
var ErrSagaTriggerServiceNameRequired = fmt.Errorf("ServiceName is required for saga trigger client")

// SagaTriggerClient implements domain.SagaTrigger using gRPC.
// It calls the control-plane's SagaExecutionService to trigger saga instances
// from Kafka events, with retry logic for transient failures.
type SagaTriggerClient struct {
	conn        *grpc.ClientConn
	client      controlplanev1.SagaExecutionServiceClient
	timeout     time.Duration
	logger      *slog.Logger
	retryConfig sharedclients.RetryConfig
}

// SagaTriggerClientConfig holds configuration for creating a SagaTriggerClient.
type SagaTriggerClientConfig struct {
	// ServiceName is the Kubernetes service name (e.g., "control-plane").
	// Required for DNS-based load balancing.
	ServiceName string

	// Namespace is the Kubernetes namespace (defaults to "default" if empty).
	Namespace string

	// Port is the service gRPC port number.
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

// NewSagaTriggerClient creates a new gRPC client for the control-plane SagaExecutionService.
//
// The client uses DNS-based service discovery and round-robin load balancing.
// TriggerSaga calls include retry logic with exponential backoff for transient
// failures (UNAVAILABLE, INTERNAL, DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED) and
// skip retries for permanent errors (INVALID_ARGUMENT, NOT_FOUND).
//
// Example:
//
//	config := &grpc.SagaTriggerClientConfig{
//	    ServiceName: "control-plane",
//	    Namespace:   "default",
//	    Port:        50051,
//	    Timeout:     5 * time.Second,
//	    Logger:      logger,
//	}
//	client, err := grpc.NewSagaTriggerClient(config)
func NewSagaTriggerClient(cfg *SagaTriggerClientConfig) (*SagaTriggerClient, error) {
	if cfg == nil {
		return nil, ErrSagaTriggerConfigRequired
	}

	if cfg.ServiceName == "" {
		return nil, ErrSagaTriggerServiceNameRequired
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	retryConfig := sharedclients.DefaultRetryConfig()
	if cfg.RetryConfig != nil {
		retryConfig = *cfg.RetryConfig
	}
	// Cap max interval at 1s to avoid excessive backoff on saga trigger retries
	if retryConfig.MaxInterval > 1*time.Second {
		retryConfig.MaxInterval = 1 * time.Second
	}

	conn, err := platformgrpc.NewClient(context.Background(), platformgrpc.ClientConfig{
		ServiceName: cfg.ServiceName,
		Namespace:   cfg.Namespace,
		Port:        cfg.Port,
		DialOptions: cfg.DialOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	return &SagaTriggerClient{
		conn:        conn,
		client:      controlplanev1.NewSagaExecutionServiceClient(conn),
		timeout:     cfg.Timeout,
		logger:      cfg.Logger,
		retryConfig: retryConfig,
	}, nil
}

// TriggerSaga starts a new saga instance by name with the given input data.
//
// The idempotencyKey ensures duplicate triggers (e.g., Kafka at-least-once delivery)
// are handled safely. If a saga with the same key already exists, the existing
// saga ID is returned without re-executing.
//
// Error handling:
//   - INVALID_ARGUMENT errors are not retried (bad saga name or input)
//   - NOT_FOUND errors are not retried (saga definition does not exist)
//   - UNAVAILABLE / INTERNAL / DEADLINE_EXCEEDED / RESOURCE_EXHAUSTED errors are retried with backoff
//   - Context cancellation stops retries immediately
func (c *SagaTriggerClient) TriggerSaga(ctx context.Context, sagaName string, inputData map[string]any, idempotencyKey string) (string, error) {
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

	req, err := c.buildRequest(sagaName, inputData, idempotencyKey)
	if err != nil {
		return "", err
	}

	var (
		sagaID  string
		lastErr error
	)

	err = sharedclients.Retry(ctx, c.retryConfig, func() error {
		resp, rpcErr := c.client.ExecuteSaga(ctx, req)
		if rpcErr != nil {
			lastErr = rpcErr
			c.handleError(rpcErr, sagaName)
			return rpcErr
		}

		sagaID = resp.GetSagaId()

		if resp.GetWasDuplicate() {
			c.logger.Debug("saga trigger was duplicate, returning existing instance",
				"saga_name", sagaName,
				"saga_id", sagaID,
			)
		} else {
			c.logger.Debug("saga triggered successfully",
				"saga_name", sagaName,
				"saga_id", sagaID,
			)
		}
		return nil
	})
	if err != nil {
		// If the context was cancelled or timed out, return the context error
		// rather than the last RPC error which may be stale.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if lastErr != nil {
			return "", lastErr
		}
		return "", err
	}

	return sagaID, nil
}

// buildRequest constructs the ExecuteSagaRequest proto from the given parameters.
// Returns an error if inputData contains types that cannot be serialized to structpb.
func (c *SagaTriggerClient) buildRequest(sagaName string, inputData map[string]any, idempotencyKey string) (*controlplanev1.ExecuteSagaRequest, error) {
	req := &controlplanev1.ExecuteSagaRequest{
		SagaName:       sagaName,
		IdempotencyKey: idempotencyKey,
	}

	if len(inputData) > 0 {
		s, err := structpb.NewStruct(inputData)
		if err != nil {
			return nil, fmt.Errorf("invalid input_data: cannot serialize to protobuf struct: %w", err)
		}
		req.InputData = s
	}

	return req, nil
}

// handleError logs and categorizes gRPC errors from ExecuteSaga calls.
func (c *SagaTriggerClient) handleError(err error, sagaName string) {
	st, ok := status.FromError(err)
	if !ok {
		c.logger.Error("non-gRPC error triggering saga",
			"error", err,
			"saga_name", sagaName,
		)
		return
	}

	//exhaustive:ignore
	switch st.Code() {
	case codes.InvalidArgument:
		c.logger.Warn("invalid saga trigger request, will not retry",
			"error", st.Message(),
			"saga_name", sagaName,
		)
	case codes.NotFound:
		c.logger.Warn("saga definition not found, will not retry",
			"error", st.Message(),
			"saga_name", sagaName,
		)
	case codes.Unavailable:
		c.logger.Warn("control-plane unavailable, will retry",
			"error", st.Message(),
			"saga_name", sagaName,
		)
	case codes.Internal:
		c.logger.Warn("control-plane internal error, will retry",
			"error", st.Message(),
			"saga_name", sagaName,
		)
	case codes.DeadlineExceeded:
		c.logger.Warn("saga trigger timed out, will retry",
			"saga_name", sagaName,
		)
	case codes.ResourceExhausted:
		c.logger.Warn("control-plane rate limited, will retry",
			"saga_name", sagaName,
		)
	default:
		c.logger.Error("unexpected error triggering saga",
			"code", st.Code().String(),
			"error", st.Message(),
			"saga_name", sagaName,
		)
	}
}

// Close releases the gRPC client connection.
func (c *SagaTriggerClient) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close saga trigger client connection: %w", err)
		}
	}
	return nil
}
