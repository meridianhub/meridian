// Package client provides a gRPC client for the AccountReconciliation service.
//
// The AccountReconciliation service manages settlement runs, variance detection,
// balance assertions, and dispute management. This client enables inter-service
// communication with proper context propagation, tracing, and resilience patterns.
//
// Usage with Kubernetes DNS-based load balancing (recommended for production):
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "reconciliation",
//	    Namespace:   "default",
//	    Port:        50060,
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
//	    Target:  "localhost:50060",
//	    Timeout: 30 * time.Second,
//	})
package client

import (
	"context"
	"fmt"
	"time"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"google.golang.org/grpc"
)

const (
	// DefaultPort is the default gRPC port for the AccountReconciliation service.
	DefaultPort = ports.Reconciliation

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for AccountReconciliation.
	ServiceName = "reconciliation"
)

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = clients.ErrConnTargetRequired

// Config holds configuration for the AccountReconciliation client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50060" or "reconciliation:50060").
	// If set, overrides Kubernetes DNS-based discovery.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "reconciliation").
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50060 if not specified.
	Port int

	// Timeout is the default timeout for RPC calls.
	// Defaults to 30 seconds if not specified.
	Timeout time.Duration

	// Tracer is an optional observability tracer for distributed tracing.
	Tracer *observability.Tracer

	// Resilience is an optional configuration for circuit breaker and retry.
	Resilience *clients.ResilientClientConfig

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// Client provides access to the AccountReconciliation service.
type Client struct {
	conn           *grpc.ClientConn
	reconciliation reconciliationv1.AccountReconciliationServiceClient
	tracer         *observability.Tracer
	resilient      *clients.ResilientClient
	timeout        time.Duration
}

// New creates a new AccountReconciliation gRPC client.
//
// Returns the client, a cleanup function to close the connection, and any error.
// The cleanup function should be deferred immediately after checking the error.
func New(cfg Config) (*Client, func(), error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}

	conn, cleanup, err := clients.NewConn(context.Background(), clients.ConnConfig{
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
		conn:           conn,
		reconciliation: reconciliationv1.NewAccountReconciliationServiceClient(conn),
		tracer:         cfg.Tracer,
		resilient:      resilient,
		timeout:        cfg.Timeout,
	}, cleanup, nil
}

// InitiateAccountReconciliation creates a new settlement run.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) InitiateAccountReconciliation(ctx context.Context, req *reconciliationv1.InitiateAccountReconciliationRequest) (*reconciliationv1.InitiateAccountReconciliationResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "InitiateAccountReconciliation", func() (*reconciliationv1.InitiateAccountReconciliationResponse, error) {
			return c.reconciliation.InitiateAccountReconciliation(ctx, req)
		})
	}

	resp, err := c.reconciliation.InitiateAccountReconciliation(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate account reconciliation: %w", err)
	}
	return resp, nil
}

// ExecuteAccountReconciliation triggers execution of a pending settlement run.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) ExecuteAccountReconciliation(ctx context.Context, req *reconciliationv1.ExecuteAccountReconciliationRequest) (*reconciliationv1.ExecuteAccountReconciliationResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "ExecuteAccountReconciliation", func() (*reconciliationv1.ExecuteAccountReconciliationResponse, error) {
			return c.reconciliation.ExecuteAccountReconciliation(ctx, req)
		})
	}

	resp, err := c.reconciliation.ExecuteAccountReconciliation(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute account reconciliation: %w", err)
	}
	return resp, nil
}

// RetrieveAccountReconciliation retrieves a settlement run summary.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) RetrieveAccountReconciliation(ctx context.Context, req *reconciliationv1.RetrieveAccountReconciliationRequest) (*reconciliationv1.RetrieveAccountReconciliationResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveAccountReconciliation", func() (*reconciliationv1.RetrieveAccountReconciliationResponse, error) {
			return c.reconciliation.RetrieveAccountReconciliation(ctx, req)
		})
	}

	resp, err := c.reconciliation.RetrieveAccountReconciliation(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve account reconciliation: %w", err)
	}
	return resp, nil
}

// ControlAccountReconciliation controls a settlement run (cancel, pause, resume).
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) ControlAccountReconciliation(ctx context.Context, req *reconciliationv1.ControlAccountReconciliationRequest) (*reconciliationv1.ControlAccountReconciliationResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "ControlAccountReconciliation", func() (*reconciliationv1.ControlAccountReconciliationResponse, error) {
			return c.reconciliation.ControlAccountReconciliation(ctx, req)
		})
	}

	resp, err := c.reconciliation.ControlAccountReconciliation(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to control account reconciliation: %w", err)
	}
	return resp, nil
}

// ListReconciliationResults returns paginated variance details for a run.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) ListReconciliationResults(ctx context.Context, req *reconciliationv1.ListReconciliationResultsRequest) (*reconciliationv1.ListReconciliationResultsResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "ListReconciliationResults", func() (*reconciliationv1.ListReconciliationResultsResponse, error) {
			return c.reconciliation.ListReconciliationResults(ctx, req)
		})
	}

	resp, err := c.reconciliation.ListReconciliationResults(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list reconciliation results: %w", err)
	}
	return resp, nil
}

// AssertBalance evaluates a balance assertion against current positions.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) AssertBalance(ctx context.Context, req *reconciliationv1.AssertBalanceRequest) (*reconciliationv1.AssertBalanceResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "AssertBalance", func() (*reconciliationv1.AssertBalanceResponse, error) {
			return c.reconciliation.AssertBalance(ctx, req)
		})
	}

	resp, err := c.reconciliation.AssertBalance(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to assert balance: %w", err)
	}
	return resp, nil
}

// InitiateDispute raises a formal dispute against a variance.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) InitiateDispute(ctx context.Context, req *reconciliationv1.InitiateDisputeRequest) (*reconciliationv1.InitiateDisputeResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "InitiateDispute", func() (*reconciliationv1.InitiateDisputeResponse, error) {
			return c.reconciliation.InitiateDispute(ctx, req)
		})
	}

	resp, err := c.reconciliation.InitiateDispute(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate dispute: %w", err)
	}
	return resp, nil
}

// ControlDispute controls a dispute lifecycle (escalate, resolve, reject).
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) ControlDispute(ctx context.Context, req *reconciliationv1.ControlDisputeRequest) (*reconciliationv1.ControlDisputeResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "ControlDispute", func() (*reconciliationv1.ControlDisputeResponse, error) {
			return c.reconciliation.ControlDispute(ctx, req)
		})
	}

	resp, err := c.reconciliation.ControlDispute(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to control dispute: %w", err)
	}
	return resp, nil
}

// RetrieveDispute retrieves a dispute by ID.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) RetrieveDispute(ctx context.Context, req *reconciliationv1.RetrieveDisputeRequest) (*reconciliationv1.RetrieveDisputeResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveDispute", func() (*reconciliationv1.RetrieveDisputeResponse, error) {
			return c.reconciliation.RetrieveDispute(ctx, req)
		})
	}

	resp, err := c.reconciliation.RetrieveDispute(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve dispute: %w", err)
	}
	return resp, nil
}

// Close terminates the gRPC connection gracefully.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close reconciliation client connection: %w", err)
		}
	}
	return nil
}

// Conn returns the underlying gRPC connection for creating additional clients.
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}
