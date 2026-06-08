// Package client provides a gRPC client for the OperationalGateway service.
//
// The OperationalGateway service manages the lifecycle of outbound instructions
// to external providers. This client enables inter-service communication with
// proper context propagation and resilience patterns.
//
// Usage with Kubernetes DNS-based load balancing (recommended for production):
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "operational-gateway",
//	    Namespace:   "default",
//	    Port:        50051,
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

	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"google.golang.org/grpc"
)

const (
	// DefaultPort is the default gRPC port for the OperationalGateway service.
	DefaultPort = ports.OperationalGateway

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for OperationalGateway.
	ServiceName = "operational-gateway"
)

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = clients.ErrConnTargetRequired

// Config holds configuration for the OperationalGateway client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50051").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "operational-gateway").
	// When specified, enables DNS-based client-side load balancing.
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
	Tracer *observability.Tracer

	// Resilience is an optional configuration for circuit breaker and retry.
	Resilience *clients.ResilientClientConfig

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// Client provides access to the OperationalGateway service.
type Client struct {
	conn               *grpc.ClientConn
	operationalGateway opgatewayv1.OperationalGatewayServiceClient
	tracer             *observability.Tracer
	resilient          *clients.ResilientClient
	timeout            time.Duration
}

// New creates a new OperationalGateway gRPC client.
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
		conn:               conn,
		operationalGateway: opgatewayv1.NewOperationalGatewayServiceClient(conn),
		tracer:             cfg.Tracer,
		resilient:          resilient,
		timeout:            cfg.Timeout,
	}, cleanup, nil
}

// DispatchInstruction submits a new instruction for outbound dispatch.
// This is a non-idempotent operation (idempotency enforced via idempotency_key).
func (c *Client) DispatchInstruction(ctx context.Context, req *opgatewayv1.DispatchInstructionRequest) (*opgatewayv1.DispatchInstructionResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "DispatchInstruction", func() (*opgatewayv1.DispatchInstructionResponse, error) {
			return c.operationalGateway.DispatchInstruction(ctx, req)
		})
	}

	resp, err := c.operationalGateway.DispatchInstruction(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to dispatch instruction: %w", err)
	}

	return resp, nil
}

// CancelInstruction cancels a pending instruction before dispatch.
// Idempotent when called on an already-cancelled instruction.
func (c *Client) CancelInstruction(ctx context.Context, req *opgatewayv1.CancelInstructionRequest) (*opgatewayv1.CancelInstructionResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "CancelInstruction", func() (*opgatewayv1.CancelInstructionResponse, error) {
			return c.operationalGateway.CancelInstruction(ctx, req)
		})
	}

	resp, err := c.operationalGateway.CancelInstruction(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to cancel instruction: %w", err)
	}

	return resp, nil
}

// GetInstruction retrieves a specific instruction by its ID.
// This is an idempotent read operation.
func (c *Client) GetInstruction(ctx context.Context, req *opgatewayv1.GetInstructionRequest) (*opgatewayv1.GetInstructionResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "GetInstruction", func() (*opgatewayv1.GetInstructionResponse, error) {
			return c.operationalGateway.GetInstruction(ctx, req)
		})
	}

	resp, err := c.operationalGateway.GetInstruction(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get instruction: %w", err)
	}

	return resp, nil
}

// Close terminates the gRPC connection gracefully.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close operational-gateway client connection: %w", err)
		}
	}
	return nil
}

// Conn returns the underlying gRPC connection.
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}
