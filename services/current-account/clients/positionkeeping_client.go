package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ErrPositionKeepingTargetRequired is returned when target address is not provided
var ErrPositionKeepingTargetRequired = errors.New("target address is required")

// PositionKeepingGRPCClient implements PositionKeepingClient using gRPC
type PositionKeepingGRPCClient struct {
	conn    *grpc.ClientConn
	client  positionkeepingv1.PositionKeepingServiceClient
	tracer  *observability.Tracer
	timeout time.Duration
}

// PositionKeepingClientConfig holds configuration for the PositionKeeping client
type PositionKeepingClientConfig struct {
	// Target is the gRPC server address (e.g., "localhost:50051" or "position-keeping:50051")
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	// This field is maintained for backward compatibility with tests and local development.
	// In production, prefer ServiceName-based configuration for automatic load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "position-keeping")
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	// The client will connect to dns:///position-keeping.<namespace>.svc.cluster.local:<port>
	// and use round_robin load balancing across all pod IPs.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production")
	// Defaults to "default" if not specified or empty.
	// Only used when ServiceName is specified.
	Namespace string

	// Port is the service port number
	// PositionKeeping service uses port 50053 (configured in deployments/k8s/position-keeping/service.yaml)
	// Only used when ServiceName is specified.
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

// NewPositionKeepingClient creates a new PositionKeeping gRPC client
//
// Supports two connection modes:
//
// 1. DNS-based load balancing (recommended for Kubernetes):
//
//	config := &clients.PositionKeepingClientConfig{
//	    ServiceName: "position-keeping",
//	    Namespace:   "default",
//	    Port:        50051,
//	    Timeout:     30 * time.Second,
//	    Tracer:      tracer,
//	}
//
// 2. Legacy direct connection (for backward compatibility):
//
//	config := &clients.PositionKeepingClientConfig{
//	    Target:  "positionkeeping-service:50051",
//	    Timeout: 30 * time.Second,
//	    Tracer:  tracer,
//	}
func NewPositionKeepingClient(cfg *PositionKeepingClientConfig) (*PositionKeepingGRPCClient, error) {
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
			return nil, ErrPositionKeepingTargetRequired
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

	return &PositionKeepingGRPCClient{
		conn:    conn,
		client:  positionkeepingv1.NewPositionKeepingServiceClient(conn),
		tracer:  cfg.Tracer,
		timeout: cfg.Timeout,
	}, nil
}

// InitiateFinancialPositionLog creates a new financial position log
func (c *PositionKeepingGRPCClient) InitiateFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.InitiateFinancialPositionLogRequest,
) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

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
