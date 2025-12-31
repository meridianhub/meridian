// Package clients provides shared gRPC client infrastructure for cross-service communication.
//
// BasePartyClient extracts common connection setup, configuration validation,
// and context propagation logic used by services that need to communicate with the Party service.
package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
)

// Common errors for Party client operations.
var (
	// ErrPartyServiceNameRequired is returned when ServiceName is not provided in the configuration.
	ErrPartyServiceNameRequired = errors.New("ServiceName is required for party client")
)

// DefaultPartyClientTimeout is the default timeout for Party service RPC calls.
const DefaultPartyClientTimeout = 30 * time.Second

// PartyClientConfig holds configuration for creating a BasePartyClient.
type PartyClientConfig struct {
	// ServiceName is the Kubernetes service name (e.g., "party").
	// Required. Enables DNS-based client-side load balancing via pkg/platform/grpc.
	// The client will connect to dns:///party.<namespace>.svc.cluster.local:<port>
	// and use round_robin load balancing across all pod IPs.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified or empty.
	Namespace string

	// Port is the service port number.
	// Party service uses ports.Party (see shared/platform/ports/ports.go).
	Port int

	// Timeout is the default timeout for RPC calls.
	// Defaults to 30 seconds if not specified.
	Timeout time.Duration

	// Tracer is an optional observability tracer for distributed tracing.
	// If provided, the client will automatically propagate trace context.
	Tracer *observability.Tracer

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// BasePartyClient provides common gRPC client infrastructure for the Party service.
//
// This struct encapsulates connection management, context preparation with timeout
// and metadata propagation, and provides access to the underlying gRPC client.
// Service-specific clients can embed this struct and add their domain-specific methods.
type BasePartyClient struct {
	conn    *grpc.ClientConn
	client  partyv1.PartyServiceClient
	tracer  *observability.Tracer
	timeout time.Duration
}

// NewBasePartyClient creates a new BasePartyClient with the provided configuration.
//
// The client uses DNS-based load balancing for Kubernetes headless services
// and configures tracing interceptors if a tracer is provided.
//
// Example:
//
//	config := &clients.PartyClientConfig{
//	    ServiceName: "party",
//	    Namespace:   "default",
//	    Port:        ports.Party, // see shared/platform/ports
//	    Timeout:     30 * time.Second,
//	    Tracer:      tracer,
//	}
//	baseClient, err := clients.NewBasePartyClient(config)
//	if err != nil {
//	    return fmt.Errorf("failed to create party client: %w", err)
//	}
//	defer baseClient.Close()
func NewBasePartyClient(cfg *PartyClientConfig) (*BasePartyClient, error) {
	if cfg.ServiceName == "" {
		return nil, ErrPartyServiceNameRequired
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultPartyClientTimeout
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

	return &BasePartyClient{
		conn:    conn,
		client:  partyv1.NewPartyServiceClient(conn),
		tracer:  cfg.Tracer,
		timeout: timeout,
	}, nil
}

// Client returns the underlying PartyServiceClient for making RPC calls.
func (c *BasePartyClient) Client() partyv1.PartyServiceClient {
	return c.client
}

// Timeout returns the configured timeout duration for RPC calls.
func (c *BasePartyClient) Timeout() time.Duration {
	return c.timeout
}

// Close terminates the gRPC connection gracefully.
//
// This method should be called when the client is no longer needed
// to release underlying network resources.
func (c *BasePartyClient) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close party client connection: %w", err)
		}
	}
	return nil
}

// PrepareContext prepares a context for making Party service RPC calls.
//
// This method applies:
//   - Timeout: Adds a timeout if the context doesn't already have a deadline
//   - Correlation ID: Propagates correlation ID from incoming context to outgoing metadata
//   - Organization ID: Propagates tenant/organization ID for multi-tenant operations
//
// The returned cancel function should be deferred to clean up resources.
//
// Example:
//
//	ctx, cancel := baseClient.PrepareContext(ctx)
//	defer cancel()
//	resp, err := baseClient.Client().SomeMethod(ctx, req)
func (c *BasePartyClient) PrepareContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := WithTimeout(ctx, c.timeout)
	ctx = PropagateCorrelationID(ctx)
	ctx = PropagateOrganization(ctx)
	return ctx, cancel
}
