// Package main provides the PartyClientWrapper for adapting the service-owned
// party client to the CurrentAccount service's PartyClient interface.
package main

import (
	"context"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/current-account/clients" //nolint:staticcheck // Using clients package for PartyClient interface and errors
	partyclient "github.com/meridianhub/meridian/services/party/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Compile-time interface assertion.
var _ clients.PartyClient = (*PartyClientWrapper)(nil)

// PartyClientWrapper wraps the service-owned party client with CurrentAccount-specific methods.
//
// The service-owned party client provides raw gRPC operations (RetrieveParty, RegisterParty),
// but CurrentAccount needs higher-level convenience methods (ValidateParty, GetParty) that
// handle status checking and error translation.
//
// This wrapper implements the clients.PartyClient interface expected by the service layer.
type PartyClientWrapper struct {
	client *partyclient.Client
}

// NewPartyClientWrapper creates a new wrapper around the service-owned party client.
func NewPartyClientWrapper(client *partyclient.Client) *PartyClientWrapper {
	return &PartyClientWrapper{client: client}
}

// ValidateParty checks if a party exists and is active.
//
// Returns nil if the party exists and has ACTIVE status.
// Returns clients.ErrPartyNotFound if the party does not exist.
// Returns clients.ErrPartyNotActive if the party exists but is not ACTIVE.
func (w *PartyClientWrapper) ValidateParty(ctx context.Context, partyID string) error {
	party, err := w.GetParty(ctx, partyID)
	if err != nil {
		return err
	}

	if party.Status != partyv1.PartyStatus_PARTY_STATUS_ACTIVE {
		return clients.ErrPartyNotActive
	}

	return nil
}

// GetParty retrieves full party details by ID.
//
// Returns the party data if found, or an error if not found.
// Errors are passed through from the underlying client without re-wrapping
// to avoid double-wrapped error messages.
func (w *PartyClientWrapper) GetParty(ctx context.Context, partyID string) (*partyv1.Party, error) {
	resp, err := w.client.RetrieveParty(ctx, &partyv1.RetrievePartyRequest{
		PartyId: partyID,
	})
	if err != nil {
		// Check for NOT_FOUND status and translate to domain error
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, clients.ErrPartyNotFound
		}
		// Pass through error without re-wrapping (underlying client already wraps)
		return nil, err
	}

	return resp.Party, nil
}

// Close terminates the client connection gracefully.
func (w *PartyClientWrapper) Close() error {
	return w.client.Close()
}
