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

// Party validation errors.
var (
	// ErrPartyNotFound is returned when the requested party does not exist.
	ErrPartyNotFound = errors.New("party not found")
	// ErrPartyNotActive is returned when the party exists but is not in ACTIVE status.
	ErrPartyNotActive = errors.New("party not active")
)

// PartyGRPCClient implements PartyClient using gRPC.
//
// Deprecated: Use services/party/client.Client directly with a wrapper for
// CurrentAccount-specific methods (ValidateParty, GetParty). See cmd/main.go
// for the PartyClientWrapper implementation.
//
// This client embeds the shared BasePartyClient for connection management
// and adds current-account-specific error handling for party validation operations.
type PartyGRPCClient struct {
	*sharedclients.BasePartyClient
}

// NewPartyClient creates a new Party gRPC client using DNS-based load balancing.
//
// Deprecated: Use services/party/client.New() instead with a PartyClientWrapper.
// See cmd/main.go for the recommended pattern.
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

// ValidateParty checks if a party exists and is active.
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

// GetParty retrieves full party details by ID.
func (c *PartyGRPCClient) GetParty(ctx context.Context, partyID string) (*partyv1.Party, error) {
	ctx, cancel := c.PrepareContext(ctx)
	defer cancel()

	resp, err := c.Client().RetrieveParty(ctx, &partyv1.RetrievePartyRequest{
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
