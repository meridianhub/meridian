package main

import (
	"context"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/current-account/service"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Compile-time interface assertion.
var _ service.PartyClient = (*partyClientWrapper)(nil)

// partyClientWrapper adapts the party gRPC client to the current-account
// service's PartyClient interface. It translates raw gRPC operations
// (RetrieveParty) into the higher-level methods (ValidateParty, GetParty)
// expected by the service layer.
type partyClientWrapper struct {
	client *partyclient.Client
}

func newPartyClientWrapper(client *partyclient.Client) *partyClientWrapper {
	return &partyClientWrapper{client: client}
}

// ValidateParty checks if a party exists and is active.
func (w *partyClientWrapper) ValidateParty(ctx context.Context, partyID string) error {
	party, err := w.GetParty(ctx, partyID)
	if err != nil {
		return err
	}

	if party.Status != partyv1.PartyStatus_PARTY_STATUS_ACTIVE {
		return service.ErrPartyNotActive
	}

	return nil
}

// GetParty retrieves full party details by ID.
func (w *partyClientWrapper) GetParty(ctx context.Context, partyID string) (*partyv1.Party, error) {
	resp, err := w.client.RetrieveParty(ctx, &partyv1.RetrievePartyRequest{
		PartyId: partyID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, service.ErrPartyNotFound
		}
		return nil, err
	}

	return resp.Party, nil
}

// Close terminates the client connection gracefully.
func (w *partyClientWrapper) Close() error {
	return w.client.Close()
}
