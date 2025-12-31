package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
)

// ErrFinancialAccountingServiceNameRequired is returned when ServiceName is not provided
var ErrFinancialAccountingServiceNameRequired = errors.New("ServiceName is required for financial accounting client")

// FinancialAccountingGRPCClient implements FinancialAccountingClient using gRPC
type FinancialAccountingGRPCClient struct {
	conn    *grpc.ClientConn
	client  financialaccountingv1.FinancialAccountingServiceClient
	tracer  *observability.Tracer
	timeout time.Duration
}

// FinancialAccountingClientConfig holds configuration for the FinancialAccounting client
type FinancialAccountingClientConfig struct {
	// ServiceName is the Kubernetes service name (e.g., "financial-accounting").
	// Required. Enables DNS-based client-side load balancing via pkg/platform/grpc.
	// The client will connect to dns:///financial-accounting.<namespace>.svc.cluster.local:<port>
	// and use round_robin load balancing across all pod IPs.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production")
	// Defaults to "default" if not specified or empty.
	Namespace string

	// Port is the service port number
	// FinancialAccounting service uses port 50052 (configured in deployments/k8s/financial-accounting/service.yaml)
	Port int

	// Timeout is the default timeout for RPC calls
	// If not specified, defaults to 30 seconds
	Timeout time.Duration

	// Tracer is an optional observability tracer for distributed tracing
	// If provided, the client will automatically propagate trace context
	Tracer *observability.Tracer

	// DialOptions allows custom gRPC dial options
	DialOptions []grpc.DialOption
}

// NewFinancialAccountingClient creates a new FinancialAccounting gRPC client using DNS-based load balancing.
//
// Example:
//
//	config := &clients.FinancialAccountingClientConfig{
//	    ServiceName: "financial-accounting",
//	    Namespace:   "default",
//	    Port:        50052,
//	    Timeout:     30 * time.Second,
//	    Tracer:      tracer,
//	}
func NewFinancialAccountingClient(cfg *FinancialAccountingClientConfig) (*FinancialAccountingGRPCClient, error) {
	if cfg.ServiceName == "" {
		return nil, ErrFinancialAccountingServiceNameRequired
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = defaults.DefaultRPCTimeout
	}

	dialOpts := cfg.DialOptions

	if cfg.Tracer != nil {
		dialOpts = append(dialOpts,
			grpc.WithUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
			grpc.WithStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
		)
	}

	conn, err := platformgrpc.NewClient(context.Background(), platformgrpc.ClientConfig{
		ServiceName: cfg.ServiceName,
		Namespace:   cfg.Namespace,
		Port:        cfg.Port,
		DialOptions: dialOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
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
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

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
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

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
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

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
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

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
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

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
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

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

// Conn returns the underlying gRPC connection for creating additional clients
// (e.g., health check clients that bypass the business client's circuit breaker).
func (c *FinancialAccountingGRPCClient) Conn() *grpc.ClientConn {
	return c.conn
}
