package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/internal/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// ErrPositionKeepingTargetRequired is returned when target address is not provided
var ErrPositionKeepingTargetRequired = errors.New("target address is required")

// PositionKeepingGRPCClient implements PositionKeepingClient using gRPC
type PositionKeepingGRPCClient struct {
	conn   *grpc.ClientConn
	client positionkeepingv1.PositionKeepingServiceClient
	tracer *observability.Tracer
}

// PositionKeepingClientConfig holds configuration for the PositionKeeping client
type PositionKeepingClientConfig struct {
	// Target is the gRPC server address (e.g., "localhost:50051" or "positionkeeping-service:443")
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

// NewPositionKeepingClient creates a new PositionKeeping gRPC client
//
// Example usage:
//
//	config := &clients.PositionKeepingClientConfig{
//	    Target:  "positionkeeping-service:50051",
//	    Timeout: 30 * time.Second,
//	    Tracer:  tracer,
//	}
//	client, err := clients.NewPositionKeepingClient(config)
//	if err != nil {
//	    return fmt.Errorf("failed to create position keeping client: %w", err)
//	}
//	defer client.Close()
func NewPositionKeepingClient(cfg *PositionKeepingClientConfig) (*PositionKeepingGRPCClient, error) {
	if cfg.Target == "" {
		return nil, ErrPositionKeepingTargetRequired
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

	return &PositionKeepingGRPCClient{
		conn:   conn,
		client: positionkeepingv1.NewPositionKeepingServiceClient(conn),
		tracer: cfg.Tracer,
	}, nil
}

// InitiateFinancialPositionLog creates a new financial position log
func (c *PositionKeepingGRPCClient) InitiateFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.InitiateFinancialPositionLogRequest,
) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	// Propagate correlation ID if present in context
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.InitiateFinancialPositionLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate financial position log: %w", err)
	}

	return resp, nil
}

// UpdateFinancialPositionLog updates an existing financial position log
func (c *PositionKeepingGRPCClient) UpdateFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.UpdateFinancialPositionLogRequest,
) (*positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.UpdateFinancialPositionLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to update financial position log: %w", err)
	}

	return resp, nil
}

// RetrieveFinancialPositionLog retrieves a specific financial position log
func (c *PositionKeepingGRPCClient) RetrieveFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.RetrieveFinancialPositionLogRequest,
) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.RetrieveFinancialPositionLog(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve financial position log: %w", err)
	}

	return resp, nil
}

// BulkImportTransactions imports multiple transactions in a single operation
func (c *PositionKeepingGRPCClient) BulkImportTransactions(
	ctx context.Context,
	req *positionkeepingv1.BulkImportTransactionsRequest,
) (*positionkeepingv1.BulkImportTransactionsResponse, error) {
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.BulkImportTransactions(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk import transactions: %w", err)
	}

	return resp, nil
}

// ListFinancialPositionLogs lists financial position logs with filtering
func (c *PositionKeepingGRPCClient) ListFinancialPositionLogs(
	ctx context.Context,
	req *positionkeepingv1.ListFinancialPositionLogsRequest,
) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	ctx = c.propagateCorrelationID(ctx)

	resp, err := c.client.ListFinancialPositionLogs(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list financial position logs: %w", err)
	}

	return resp, nil
}

// Close terminates the gRPC connection
func (c *PositionKeepingGRPCClient) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close position keeping client connection: %w", err)
		}
	}
	return nil
}

// propagateCorrelationID extracts correlation ID from context and adds it to gRPC metadata
//
// Correlation IDs are used for request tracing across service boundaries.
// This method checks for common correlation ID keys and propagates them
// via gRPC metadata headers.
func (c *PositionKeepingGRPCClient) propagateCorrelationID(ctx context.Context) context.Context {
	// Extract correlation ID from context
	// Common keys: "correlation-id", "x-correlation-id", "x-request-id"
	correlationID := extractCorrelationID(ctx)
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

// extractCorrelationID attempts to extract correlation ID from context
//
// Checks multiple common keys used for correlation IDs across different systems.
func extractCorrelationID(ctx context.Context) string {
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
