// Package client provides a gRPC client for the InternalAccount service.
//
// The InternalAccount service provides BIAN-compliant internal account
// operations for managing non-customer-facing accounts including clearing, nostro,
// vostro, holding, suspense, revenue, expense, and inventory accounts. This client
// enables inter-service communication with proper context propagation, tracing, and
// resilience patterns.
//
// Usage with Kubernetes DNS-based load balancing (recommended for production):
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "internal-account",
//	    Namespace:   "default",
//	    Port:        50057,
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
//	    Target:  "localhost:50057",
//	    Timeout: 30 * time.Second,
//	})
package client

import (
	"context"
	"fmt"
	"time"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/validation"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// DefaultPort is the default gRPC port for the InternalAccount service.
	DefaultPort = 50057

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for InternalAccount.
	ServiceName = "internal-account"
)

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = clients.ErrConnTargetRequired

// Config holds configuration for the InternalAccount client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50057" or "internal-account:50057").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "internal-account").
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50057 if not specified.
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

// Client provides access to the InternalAccount service.
type Client struct {
	conn            *grpc.ClientConn
	internalAccount internalaccountv1.InternalAccountServiceClient
	tracer          *observability.Tracer
	resilient       *clients.ResilientClient
	timeout         time.Duration
}

// New creates a new InternalAccount gRPC client.
//
// Returns the client, a cleanup function to close the connection, and any error.
// The cleanup function should be deferred immediately after checking the error.
//
// Example:
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "internal-account",
//	    Namespace:   "default",
//	    Port:        50057,
//	})
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
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
		conn:            conn,
		internalAccount: internalaccountv1.NewInternalAccountServiceClient(conn),
		tracer:          cfg.Tracer,
		resilient:       resilient,
		timeout:         cfg.Timeout,
	}, cleanup, nil
}

// Close terminates the gRPC connection gracefully.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close internal-account client connection: %w", err)
		}
	}
	return nil
}

// Conn returns the underlying gRPC connection for creating additional clients
// (e.g., health check clients that bypass the business client's circuit breaker).
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}

// ============================================================================
// Write Operations (Non-Idempotent) - Circuit breaker WITHOUT retry
// ============================================================================

// InitiateInternalAccount creates a new internal account.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) InitiateInternalAccount(ctx context.Context, req *internalaccountv1.InitiateInternalAccountRequest) (*internalaccountv1.InitiateInternalAccountResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "InitiateInternalAccount", func() (*internalaccountv1.InitiateInternalAccountResponse, error) {
			return c.internalAccount.InitiateInternalAccount(ctx, req)
		})
	}

	resp, err := c.internalAccount.InitiateInternalAccount(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate internal account: %w", err)
	}

	return resp, nil
}

// ControlInternalAccount performs lifecycle state transitions (suspend, activate, close).
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) ControlInternalAccount(ctx context.Context, req *internalaccountv1.ControlInternalAccountRequest) (*internalaccountv1.ControlInternalAccountResponse, error) {
	if err := validation.ValidateAccountID(req.GetAccountId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_id format: %v", err)
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "ControlInternalAccount", func() (*internalaccountv1.ControlInternalAccountResponse, error) {
			return c.internalAccount.ControlInternalAccount(ctx, req)
		})
	}

	resp, err := c.internalAccount.ControlInternalAccount(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to control internal account: %w", err)
	}

	return resp, nil
}

// ============================================================================
// Read/Idempotent Operations - Circuit breaker WITH retry
// ============================================================================

// UpdateInternalAccount modifies account settings (partial update).
// Updates are idempotent when using version-based concurrency (expected_version),
// so retry is enabled.
func (c *Client) UpdateInternalAccount(ctx context.Context, req *internalaccountv1.UpdateInternalAccountRequest) (*internalaccountv1.UpdateInternalAccountResponse, error) {
	if err := validation.ValidateAccountID(req.GetAccountId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_id format: %v", err)
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent update)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "UpdateInternalAccount", func() (*internalaccountv1.UpdateInternalAccountResponse, error) {
			return c.internalAccount.UpdateInternalAccount(ctx, req)
		})
	}

	resp, err := c.internalAccount.UpdateInternalAccount(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to update internal account: %w", err)
	}

	return resp, nil
}

// RetrieveInternalAccount fetches a single account by ID.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) RetrieveInternalAccount(ctx context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
	if err := validation.ValidateAccountID(req.GetAccountId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_id format: %v", err)
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveInternalAccount", func() (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return c.internalAccount.RetrieveInternalAccount(ctx, req)
		})
	}

	resp, err := c.internalAccount.RetrieveInternalAccount(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve internal account: %w", err)
	}

	return resp, nil
}

// ListInternalAccounts queries accounts with filtering and pagination.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) ListInternalAccounts(ctx context.Context, req *internalaccountv1.ListInternalAccountsRequest) (*internalaccountv1.ListInternalAccountsResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "ListInternalAccounts", func() (*internalaccountv1.ListInternalAccountsResponse, error) {
			return c.internalAccount.ListInternalAccounts(ctx, req)
		})
	}

	resp, err := c.internalAccount.ListInternalAccounts(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list internal accounts: %w", err)
	}

	return resp, nil
}

// GetBalance queries the current balance for an internal account.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) GetBalance(ctx context.Context, req *internalaccountv1.GetBalanceRequest) (*internalaccountv1.GetBalanceResponse, error) {
	if err := validation.ValidateAccountID(req.GetAccountId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_id format: %v", err)
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "GetBalance", func() (*internalaccountv1.GetBalanceResponse, error) {
			return c.internalAccount.GetBalance(ctx, req)
		})
	}

	resp, err := c.internalAccount.GetBalance(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	return resp, nil
}
