// Package clients provides gRPC client wrappers for external service communication.
package clients

import (
	"context"
	"errors"
	"fmt"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Party client errors.
var (
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
//
// This client embeds the shared BasePartyClient for connection management
// and adds tenant-specific error handling for party registration operations.
type PartyGRPCClient struct {
	*sharedclients.BasePartyClient
}

// NewPartyClient creates a new Party gRPC client using DNS-based load balancing.
//
// Example:
//
//	config := &sharedclients.PartyClientConfig{
//	    ServiceName: "party",
//	    Namespace:   "default",
//	    Port:        50055,
//	    Timeout:     30 * time.Second,
//	}
//	client, err := clients.NewPartyClient(config)
func NewPartyClient(cfg *sharedclients.PartyClientConfig) (*PartyGRPCClient, error) {
	base, err := sharedclients.NewBasePartyClient(cfg)
	if err != nil {
		return nil, err
	}

	return &PartyGRPCClient{
		BasePartyClient: base,
	}, nil
}

// RegisterParty creates a new party in the Party Reference Data Directory.
func (c *PartyGRPCClient) RegisterParty(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.Party, error) {
	ctx, cancel := c.PrepareContext(ctx)
	defer cancel()

	resp, err := c.Client().RegisterParty(ctx, req)
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
				return nil, fmt.Errorf("%w: %v", ErrPartyServiceUnavailable, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
			case codes.DeadlineExceeded:
				// Transient error - request timed out, may be retried
				return nil, fmt.Errorf("%w: %v", ErrPartyServiceTimeout, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
			}
		}
		return nil, fmt.Errorf("%w: %v", ErrPartyRegistrationFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
	}

	return resp.Party, nil
}
