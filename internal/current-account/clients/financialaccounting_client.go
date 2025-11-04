package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/internal/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// ErrTargetAddressRequired is returned when target address is not provided
var ErrTargetAddressRequired = errors.New("target address is required")

// FinancialAccountingGRPCClient implements FinancialAccountingClient using gRPC
type FinancialAccountingGRPCClient struct {
	conn   *grpc.ClientConn
	client financialaccountingv1.FinancialAccountingServiceClient
	tracer *observability.Tracer
}

// FinancialAccountingClientConfig holds configuration for the FinancialAccounting client
type FinancialAccountingClientConfig struct {
	// Target is the gRPC server address (e.g., "localhost:50052" or "financialaccounting-service:443")
	Target string

	// Timeout is the default timeout for RPC calls
	// If not specified, defaults to 30 seconds
	Timeout time.Duration

	// Tracer is an optional observability tracer for distributed tracing
	// If provided, the client will automatically propagate trace context
	Tracer *observability.Tracer

	// DialOptions allows custom gRPC dial options
	// If not specified, uses insecure credentials (suitable for internal service mesh)
	DialOptions []grpc.DialOption
}

// NewFinancialAccountingClient creates a new FinancialAccounting gRPC client
//
// Example usage:
//
//	config := &clients.FinancialAccountingClientConfig{
//	    Target:  "financialaccounting-service:50052",
//	    Timeout: 30 * time.Second,
//	    Tracer:  tracer,
//	}
//	client, err := clients.NewFinancialAccountingClient(config)
//	if err != nil {
//	    return fmt.Errorf("failed to create financial accounting client: %w", err)
//	}
//	defer client.Close()
func NewFinancialAccountingClient(cfg *FinancialAccountingClientConfig) (*FinancialAccountingGRPCClient, error) {
	if cfg.Target == "" {
		return nil, ErrTargetAddressRequired
	}

	// Set default timeout if not specified
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	// Prepare dial options
	dialOpts := cfg.DialOptions
	if dialOpts == nil {
		// Default: insecure credentials for service mesh communication
		// In production, this would typically be secured by the service mesh (e.g., Istio, Linkerd)
		dialOpts = []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}
	}

	// Add tracing interceptor if tracer is provided
	if cfg.Tracer != nil {
		dialOpts = append(dialOpts,
			grpc.WithUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
			grpc.WithStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
		)
	}

	// Establish connection
	conn, err := grpc.NewClient(cfg.Target, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to %s: %w", cfg.Target, err)
	}

	return &FinancialAccountingGRPCClient{
		conn:   conn,
		client: financialaccountingv1.NewFinancialAccountingServiceClient(conn),
		tracer: cfg.Tracer,
	}, nil
}

// InitiateFinancialBookingLog creates a new financial booking log
func (c *FinancialAccountingGRPCClient) InitiateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.InitiateFinancialBookingLogRequest,
) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	// Propagate correlation ID if present in context
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.InitiateFinancialBookingLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate financial booking log: %w", err)
	}

	return resp, nil
}

// UpdateFinancialBookingLog updates an existing financial booking log
func (c *FinancialAccountingGRPCClient) UpdateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.UpdateFinancialBookingLogRequest,
) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.UpdateFinancialBookingLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to update financial booking log: %w", err)
	}

	return resp, nil
}

// RetrieveFinancialBookingLog retrieves a specific financial booking log
func (c *FinancialAccountingGRPCClient) RetrieveFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.RetrieveFinancialBookingLogRequest,
) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.RetrieveFinancialBookingLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve financial booking log: %w", err)
	}

	return resp, nil
}

// ListFinancialBookingLogs lists financial booking logs with filtering
func (c *FinancialAccountingGRPCClient) ListFinancialBookingLogs(
	ctx context.Context,
	req *financialaccountingv1.ListFinancialBookingLogsRequest,
) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.ListFinancialBookingLogs(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list financial booking logs: %w", err)
	}

	return resp, nil
}

// CaptureLedgerPosting creates a new ledger posting
func (c *FinancialAccountingGRPCClient) CaptureLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.CaptureLedgerPostingRequest,
) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.CaptureLedgerPosting(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to capture ledger posting: %w", err)
	}

	return resp, nil
}

// RetrieveLedgerPosting retrieves a specific ledger posting
func (c *FinancialAccountingGRPCClient) RetrieveLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.RetrieveLedgerPostingRequest,
) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.RetrieveLedgerPosting(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve ledger posting: %w", err)
	}

	return resp, nil
}

// Close terminates the gRPC connection
func (c *FinancialAccountingGRPCClient) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close financial accounting client connection: %w", err)
		}
	}
	return nil
}

// propagateCorrelationID extracts correlation ID from context and adds it to gRPC metadata
//
// Correlation IDs are used for request tracing across service boundaries.
// This method checks for common correlation ID keys and propagates them
// via gRPC metadata headers.
func (c *FinancialAccountingGRPCClient) propagateCorrelationID(ctx context.Context) context.Context {
	// Extract correlation ID from context
	// Common keys: "correlation-id", "x-correlation-id", "x-request-id"
	correlationID := extractCorrelationIDFromContext(ctx)
	if correlationID == "" {
		return ctx
	}

	// Get existing metadata or create new
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		// Clone to avoid modifying shared metadata
		md = md.Copy()
	}

	// Add correlation ID to metadata
	md.Set("x-correlation-id", correlationID)

	return metadata.NewOutgoingContext(ctx, md)
}

// extractCorrelationIDFromContext attempts to extract correlation ID from context
//
// Checks multiple common keys used for correlation IDs across different systems.
func extractCorrelationIDFromContext(ctx context.Context) string {
	// Try common correlation ID keys
	keys := []string{
		"correlation-id",
		"x-correlation-id",
		"x-request-id",
		"request-id",
	}

	for _, key := range keys {
		if val := ctx.Value(key); val != nil {
			if id, ok := val.(string); ok {
				return id
			}
		}
	}

	// Check incoming metadata as fallback
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, key := range keys {
			if vals := md.Get(key); len(vals) > 0 {
				return vals[0]
			}
		}
	}

	return ""
}
