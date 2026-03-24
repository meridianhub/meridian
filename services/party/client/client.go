// Package client provides a gRPC client for the Party service.
//
// The Party service manages party reference data (customers, counterparties,
// legal entities). This client enables inter-service communication with
// proper context propagation, tracing, and resilience patterns.
//
// Usage with Kubernetes DNS-based load balancing (recommended for production):
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "party",
//	    Namespace:   "default",
//	    Port:        50055,
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
//	    Target:  "localhost:50055",
//	    Timeout: 30 * time.Second,
//	})
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// DefaultPort is the default gRPC port for the Party service.
	DefaultPort = 50055

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for Party.
	ServiceName = "party"
)

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = errors.New("either Target or ServiceName must be provided")

// Config holds configuration for the Party client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50055" or "party:50055").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "party").
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50055 if not specified.
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

// Client provides access to the Party service.
type Client struct {
	conn      *grpc.ClientConn
	party     partyv1.PartyServiceClient
	tracer    *observability.Tracer
	resilient *clients.ResilientClient
	timeout   time.Duration
}

// New creates a new Party gRPC client.
//
// Returns the client, a cleanup function to close the connection, and any error.
// The cleanup function should be deferred immediately after checking the error.
//
// Example:
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "party",
//	    Namespace:   "default",
//	    Port:        50055,
//	})
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
func New(ctx context.Context, cfg Config) (*Client, func(), error) {
	// Apply defaults
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}

	var conn *grpc.ClientConn
	var err error

	// Use platform gRPC factory when ServiceName is provided (preferred)
	if cfg.ServiceName != "" {
		dialOpts := cfg.DialOptions

		// Add tracing interceptors if tracer is provided
		// Use WithChainUnaryInterceptor/WithChainStreamInterceptor to properly chain
		// multiple interceptors instead of overwriting them
		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithChainUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithChainStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
			)
		}

		// Use platform factory for DNS-based load balancing
		conn, err = platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
			ServiceName: cfg.ServiceName,
			Namespace:   cfg.Namespace,
			Port:        cfg.Port,
			DialOptions: dialOpts,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create party gRPC connection via platform factory: %w", err)
		}
	} else if cfg.Target != "" {
		// Fallback to legacy direct connection for backward compatibility
		dialOpts := cfg.DialOptions
		if dialOpts == nil {
			dialOpts = []grpc.DialOption{
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			}
		}

		// Add tracing interceptors if tracer is provided
		// Use WithChainUnaryInterceptor/WithChainStreamInterceptor to properly chain
		// multiple interceptors instead of overwriting them
		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithChainUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithChainStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
			)
		}

		conn, err = grpc.NewClient(cfg.Target, dialOpts...)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create party gRPC connection to %s: %w", cfg.Target, err)
		}
	} else {
		return nil, nil, ErrTargetRequired
	}

	// Create resilient client if configuration is provided
	var resilient *clients.ResilientClient
	if cfg.Resilience != nil {
		resilient = clients.NewResilientClient(*cfg.Resilience)
	}

	client := &Client{
		conn:      conn,
		party:     partyv1.NewPartyServiceClient(conn),
		tracer:    cfg.Tracer,
		resilient: resilient,
		timeout:   cfg.Timeout,
	}

	cleanup := func() {
		if client.conn != nil {
			_ = client.conn.Close()
		}
	}

	return client, cleanup, nil
}

// RegisterParty creates a new party in the reference data directory.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) RegisterParty(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.RegisterPartyResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "RegisterParty", func() (*partyv1.RegisterPartyResponse, error) {
			return c.party.RegisterParty(ctx, req)
		})
	}

	resp, err := c.party.RegisterParty(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to register party: %w", err)
	}

	return resp, nil
}

// RetrieveParty gets party details by ID.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) RetrieveParty(ctx context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveParty", func() (*partyv1.RetrievePartyResponse, error) {
			return c.party.RetrieveParty(ctx, req)
		})
	}

	resp, err := c.party.RetrieveParty(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve party: %w", err)
	}

	return resp, nil
}

// GetDefaultPaymentMethod retrieves the default payment method for a party.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) GetDefaultPaymentMethod(ctx context.Context, req *partyv1.GetDefaultPaymentMethodRequest) (*partyv1.GetDefaultPaymentMethodResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "GetDefaultPaymentMethod", func() (*partyv1.GetDefaultPaymentMethodResponse, error) {
			return c.party.GetDefaultPaymentMethod(ctx, req)
		})
	}

	resp, err := c.party.GetDefaultPaymentMethod(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get default payment method: %w", err)
	}

	return resp, nil
}

// ListParticipants returns all active participants for a syndicate (org party).
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) ListParticipants(ctx context.Context, req *partyv1.ListParticipantsRequest) (*partyv1.ListParticipantsResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "ListParticipants", func() (*partyv1.ListParticipantsResponse, error) {
			return c.party.ListParticipants(ctx, req)
		})
	}

	resp, err := c.party.ListParticipants(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list participants: %w", err)
	}

	return resp, nil
}

// GetStructuringData returns the structuring metadata for a specific participant in a syndicate.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) GetStructuringData(ctx context.Context, req *partyv1.GetStructuringDataRequest) (*partyv1.GetStructuringDataResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "GetStructuringData", func() (*partyv1.GetStructuringDataResponse, error) {
			return c.party.GetStructuringData(ctx, req)
		})
	}

	resp, err := c.party.GetStructuringData(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get structuring data: %w", err)
	}

	return resp, nil
}

// Close terminates the gRPC connection gracefully.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close party client connection: %w", err)
		}
	}
	return nil
}

// Conn returns the underlying gRPC connection for creating additional clients
// (e.g., health check clients that bypass the business client's circuit breaker).
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}
