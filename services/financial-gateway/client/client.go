// Package client provides a gRPC client for the FinancialGateway service.
//
// The FinancialGateway service manages outbound payment dispatch to external payment rails
// (e.g. Stripe). This client enables inter-service communication with proper context
// propagation, tracing, and resilience patterns.
//
// Usage with Kubernetes DNS-based load balancing (recommended for production):
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "financial-gateway",
//	    Namespace:   "default",
//	    Port:        50064,
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
//	    Target:  "localhost:50064",
//	    Timeout: 30 * time.Second,
//	})
package client

import (
	"context"
	"fmt"
	"time"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
)

const (
	// DefaultPort is the default gRPC port for the FinancialGateway service.
	DefaultPort = 50064

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for FinancialGateway.
	ServiceName = "financial-gateway"
)

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = clients.ErrConnTargetRequired

// Config holds configuration for the FinancialGateway client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50064" or "financial-gateway:50064").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "financial-gateway").
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50064 if not specified.
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

// Client provides access to the FinancialGateway service.
type Client struct {
	conn             *grpc.ClientConn
	financialGateway financialgatewayv1.FinancialGatewayServiceClient
	tracer           *observability.Tracer
	resilient        *clients.ResilientClient
	timeout          time.Duration
}

// New creates a new FinancialGateway gRPC client.
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
		conn:             conn,
		financialGateway: financialgatewayv1.NewFinancialGatewayServiceClient(conn),
		tracer:           cfg.Tracer,
		resilient:        resilient,
		timeout:          cfg.Timeout,
	}, cleanup, nil
}

// DispatchPayment submits a payment for outbound dispatch via a payment rail.
// This is a non-idempotent operation guarded by idempotency_key, so retry is disabled.
func (c *Client) DispatchPayment(ctx context.Context, req *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "DispatchPayment", func() (*financialgatewayv1.DispatchPaymentResponse, error) {
			return c.financialGateway.DispatchPayment(ctx, req)
		})
	}

	resp, err := c.financialGateway.DispatchPayment(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to dispatch payment: %w", err)
	}

	return resp, nil
}

// DispatchRefund submits a refund for outbound dispatch via the original payment rail.
// This is a non-idempotent operation guarded by idempotency_key, so retry is disabled.
func (c *Client) DispatchRefund(ctx context.Context, req *financialgatewayv1.DispatchRefundRequest) (*financialgatewayv1.DispatchRefundResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "DispatchRefund", func() (*financialgatewayv1.DispatchRefundResponse, error) {
			return c.financialGateway.DispatchRefund(ctx, req)
		})
	}

	resp, err := c.financialGateway.DispatchRefund(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to dispatch refund: %w", err)
	}

	return resp, nil
}

// CancelPayment cancels a pending payment dispatch before it is delivered to the payment rail.
// Only payments in PENDING status can be cancelled.
func (c *Client) CancelPayment(ctx context.Context, req *financialgatewayv1.CancelPaymentRequest) (*financialgatewayv1.CancelPaymentResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "CancelPayment", func() (*financialgatewayv1.CancelPaymentResponse, error) {
			return c.financialGateway.CancelPayment(ctx, req)
		})
	}

	resp, err := c.financialGateway.CancelPayment(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to cancel payment: %w", err)
	}

	return resp, nil
}

// GetProviderHealth returns the current health status of a payment rail provider.
// This is an idempotent read operation, so retry is enabled.
func (c *Client) GetProviderHealth(ctx context.Context, req *financialgatewayv1.GetProviderHealthRequest) (*financialgatewayv1.GetProviderHealthResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "GetProviderHealth", func() (*financialgatewayv1.GetProviderHealthResponse, error) {
			return c.financialGateway.GetProviderHealth(ctx, req)
		})
	}

	resp, err := c.financialGateway.GetProviderHealth(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider health: %w", err)
	}

	return resp, nil
}

// Close terminates the gRPC connection gracefully.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close financial-gateway client connection: %w", err)
		}
	}
	return nil
}

// Conn returns the underlying gRPC connection for creating additional clients
// (e.g., health check clients that bypass the business client's circuit breaker).
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}
