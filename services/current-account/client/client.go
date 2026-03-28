// Package client provides a gRPC client for the CurrentAccount service.
//
// The CurrentAccount service provides BIAN-compliant current account operations
// including account management, deposits, and lien operations. This client enables
// inter-service communication with proper context propagation, tracing, and
// resilience patterns.
//
// Usage with Kubernetes DNS-based load balancing (recommended for production):
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "current-account",
//	    Namespace:   "default",
//	    Port:        50051,
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
//	    Target:  "localhost:50051",
//	    Timeout: 30 * time.Second,
//	})
package client

import (
	"context"
	"fmt"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/validation"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// DefaultPort is the default gRPC port for the CurrentAccount service.
	DefaultPort = 50051

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for CurrentAccount.
	ServiceName = "current-account"
)

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = clients.ErrConnTargetRequired

// Config holds configuration for the CurrentAccount client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50051" or "current-account:50051").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "current-account").
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50051 if not specified.
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

// Client provides access to the CurrentAccount service.
type Client struct {
	conn           *grpc.ClientConn
	currentAccount currentaccountv1.CurrentAccountServiceClient
	tracer         *observability.Tracer
	resilient      *clients.ResilientClient
	timeout        time.Duration
}

// New creates a new CurrentAccount gRPC client.
//
// Returns the client, a cleanup function to close the connection, and any error.
// The cleanup function should be deferred immediately after checking the error.
//
// Example:
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "current-account",
//	    Namespace:   "default",
//	    Port:        50051,
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
		conn:           conn,
		currentAccount: currentaccountv1.NewCurrentAccountServiceClient(conn),
		tracer:         cfg.Tracer,
		resilient:      resilient,
		timeout:        cfg.Timeout,
	}, cleanup, nil
}

// InitiateCurrentAccount creates a new current account facility.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) InitiateCurrentAccount(ctx context.Context, req *currentaccountv1.InitiateCurrentAccountRequest) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "InitiateCurrentAccount", func() (*currentaccountv1.InitiateCurrentAccountResponse, error) {
			return c.currentAccount.InitiateCurrentAccount(ctx, req)
		})
	}

	resp, err := c.currentAccount.InitiateCurrentAccount(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate current account: %w", err)
	}

	return resp, nil
}

// ExecuteDeposit processes a deposit transaction (Behavior Qualifier).
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) ExecuteDeposit(ctx context.Context, req *currentaccountv1.ExecuteDepositRequest) (*currentaccountv1.ExecuteDepositResponse, error) {
	if err := validation.ValidateAccountID(req.GetAccountId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_id format: %v", err)
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "ExecuteDeposit", func() (*currentaccountv1.ExecuteDepositResponse, error) {
			return c.currentAccount.ExecuteDeposit(ctx, req)
		})
	}

	resp, err := c.currentAccount.ExecuteDeposit(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute deposit: %w", err)
	}

	return resp, nil
}

// RetrieveCurrentAccount gets current account details.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) RetrieveCurrentAccount(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
	if err := validation.ValidateAccountID(req.GetAccountId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_id format: %v", err)
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveCurrentAccount", func() (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return c.currentAccount.RetrieveCurrentAccount(ctx, req)
		})
	}

	resp, err := c.currentAccount.RetrieveCurrentAccount(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve current account: %w", err)
	}

	return resp, nil
}

// InitiateLien creates a fund reservation on an account.
// Used by Payment Order to reserve funds before external payment execution.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) InitiateLien(ctx context.Context, req *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
	if err := validation.ValidateAccountID(req.GetAccountId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_id format: %v", err)
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "InitiateLien", func() (*currentaccountv1.InitiateLienResponse, error) {
			return c.currentAccount.InitiateLien(ctx, req)
		})
	}

	resp, err := c.currentAccount.InitiateLien(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate lien: %w", err)
	}

	return resp, nil
}

// ExecuteLien converts a reservation to an actual debit atomically.
// Called when the external payment is confirmed as settled.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) ExecuteLien(ctx context.Context, req *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "ExecuteLien", func() (*currentaccountv1.ExecuteLienResponse, error) {
			return c.currentAccount.ExecuteLien(ctx, req)
		})
	}

	resp, err := c.currentAccount.ExecuteLien(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute lien: %w", err)
	}

	return resp, nil
}

// TerminateLien releases a reservation without executing.
// Called when the external payment fails or is cancelled.
// Lien termination is idempotent (can be called multiple times safely).
func (c *Client) TerminateLien(ctx context.Context, req *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent operation)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "TerminateLien", func() (*currentaccountv1.TerminateLienResponse, error) {
			return c.currentAccount.TerminateLien(ctx, req)
		})
	}

	resp, err := c.currentAccount.TerminateLien(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to terminate lien: %w", err)
	}

	return resp, nil
}

// RetrieveLien gets lien details.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) RetrieveLien(ctx context.Context, req *currentaccountv1.RetrieveLienRequest) (*currentaccountv1.RetrieveLienResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveLien", func() (*currentaccountv1.RetrieveLienResponse, error) {
			return c.currentAccount.RetrieveLien(ctx, req)
		})
	}

	resp, err := c.currentAccount.RetrieveLien(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve lien: %w", err)
	}

	return resp, nil
}

// UpdateCurrentAccount updates account settings like overdraft limits.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) UpdateCurrentAccount(ctx context.Context, req *currentaccountv1.UpdateCurrentAccountRequest) (*currentaccountv1.UpdateCurrentAccountResponse, error) {
	if err := validation.ValidateAccountID(req.GetAccountId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_id format: %v", err)
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "UpdateCurrentAccount", func() (*currentaccountv1.UpdateCurrentAccountResponse, error) {
			return c.currentAccount.UpdateCurrentAccount(ctx, req)
		})
	}

	resp, err := c.currentAccount.UpdateCurrentAccount(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to update current account: %w", err)
	}

	return resp, nil
}

// GetActiveAmountBlocks retrieves active fund reservations for balance calculations.
// Used by Position Keeping to query blocked amounts without coupling to lien details.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) GetActiveAmountBlocks(ctx context.Context, req *currentaccountv1.GetActiveAmountBlocksRequest) (*currentaccountv1.GetActiveAmountBlocksResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "GetActiveAmountBlocks", func() (*currentaccountv1.GetActiveAmountBlocksResponse, error) {
			return c.currentAccount.GetActiveAmountBlocks(ctx, req)
		})
	}

	resp, err := c.currentAccount.GetActiveAmountBlocks(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get active amount blocks: %w", err)
	}

	return resp, nil
}

// ControlCurrentAccount performs lifecycle state transitions on an account.
// Used by dunning sagas to freeze/unfreeze accounts.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) ControlCurrentAccount(ctx context.Context, req *currentaccountv1.ControlCurrentAccountRequest) (*currentaccountv1.ControlCurrentAccountResponse, error) {
	if err := validation.ValidateAccountID(req.GetAccountId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_id format: %v", err)
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "ControlCurrentAccount", func() (*currentaccountv1.ControlCurrentAccountResponse, error) {
			return c.currentAccount.ControlCurrentAccount(ctx, req)
		})
	}

	resp, err := c.currentAccount.ControlCurrentAccount(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to control current account: %w", err)
	}

	return resp, nil
}

// Close terminates the gRPC connection gracefully.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close current-account client connection: %w", err)
		}
	}
	return nil
}

// Conn returns the underlying gRPC connection for creating additional clients
// (e.g., health check clients that bypass the business client's circuit breaker).
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}
