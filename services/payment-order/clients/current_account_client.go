// Package clients provides gRPC client wrappers with resilience patterns.
//
// Deprecated: This package is deprecated. Use the service-owned client packages instead:
//   - For CurrentAccount: github.com/meridianhub/meridian/services/current-account/client
//   - For FinancialAccounting: github.com/meridianhub/meridian/services/financial-accounting/client
//
// The service-owned client packages provide standardized client creation with built-in
// DNS-based load balancing, tracing, and resilience patterns. This package will be
// removed in a future release once all consumers have migrated.
//
// Migration example:
//
//	// Old (deprecated):
//	client := payclients.NewResilientCurrentAccountClient(conn, config)
//
//	// New (preferred):
//	client, cleanup, err := currentaccountclient.New(currentaccountclient.Config{
//	    ServiceName: "current-account",
//	    Namespace:   "default",
//	    Tracer:      tracer,
//	    Resilience:  &resilientConfig,
//	})
//	defer cleanup()
package clients

import (
	"context"
	"fmt"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"google.golang.org/grpc"
)

// CurrentAccountClient defines the interface for current-account service operations.
// This interface allows for easy mocking in tests.
type CurrentAccountClient interface {
	InitiateLien(ctx context.Context, req *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error)
	TerminateLien(ctx context.Context, req *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error)
	ExecuteLien(ctx context.Context, req *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error)
	Close() error
}

// ResilientCurrentAccountClient wraps the gRPC client with resilience patterns.
// It provides circuit breaker protection and retry capabilities for all operations
// against the current-account service.
type ResilientCurrentAccountClient struct {
	client          currentAccountGRPCClient
	resilientClient *sharedclients.ResilientClient
}

// currentAccountGRPCClient is the internal interface for the gRPC client.
// This allows us to inject mocks for testing.
type currentAccountGRPCClient interface {
	InitiateLien(ctx context.Context, in *currentaccountv1.InitiateLienRequest, opts ...grpc.CallOption) (*currentaccountv1.InitiateLienResponse, error)
	TerminateLien(ctx context.Context, in *currentaccountv1.TerminateLienRequest, opts ...grpc.CallOption) (*currentaccountv1.TerminateLienResponse, error)
	ExecuteLien(ctx context.Context, in *currentaccountv1.ExecuteLienRequest, opts ...grpc.CallOption) (*currentaccountv1.ExecuteLienResponse, error)
	Close() error
}

// grpcClientWrapper wraps the generated gRPC client and connection
type grpcClientWrapper struct {
	conn   *grpc.ClientConn
	client currentaccountv1.CurrentAccountServiceClient
}

func (w *grpcClientWrapper) InitiateLien(ctx context.Context, in *currentaccountv1.InitiateLienRequest, opts ...grpc.CallOption) (*currentaccountv1.InitiateLienResponse, error) {
	return w.client.InitiateLien(ctx, in, opts...)
}

func (w *grpcClientWrapper) TerminateLien(ctx context.Context, in *currentaccountv1.TerminateLienRequest, opts ...grpc.CallOption) (*currentaccountv1.TerminateLienResponse, error) {
	return w.client.TerminateLien(ctx, in, opts...)
}

func (w *grpcClientWrapper) ExecuteLien(ctx context.Context, in *currentaccountv1.ExecuteLienRequest, opts ...grpc.CallOption) (*currentaccountv1.ExecuteLienResponse, error) {
	return w.client.ExecuteLien(ctx, in, opts...)
}

func (w *grpcClientWrapper) Close() error {
	return w.conn.Close()
}

// NewResilientCurrentAccountClient creates a resilient wrapper around the
// CurrentAccount gRPC client. The connection must be established before
// calling this function.
func NewResilientCurrentAccountClient(
	conn *grpc.ClientConn,
	config sharedclients.ResilientClientConfig,
) *ResilientCurrentAccountClient {
	// Apply default name if not provided
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = "current-account"
	}

	wrapper := &grpcClientWrapper{
		conn:   conn,
		client: currentaccountv1.NewCurrentAccountServiceClient(conn),
	}

	return &ResilientCurrentAccountClient{
		client:          wrapper,
		resilientClient: sharedclients.NewResilientClient(config),
	}
}

// InitiateLien creates a fund reservation on an account with resilience patterns.
// This operation is idempotent (same request produces same result), so retries
// are enabled.
func (c *ResilientCurrentAccountClient) InitiateLien(
	ctx context.Context,
	req *currentaccountv1.InitiateLienRequest,
) (*currentaccountv1.InitiateLienResponse, error) {
	return sharedclients.ExecuteWithResilience(
		ctx,
		c.resilientClient,
		"InitiateLien",
		func() (*currentaccountv1.InitiateLienResponse, error) {
			return c.client.InitiateLien(ctx, req)
		},
	)
}

// TerminateLien releases a reservation without executing it with resilience patterns.
// This operation is idempotent (terminating an already terminated lien is a no-op),
// so retries are enabled.
func (c *ResilientCurrentAccountClient) TerminateLien(
	ctx context.Context,
	req *currentaccountv1.TerminateLienRequest,
) (*currentaccountv1.TerminateLienResponse, error) {
	return sharedclients.ExecuteWithResilience(
		ctx,
		c.resilientClient,
		"TerminateLien",
		func() (*currentaccountv1.TerminateLienResponse, error) {
			return c.client.TerminateLien(ctx, req)
		},
	)
}

// ExecuteLien converts a reservation to an actual debit with resilience patterns.
// This operation is idempotent (executing an already executed lien returns the
// existing result), so retries are enabled.
func (c *ResilientCurrentAccountClient) ExecuteLien(
	ctx context.Context,
	req *currentaccountv1.ExecuteLienRequest,
) (*currentaccountv1.ExecuteLienResponse, error) {
	return sharedclients.ExecuteWithResilience(
		ctx,
		c.resilientClient,
		"ExecuteLien",
		func() (*currentaccountv1.ExecuteLienResponse, error) {
			return c.client.ExecuteLien(ctx, req)
		},
	)
}

// Close closes the underlying gRPC connection.
func (c *ResilientCurrentAccountClient) Close() error {
	if err := c.client.Close(); err != nil {
		return fmt.Errorf("failed to close current-account client connection: %w", err)
	}
	return nil
}

// newResilientCurrentAccountClientForTesting creates a resilient client with a mock
// gRPC client for testing purposes. This function is not exported to prevent misuse.
func newResilientCurrentAccountClientForTesting(
	mockClient currentAccountGRPCClient,
	config sharedclients.ResilientClientConfig,
) *ResilientCurrentAccountClient {
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = "current-account"
	}

	return &ResilientCurrentAccountClient{
		client:          mockClient,
		resilientClient: sharedclients.NewResilientClient(config),
	}
}
