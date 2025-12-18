// Package clients provides gRPC client wrappers with resilience patterns.
//
// This package provides resilient client wrappers that delegate to the shared
// implementation in github.com/meridianhub/meridian/shared/pkg/clients.
package clients

import (
	"context"
	"fmt"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"google.golang.org/grpc"
)

// FinancialAccountingClient defines the interface for financial-accounting service operations.
// This interface allows for easy mocking in tests.
type FinancialAccountingClient interface {
	InitiateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error)
	CaptureLedgerPosting(ctx context.Context, req *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error)
	UpdateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error)
	Close() error
}

// ResilientFinancialAccountingClient wraps the gRPC client with resilience patterns.
// It provides circuit breaker protection and retry capabilities for all operations
// against the financial-accounting service.
type ResilientFinancialAccountingClient struct {
	client          financialAccountingGRPCClient
	resilientClient *sharedclients.ResilientClient
}

// financialAccountingGRPCClient is the internal interface for the gRPC client.
// This allows us to inject mocks for testing.
type financialAccountingGRPCClient interface {
	InitiateFinancialBookingLog(ctx context.Context, in *financialaccountingv1.InitiateFinancialBookingLogRequest, opts ...grpc.CallOption) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error)
	CaptureLedgerPosting(ctx context.Context, in *financialaccountingv1.CaptureLedgerPostingRequest, opts ...grpc.CallOption) (*financialaccountingv1.CaptureLedgerPostingResponse, error)
	UpdateFinancialBookingLog(ctx context.Context, in *financialaccountingv1.UpdateFinancialBookingLogRequest, opts ...grpc.CallOption) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error)
	Close() error
}

// grpcFAClientWrapper wraps the generated gRPC client and connection
type grpcFAClientWrapper struct {
	conn   *grpc.ClientConn
	client financialaccountingv1.FinancialAccountingServiceClient
}

func (w *grpcFAClientWrapper) InitiateFinancialBookingLog(ctx context.Context, in *financialaccountingv1.InitiateFinancialBookingLogRequest, opts ...grpc.CallOption) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	return w.client.InitiateFinancialBookingLog(ctx, in, opts...)
}

func (w *grpcFAClientWrapper) CaptureLedgerPosting(ctx context.Context, in *financialaccountingv1.CaptureLedgerPostingRequest, opts ...grpc.CallOption) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	return w.client.CaptureLedgerPosting(ctx, in, opts...)
}

func (w *grpcFAClientWrapper) UpdateFinancialBookingLog(ctx context.Context, in *financialaccountingv1.UpdateFinancialBookingLogRequest, opts ...grpc.CallOption) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	return w.client.UpdateFinancialBookingLog(ctx, in, opts...)
}

func (w *grpcFAClientWrapper) Close() error {
	return w.conn.Close()
}

// NewResilientFinancialAccountingClient creates a resilient wrapper around the
// FinancialAccounting gRPC client. The connection must be established before
// calling this function.
func NewResilientFinancialAccountingClient(
	conn *grpc.ClientConn,
	config sharedclients.ResilientClientConfig,
) *ResilientFinancialAccountingClient {
	// Apply default name if not provided
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = "financial-accounting"
	}

	wrapper := &grpcFAClientWrapper{
		conn:   conn,
		client: financialaccountingv1.NewFinancialAccountingServiceClient(conn),
	}

	return &ResilientFinancialAccountingClient{
		client:          wrapper,
		resilientClient: sharedclients.NewResilientClient(config),
	}
}

// InitiateFinancialBookingLog creates a new financial booking log with resilience patterns.
// This operation is idempotent (same idempotency_key produces same result), so retries
// are enabled.
func (c *ResilientFinancialAccountingClient) InitiateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.InitiateFinancialBookingLogRequest,
) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	return sharedclients.ExecuteWithResilience(
		ctx,
		c.resilientClient,
		"InitiateFinancialBookingLog",
		func() (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
			return c.client.InitiateFinancialBookingLog(ctx, req)
		},
	)
}

// CaptureLedgerPosting creates a new ledger posting with resilience patterns.
// This operation is idempotent (same idempotency_key produces same result), so retries
// are enabled.
func (c *ResilientFinancialAccountingClient) CaptureLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.CaptureLedgerPostingRequest,
) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	return sharedclients.ExecuteWithResilience(
		ctx,
		c.resilientClient,
		"CaptureLedgerPosting",
		func() (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
			return c.client.CaptureLedgerPosting(ctx, req)
		},
	)
}

// UpdateFinancialBookingLog updates an existing booking log with resilience patterns.
// This operation transitions booking log status (e.g., to POSTED).
// Retries are enabled as the operation is idempotent based on version checking.
func (c *ResilientFinancialAccountingClient) UpdateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.UpdateFinancialBookingLogRequest,
) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	return sharedclients.ExecuteWithResilience(
		ctx,
		c.resilientClient,
		"UpdateFinancialBookingLog",
		func() (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
			return c.client.UpdateFinancialBookingLog(ctx, req)
		},
	)
}

// Close closes the underlying gRPC connection.
func (c *ResilientFinancialAccountingClient) Close() error {
	if err := c.client.Close(); err != nil {
		return fmt.Errorf("failed to close financial-accounting client connection: %w", err)
	}
	return nil
}

// newResilientFinancialAccountingClientForTesting creates a resilient client with a mock
// gRPC client for testing purposes. This function is not exported to prevent misuse.
func newResilientFinancialAccountingClientForTesting(
	mockClient financialAccountingGRPCClient,
	config sharedclients.ResilientClientConfig,
) *ResilientFinancialAccountingClient {
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = "financial-accounting"
	}

	return &ResilientFinancialAccountingClient{
		client:          mockClient,
		resilientClient: sharedclients.NewResilientClient(config),
	}
}
