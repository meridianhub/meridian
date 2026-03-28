// Package client provides a gRPC client for the FinancialAccounting service.
//
// The FinancialAccounting service implements double-entry bookkeeping, managing
// financial booking logs and ledger postings. This client enables inter-service
// communication with proper context propagation, tracing, and resilience patterns.
//
// Usage with Kubernetes DNS-based load balancing (recommended for production):
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "financial-accounting",
//	    Namespace:   "default",
//	    Port:        50052,
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
//	    Target:  "localhost:50052",
//	    Timeout: 30 * time.Second,
//	})
package client

import (
	"context"
	"fmt"
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
)

const (
	// DefaultPort is the default gRPC port for the FinancialAccounting service.
	DefaultPort = 50052

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for FinancialAccounting.
	ServiceName = "financial-accounting"
)

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = clients.ErrConnTargetRequired

// Config holds configuration for the FinancialAccounting client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50052" or "financial-accounting:50052").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "financial-accounting").
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50052 if not specified.
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

// Client provides access to the FinancialAccounting service.
type Client struct {
	conn                *grpc.ClientConn
	financialAccounting financialaccountingv1.FinancialAccountingServiceClient
	tracer              *observability.Tracer
	resilient           *clients.ResilientClient
	timeout             time.Duration
}

// New creates a new FinancialAccounting gRPC client.
//
// Returns the client, a cleanup function to close the connection, and any error.
// The cleanup function should be deferred immediately after checking the error.
//
// Example:
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "financial-accounting",
//	    Namespace:   "default",
//	    Port:        50052,
//	})
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
func New(ctx context.Context, cfg Config) (*Client, func(), error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}

	conn, cleanup, err := clients.NewConn(ctx, clients.ConnConfig{
		Target:      cfg.Target,
		ServiceName: cfg.ServiceName,
		Namespace:   cfg.Namespace,
		Port:        cfg.Port,
		Tracer:      cfg.Tracer,
		DialOptions: cfg.DialOptions,
	})
	if err != nil {
		return nil, nil, err
	}

	var resilient *clients.ResilientClient
	if cfg.Resilience != nil {
		resilient = clients.NewResilientClient(*cfg.Resilience)
	}

	return &Client{
		conn:                conn,
		financialAccounting: financialaccountingv1.NewFinancialAccountingServiceClient(conn),
		tracer:              cfg.Tracer,
		resilient:           resilient,
		timeout:             cfg.Timeout,
	}, cleanup, nil
}

// InitiateFinancialBookingLog creates a new financial booking log.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) InitiateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "InitiateFinancialBookingLog", func() (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
			return c.financialAccounting.InitiateFinancialBookingLog(ctx, req)
		})
	}

	resp, err := c.financialAccounting.InitiateFinancialBookingLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate financial booking log: %w", err)
	}

	return resp, nil
}

// UpdateFinancialBookingLog updates an existing booking log.
// Updates are idempotent when using version-based concurrency, so retry is enabled.
func (c *Client) UpdateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent update)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "UpdateFinancialBookingLog", func() (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
			return c.financialAccounting.UpdateFinancialBookingLog(ctx, req)
		})
	}

	resp, err := c.financialAccounting.UpdateFinancialBookingLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to update financial booking log: %w", err)
	}

	return resp, nil
}

// RetrieveFinancialBookingLog retrieves a specific booking log.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) RetrieveFinancialBookingLog(ctx context.Context, req *financialaccountingv1.RetrieveFinancialBookingLogRequest) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveFinancialBookingLog", func() (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
			return c.financialAccounting.RetrieveFinancialBookingLog(ctx, req)
		})
	}

	resp, err := c.financialAccounting.RetrieveFinancialBookingLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve financial booking log: %w", err)
	}

	return resp, nil
}

// ListFinancialBookingLogs lists booking logs with optional filtering.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) ListFinancialBookingLogs(ctx context.Context, req *financialaccountingv1.ListFinancialBookingLogsRequest) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "ListFinancialBookingLogs", func() (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
			return c.financialAccounting.ListFinancialBookingLogs(ctx, req)
		})
	}

	resp, err := c.financialAccounting.ListFinancialBookingLogs(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list financial booking logs: %w", err)
	}

	return resp, nil
}

// CaptureLedgerPosting creates a new ledger posting.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) CaptureLedgerPosting(ctx context.Context, req *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "CaptureLedgerPosting", func() (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
			return c.financialAccounting.CaptureLedgerPosting(ctx, req)
		})
	}

	resp, err := c.financialAccounting.CaptureLedgerPosting(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to capture ledger posting: %w", err)
	}

	return resp, nil
}

// UpdateLedgerPosting updates an existing ledger posting.
// Updates are idempotent when using version-based concurrency, so retry is enabled.
func (c *Client) UpdateLedgerPosting(ctx context.Context, req *financialaccountingv1.UpdateLedgerPostingRequest) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent update)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "UpdateLedgerPosting", func() (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
			return c.financialAccounting.UpdateLedgerPosting(ctx, req)
		})
	}

	resp, err := c.financialAccounting.UpdateLedgerPosting(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to update ledger posting: %w", err)
	}

	return resp, nil
}

// RetrieveLedgerPosting retrieves a specific posting.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) RetrieveLedgerPosting(ctx context.Context, req *financialaccountingv1.RetrieveLedgerPostingRequest) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveLedgerPosting", func() (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
			return c.financialAccounting.RetrieveLedgerPosting(ctx, req)
		})
	}

	resp, err := c.financialAccounting.RetrieveLedgerPosting(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve ledger posting: %w", err)
	}

	return resp, nil
}

// ListLedgerPostings lists ledger postings with optional filtering.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) ListLedgerPostings(ctx context.Context, req *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "ListLedgerPostings", func() (*financialaccountingv1.ListLedgerPostingsResponse, error) {
			return c.financialAccounting.ListLedgerPostings(ctx, req)
		})
	}

	resp, err := c.financialAccounting.ListLedgerPostings(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list ledger postings: %w", err)
	}

	return resp, nil
}

// Close terminates the gRPC connection gracefully.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close financial-accounting client connection: %w", err)
		}
	}
	return nil
}

// Conn returns the underlying gRPC connection for creating additional clients
// (e.g., health check clients that bypass the business client's circuit breaker).
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}
