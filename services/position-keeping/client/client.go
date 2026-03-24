// Package client provides a gRPC client for the PositionKeeping service.
//
// The PositionKeeping service maintains comprehensive financial position logs,
// capturing transaction entries, lineage, audit trails, and status tracking.
// This client enables inter-service communication with proper context propagation,
// tracing, and resilience patterns.
//
// Usage with Kubernetes DNS-based load balancing (recommended for production):
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "position-keeping",
//	    Namespace:   "default",
//	    Port:        50053,
//	    Tracer:      tracer,
//	})
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
//
// Usage with direct connection (for local development):
//
//	client, cleanup, err := client.New(client.Config{
//	    Target:  "localhost:50053",
//	    Timeout: 30 * time.Second,
//	})
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// DefaultPort is the default gRPC port for the PositionKeeping service.
	DefaultPort = 50053

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for PositionKeeping.
	ServiceName = "position-keeping"
)

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = errors.New("either Target or ServiceName must be provided")

// Config holds configuration for the PositionKeeping client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50053" or "position-keeping:50053").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "position-keeping").
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50053 if not specified.
	Port int

	// Timeout is the default timeout for RPC calls.
	// Defaults to 30 seconds if not specified.
	Timeout time.Duration

	// Tracer is an optional observability tracer for distributed tracing.
	// If provided, the client will automatically propagate trace context.
	Tracer *observability.Tracer

	// Resilience is an optional configuration for circuit breaker and retry.
	// If provided, calls will be wrapped with resilience patterns.
	Resilience *clients.ResilientClientConfig

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// Client provides access to the PositionKeeping service.
type Client struct {
	conn            *grpc.ClientConn
	positionKeeping positionkeepingv1.PositionKeepingServiceClient
	tracer          *observability.Tracer
	resilient       *clients.ResilientClient
	timeout         time.Duration
}

// New creates a new PositionKeeping gRPC client.
//
// Returns the client, a cleanup function to close the connection, and any error.
// The cleanup function should be deferred immediately after checking the error.
//
// Example:
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "position-keeping",
//	    Namespace:   "default",
//	    Port:        50053,
//	})
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
func New(ctx context.Context, cfg Config) (*Client, func(), error) {
	// Apply defaults
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}

	var conn *grpc.ClientConn
	var err error

	// Use platform gRPC factory when ServiceName is provided (preferred)
	if cfg.ServiceName != "" {
		dialOpts := cfg.DialOptions

		// Add tracing interceptors if tracer is provided
		// Use WithChainUnaryInterceptor/WithChainStreamInterceptor to properly chain
		// multiple interceptors instead of overwriting them
		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithChainUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithChainStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
			)
		}

		// Use platform factory for DNS-based load balancing
		conn, err = platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
			ServiceName: cfg.ServiceName,
			Namespace:   cfg.Namespace,
			Port:        cfg.Port,
			DialOptions: dialOpts,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create position-keeping gRPC connection via platform factory: %w", err)
		}
	} else if cfg.Target != "" {
		// Fallback to legacy direct connection for backward compatibility
		dialOpts := cfg.DialOptions
		if dialOpts == nil {
			dialOpts = []grpc.DialOption{
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			}
		}

		// Add tracing interceptors if tracer is provided
		// Use WithChainUnaryInterceptor/WithChainStreamInterceptor to properly chain
		// multiple interceptors instead of overwriting them
		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithChainUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithChainStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
			)
		}

		conn, err = grpc.NewClient(cfg.Target, dialOpts...)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create position-keeping gRPC connection to %s: %w", cfg.Target, err)
		}
	} else {
		return nil, nil, ErrTargetRequired
	}

	// Create resilient client if configuration is provided
	var resilient *clients.ResilientClient
	if cfg.Resilience != nil {
		resilient = clients.NewResilientClient(*cfg.Resilience)
	}

	client := &Client{
		conn:            conn,
		positionKeeping: positionkeepingv1.NewPositionKeepingServiceClient(conn),
		tracer:          cfg.Tracer,
		resilient:       resilient,
		timeout:         cfg.Timeout,
	}

	cleanup := func() {
		if client.conn != nil {
			_ = client.conn.Close()
		}
	}

	return client, cleanup, nil
}

// InitiateFinancialPositionLog creates a new financial position log.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) InitiateFinancialPositionLog(ctx context.Context, req *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "InitiateFinancialPositionLog", func() (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
			return c.positionKeeping.InitiateFinancialPositionLog(ctx, req)
		})
	}

	resp, err := c.positionKeeping.InitiateFinancialPositionLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate financial position log: %w", err)
	}

	return resp, nil
}

