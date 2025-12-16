// Package clients provides gRPC client wrappers for external service communication.
//
// TODO(223-shared-client-patterns): The PartyClient implementation shares significant code with
// services/current-account/clients/party_client.go. Extract shared connection setup logic
// to shared/pkg/clients. See GitHub issue #223 for the full extraction plan.
package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Party client errors.
var (
	// ErrPartyTargetRequired is returned when target address is not provided.
	ErrPartyTargetRequired = errors.New("target address is required")
	// ErrPartyRegistrationFailed is returned when party registration fails.
	ErrPartyRegistrationFailed = errors.New("party registration failed")
	// ErrPartyServiceUnavailable is returned when the party service is temporarily unavailable.
	ErrPartyServiceUnavailable = errors.New("party service unavailable")
	// ErrPartyServiceTimeout is returned when the party service request times out.
	ErrPartyServiceTimeout = errors.New("party service timeout")
)

// PartyClient defines the interface for communicating with the Party service.
//
// The Tenant service uses this client to register a corresponding Party
// when a new tenant is created, establishing the link between platform
// infrastructure (Tenant) and BIAN domain entities (Party.Organization).
type PartyClient interface {
	// RegisterParty creates a new party in the Party Reference Data Directory.
	// Returns the registered party with its assigned party_id.
	RegisterParty(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.Party, error)

	// Close terminates the client connection gracefully.
	Close() error
}

// PartyGRPCClient implements PartyClient using gRPC.
type PartyGRPCClient struct {
	conn    *grpc.ClientConn
	client  partyv1.PartyServiceClient
	tracer  *observability.Tracer
	timeout time.Duration
}

// PartyClientConfig holds configuration for the Party client.
type PartyClientConfig struct {
	// Target is the gRPC server address (e.g., "localhost:50055" or "party-service:50055").
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "party-service").
	// When specified, enables DNS-based client-side load balancing.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Party service uses port 50055.
	Port int

	// Timeout is the default timeout for RPC calls.
	// Defaults to 30 seconds if not specified.
	Timeout time.Duration

	// Tracer is an optional observability tracer for distributed tracing.
	Tracer *observability.Tracer

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// NewPartyClient creates a new Party gRPC client.
//
// Supports two connection modes:
//
// 1. DNS-based load balancing (recommended for Kubernetes):
//
//	config := &PartyClientConfig{
//	    ServiceName: "party-service",
//	    Namespace:   "default",
//	    Port:        50055,
//	    Timeout:     30 * time.Second,
//	}
//
// 2. Legacy direct connection (for backward compatibility):
//
//	config := &PartyClientConfig{
//	    Target:  "party-service:50055",
//	    Timeout: 30 * time.Second,
//	}
func NewPartyClient(cfg *PartyClientConfig) (*PartyGRPCClient, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	var conn *grpc.ClientConn
	var err error

	if cfg.ServiceName != "" {
		dialOpts := cfg.DialOptions

		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
			)
		}

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
		if cfg.Target == "" {
			return nil, ErrPartyTargetRequired
		}

		dialOpts := cfg.DialOptions
		if dialOpts == nil {
			dialOpts = []grpc.DialOption{
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			}
		}

		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
			)
		}

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

// RegisterParty creates a new party in the Party Reference Data Directory.
func (c *PartyGRPCClient) RegisterParty(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.Party, error) {
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

	resp, err := c.client.RegisterParty(ctx, req)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() { //nolint:exhaustive // Only handling specific error codes
			case codes.AlreadyExists:
				return nil, fmt.Errorf("%w: party already exists", ErrPartyRegistrationFailed)
			case codes.InvalidArgument:
				return nil, fmt.Errorf("%w: %s", ErrPartyRegistrationFailed, st.Message())
			case codes.Unavailable:
				// Transient error - service temporarily unavailable, may be retried
				return nil, fmt.Errorf("%w: %w", ErrPartyServiceUnavailable, err)
			case codes.DeadlineExceeded:
				// Transient error - request timed out, may be retried
				return nil, fmt.Errorf("%w: %w", ErrPartyServiceTimeout, err)
			}
		}
		return nil, fmt.Errorf("%w: %w", ErrPartyRegistrationFailed, err)
	}

	return resp.Party, nil
}

// Close terminates the gRPC connection.
func (c *PartyGRPCClient) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close party client connection: %w", err)
		}
	}
	return nil
}
