// Package grpc provides gRPC client adapters for external service communication.
package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/internal-account/observability"
	"github.com/meridianhub/meridian/services/internal-account/service"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Compile-time assertion that PositionKeepingGRPCClient implements service.PositionKeepingClient.
var _ service.PositionKeepingClient = (*PositionKeepingGRPCClient)(nil)

// Static errors for validation
var (
	// ErrServiceNameRequired is returned when ServiceName is not provided in client configuration
	ErrServiceNameRequired = fmt.Errorf("ServiceName is required for position keeping client")
)

// PositionKeepingGRPCClient implements service.PositionKeepingClient using gRPC.
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
// GetAccountBalances calls include retry logic with exponential backoff for
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

// GetAccountBalances retrieves all balance types for an account by instrument.
//
// This method calls Position Keeping's GetAccountBalances RPC with retry logic
// for transient failures. The operation is O(1) as Position Keeping maintains
// pre-computed running balances.
//
// Error handling:
//   - INVALID_ARGUMENT errors are not retried (bad data)
//   - UNAVAILABLE/INTERNAL errors are retried with exponential backoff
//   - Context cancellation stops retries immediately
//
// Metrics are recorded for latency and success/failure.
func (c *PositionKeepingGRPCClient) GetAccountBalances(ctx context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	// Apply timeout
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Propagate context metadata
	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

	var lastErr error
	var resp *positionkeepingv1.GetAccountBalancesResponse

	startTime := time.Now()

	err := sharedclients.Retry(ctx, c.retryConfig, func() error {
		var err error
		resp, err = c.client.GetAccountBalances(ctx, req)
		if err != nil {
			lastErr = err
			c.handleGetAccountBalancesError(err, req.AccountId)
			return err
		}

		return nil
	})

	duration := time.Since(startTime)

	if err != nil {
		// Record failure metrics
		observability.RecordOperationDuration("get_account_balances", "error", duration)

		// Return the underlying error, not the wrapped retry error
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, err
	}

	// Record success metrics
	observability.RecordOperationDuration("get_account_balances", "success", duration)
	c.logger.Debug("retrieved account balances from Position Keeping",
		"account_id", req.AccountId,
		"instrument_code", req.InstrumentCode,
		"balance_count", len(resp.Balances),
		"duration_seconds", duration.Seconds(),
	)

	return resp, nil
}

// handleGetAccountBalancesError logs and records metrics for GetAccountBalances errors.
func (c *PositionKeepingGRPCClient) handleGetAccountBalancesError(err error, accountID string) {
	st, ok := status.FromError(err)
	if !ok {
		// Non-gRPC error
		c.logger.Error("non-gRPC error getting account balances",
			"error", err,
			"account_id", accountID,
		)
		return
	}

	//exhaustive:ignore
	switch st.Code() {
	case codes.InvalidArgument:
		// Bad data - will not be retried by sharedclients.IsRetryable
		c.logger.Warn("invalid request for account balances",
			"error", st.Message(),
			"account_id", accountID,
		)
	case codes.NotFound:
		// Position not found - permanent error
		c.logger.Warn("position not found for account",
			"error", st.Message(),
			"account_id", accountID,
		)
	case codes.Unavailable:
		// Transient - will be retried
		c.logger.Warn("Position Keeping service unavailable, will retry",
			"error", st.Message(),
			"account_id", accountID,
		)
	case codes.Internal:
		// Server error - will be retried
		c.logger.Warn("Position Keeping internal error, will retry",
			"error", st.Message(),
			"account_id", accountID,
		)
	case codes.DeadlineExceeded:
		// Timeout - will be retried
		c.logger.Warn("Position Keeping request timed out, will retry",
			"account_id", accountID,
		)
	case codes.ResourceExhausted:
		// Rate limited - will be retried
		c.logger.Warn("Position Keeping rate limited, will retry",
			"account_id", accountID,
		)
	default:
		// Other errors
		c.logger.Error("unexpected error getting account balances",
			"code", st.Code().String(),
			"error", st.Message(),
			"account_id", accountID,
		)
	}
}

// GetAccountBalance retrieves a specific balance type for an account by instrument.
//
// Used by the valuation engine to query the current balance for an account's native instrument.
// Includes the same retry logic as GetAccountBalances.
func (c *PositionKeepingGRPCClient) GetAccountBalance(ctx context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	// Apply timeout
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Propagate context metadata
	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

	var lastErr error
	var resp *positionkeepingv1.GetAccountBalanceResponse

	startTime := time.Now()

	err := sharedclients.Retry(ctx, c.retryConfig, func() error {
		var err error
		resp, err = c.client.GetAccountBalance(ctx, req)
		if err != nil {
			lastErr = err
			c.handleGetAccountBalancesError(err, req.AccountId)
			return err
		}
		return nil
	})

	duration := time.Since(startTime)

	if err != nil {
		observability.RecordOperationDuration("get_account_balance", "error", duration)
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, err
	}

	observability.RecordOperationDuration("get_account_balance", "success", duration)
	c.logger.Debug("retrieved account balance from Position Keeping",
		"account_id", req.AccountId,
		"instrument_code", req.InstrumentCode,
		"duration_seconds", duration.Seconds(),
	)

	return resp, nil
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