// InitiateFinancialPositionLogBatch creates multiple logs atomically in a single transaction.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) InitiateFinancialPositionLogBatch(ctx context.Context, req *positionkeepingv1.InitiateFinancialPositionLogBatchRequest) (*positionkeepingv1.InitiateFinancialPositionLogBatchResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "InitiateFinancialPositionLogBatch", func() (*positionkeepingv1.InitiateFinancialPositionLogBatchResponse, error) {
			return c.positionKeeping.InitiateFinancialPositionLogBatch(ctx, req)
		})
	}

	resp, err := c.positionKeeping.InitiateFinancialPositionLogBatch(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate financial position log batch: %w", err)
	}

	return resp, nil
}

// UpdateFinancialPositionLog updates an existing financial position log.
// Updates are idempotent when using version-based concurrency, so retry is enabled.
func (c *Client) UpdateFinancialPositionLog(ctx context.Context, req *positionkeepingv1.UpdateFinancialPositionLogRequest) (*positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent update)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "UpdateFinancialPositionLog", func() (*positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
			return c.positionKeeping.UpdateFinancialPositionLog(ctx, req)
		})
	}

	resp, err := c.positionKeeping.UpdateFinancialPositionLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to update financial position log: %w", err)
	}

	return resp, nil
}

// RetrieveFinancialPositionLog retrieves a specific financial position log.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) RetrieveFinancialPositionLog(ctx context.Context, req *positionkeepingv1.RetrieveFinancialPositionLogRequest) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveFinancialPositionLog", func() (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
			return c.positionKeeping.RetrieveFinancialPositionLog(ctx, req)
		})
	}

	resp, err := c.positionKeeping.RetrieveFinancialPositionLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve financial position log: %w", err)
	}

	return resp, nil
}

// BulkImportTransactions imports multiple transactions into a log at once.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) BulkImportTransactions(ctx context.Context, req *positionkeepingv1.BulkImportTransactionsRequest) (*positionkeepingv1.BulkImportTransactionsResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "BulkImportTransactions", func() (*positionkeepingv1.BulkImportTransactionsResponse, error) {
			return c.positionKeeping.BulkImportTransactions(ctx, req)
		})
	}

	resp, err := c.positionKeeping.BulkImportTransactions(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk import transactions: %w", err)
	}

	return resp, nil
}

// ListFinancialPositionLogs lists financial position logs with filtering.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) ListFinancialPositionLogs(ctx context.Context, req *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "ListFinancialPositionLogs", func() (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
			return c.positionKeeping.ListFinancialPositionLogs(ctx, req)
		})
	}

	resp, err := c.positionKeeping.ListFinancialPositionLogs(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list financial position logs: %w", err)
	}

	return resp, nil
}

// GetAccountBalance retrieves a specific balance type for an account.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) GetAccountBalance(ctx context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "GetAccountBalance", func() (*positionkeepingv1.GetAccountBalanceResponse, error) {
			return c.positionKeeping.GetAccountBalance(ctx, req)
		})
	}

	resp, err := c.positionKeeping.GetAccountBalance(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get account balance: %w", err)
	}

	return resp, nil
}

// GetAccountBalances retrieves all balance types for an account.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) GetAccountBalances(ctx context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "GetAccountBalances", func() (*positionkeepingv1.GetAccountBalancesResponse, error) {
			return c.positionKeeping.GetAccountBalances(ctx, req)
		})
	}

	resp, err := c.positionKeeping.GetAccountBalances(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get account balances: %w", err)
	}

	return resp, nil
}

// ReleaseReservation transitions a reservation to EXECUTED or TERMINATED status.
// This is idempotent (releasing an already-released reservation returns success),
// so it uses circuit breaker with retry.
func (c *Client) ReleaseReservation(ctx context.Context, req *positionkeepingv1.ReleaseReservationRequest) (*positionkeepingv1.ReleaseReservationResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "ReleaseReservation", func() (*positionkeepingv1.ReleaseReservationResponse, error) {
			return c.positionKeeping.ReleaseReservation(ctx, req)
		})
	}

	resp, err := c.positionKeeping.ReleaseReservation(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to release reservation: %w", err)
	}

	return resp, nil
}

// Close terminates the gRPC connection gracefully.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close position-keeping client connection: %w", err)
		}
	}
	return nil
}

// Conn returns the underlying gRPC connection for creating additional clients
// (e.g., health check clients that bypass the business client's circuit breaker).
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}
