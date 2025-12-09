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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Party validation errors
var (
	// ErrPartyNotFound is returned when the requested party does not exist
	ErrPartyNotFound = errors.New("party not found")
	// ErrPartyNotActive is returned when the party exists but is not in ACTIVE status
	ErrPartyNotActive = errors.New("party not active")
	// ErrPartyTargetRequired is returned when target address is not provided
	ErrPartyTargetRequired = errors.New("target address is required")
)

// PartyClient defines the interface for communicating with the Party service
//
// The Party service manages party reference data (customers, counterparties,
// legal entities). CurrentAccount uses this service to validate party ownership
// before account operations.
type PartyClient interface {
	// ValidateParty checks if a party exists and is active
	//
	// Returns nil if the party exists and has ACTIVE status.
	// Returns ErrPartyNotFound if the party does not exist.
	// Returns ErrPartyNotActive if the party exists but is not ACTIVE.
	ValidateParty(ctx context.Context, partyID string) error

	// GetParty retrieves full party details by ID
	//
	// Returns the party data if found, or an error if not found.
	GetParty(ctx context.Context, partyID string) (*partyv1.Party, error)

	// Close terminates the client connection gracefully
	Close() error
}

// PartyGRPCClient implements PartyClient using gRPC
type PartyGRPCClient struct {
	conn    *grpc.ClientConn
	client  partyv1.PartyServiceClient
	tracer  *observability.Tracer
	timeout time.Duration
}

// PartyClientConfig holds configuration for the Party client
type PartyClientConfig struct {
	// Target is the gRPC server address (e.g., "localhost:50054" or "party-service:50054")
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	// This field is maintained for backward compatibility with tests and local development.
	// In production, prefer ServiceName-based configuration for automatic load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "party-service")
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	// The client will connect to dns:///party-service.<namespace>.svc.cluster.local:<port>
	// and use round_robin load balancing across all pod IPs.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production")
	// Defaults to "default" if not specified or empty.
	// Only used when ServiceName is specified.
	Namespace string

	// Port is the service port number
	// Party service uses port 50054 (configured in deployments/k8s/party/service.yaml)
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

// NewPartyClient creates a new Party gRPC client
//
// Supports two connection modes:
//
// 1. DNS-based load balancing (recommended for Kubernetes):
//
//	config := &clients.PartyClientConfig{
//	    ServiceName: "party-service",
//	    Namespace:   "default",
//	    Port:        50054,
//	    Timeout:     30 * time.Second,
//	    Tracer:      tracer,
//	}
//
// 2. Legacy direct connection (for backward compatibility):
//
//	config := &clients.PartyClientConfig{
//	    Target:  "party-service:50054",
//	    Timeout: 30 * time.Second,
//	    Tracer:  tracer,
//	}
func NewPartyClient(cfg *PartyClientConfig) (*PartyGRPCClient, error) {
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
			return nil, ErrPartyTargetRequired
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

	return &PartyGRPCClient{
		conn:    conn,
		client:  partyv1.NewPartyServiceClient(conn),
		tracer:  cfg.Tracer,
		timeout: cfg.Timeout,
	}, nil
}

// ValidateParty checks if a party exists and is active
func (c *PartyGRPCClient) ValidateParty(ctx context.Context, partyID string) error {
	party, err := c.GetParty(ctx, partyID)
	if err != nil {
		return err
	}

	if party.Status != partyv1.PartyStatus_PARTY_STATUS_ACTIVE {
		return ErrPartyNotActive
	}

	return nil
}

// GetParty retrieves full party details by ID
func (c *PartyGRPCClient) GetParty(ctx context.Context, partyID string) (*partyv1.Party, error) {
	ctx, cancel := WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = PropagateCorrelationID(ctx)

	resp, err := c.client.RetrieveParty(ctx, &partyv1.RetrievePartyRequest{
		PartyId: partyID,
	})
	if err != nil {
		// Check for NOT_FOUND status
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, ErrPartyNotFound
		}
		return nil, fmt.Errorf("failed to retrieve party: %w", err)
	}

	return resp.Party, nil
}

// Close terminates the gRPC connection
func (c *PartyGRPCClient) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close party client connection: %w", err)
		}
	}
	return nil
}
