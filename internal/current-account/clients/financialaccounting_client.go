package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/internal/platform/observability"
	platformgrpc "github.com/meridianhub/meridian/pkg/platform/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ErrFinancialAccountingTargetRequired is returned when target address is not provided
var ErrFinancialAccountingTargetRequired = errors.New("target address is required")

// FinancialAccountingGRPCClient implements FinancialAccountingClient using gRPC
type FinancialAccountingGRPCClient struct {
	conn    *grpc.ClientConn
	client  financialaccountingv1.FinancialAccountingServiceClient
	tracer  *observability.Tracer
	timeout time.Duration
}

// FinancialAccountingClientConfig holds configuration for the FinancialAccounting client
type FinancialAccountingClientConfig struct {
	// Target is the gRPC server address (e.g., "localhost:50052" or "financial-accounting:50052")
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing
	Target string

	// ServiceName is the Kubernetes service name (e.g., "financial-accounting")
	// When specified, enables DNS-based client-side load balancing
	ServiceName string

	// Namespace is the Kubernetes namespace (defaults to "default")
	// Only used when ServiceName is specified
	Namespace string

	// Port is the service port number
	// Only used when ServiceName is specified
	Port int

	// Timeout is the default timeout for RPC calls
	// If not specified, defaults to 30 seconds
	Timeout time.Duration

	// Tracer is an optional observability tracer for distributed tracing
	// If provided, the client will automatically propagate trace context
	Tracer *observability.Tracer

	// DialOptions allows custom gRPC dial options
	// When using ServiceName, these options are passed to the platform gRPC factory
	DialOptions []grpc.DialOption
}

// NewFinancialAccountingClient creates a new FinancialAccounting gRPC client
//
// Supports two connection modes:
//
// 1. DNS-based load balancing (recommended for Kubernetes):
//
//	config := &clients.FinancialAccountingClientConfig{
//	    ServiceName: "financial-accounting",
//	    Namespace:   "default",
//	    Port:        50052,
//	    Timeout:     30 * time.Second,
//	    Tracer:      tracer,
//	}
//
// 2. Legacy direct connection (for backward compatibility):
//
//	config := &clients.FinancialAccountingClientConfig{
//	    Target:  "financialaccounting-service:50052",
//	    Timeout: 30 * time.Second,
//	    Tracer:  tracer,
//	}
func NewFinancialAccountingClient(cfg *FinancialAccountingClientConfig) (*FinancialAccountingGRPCClient, error) {
	// Set default timeout if not specified
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	var conn *grpc.ClientConn
	var err error

	// Use platform gRPC factory when ServiceName is provided (preferred)
	if cfg.ServiceName != "" {
		// Prepare dial options for platform factory
		dialOpts := cfg.DialOptions

		// Add tracing interceptor if tracer is provided
		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
			)
		}

		// Use platform factory for DNS-based load balancing
		conn, err = platformgrpc.NewClient(context.Background(), platformgrpc.ClientConfig{
			ServiceName: cfg.ServiceName,
			Namespace:   cfg.Namespace,
			Port:        cfg.Port,
			DialOptions: dialOpts,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC connection via platform factory: %w", err)
		}
	} else {
		// Fallback to legacy direct connection for backward compatibility
		if cfg.Target == "" {
			return nil, ErrFinancialAccountingTargetRequired
		}

		// Prepare dial options
		dialOpts := cfg.DialOptions
		if dialOpts == nil {
			// Default: insecure credentials for service mesh communication
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

		// Establish connection using legacy method
		conn, err = grpc.NewClient(cfg.Target, dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC connection to %s: %w", cfg.Target, err)
		}
	}

	return &FinancialAccountingGRPCClient{
		conn:    conn,
		client:  financialaccountingv1.NewFinancialAccountingServiceClient(conn),
		tracer:  cfg.Tracer,
		timeout: cfg.Timeout,
	}, nil
}

// InitiateFinancialBookingLog creates a new financial booking log
func (c *FinancialAccountingGRPCClient) InitiateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.InitiateFinancialBookingLogRequest,
) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
