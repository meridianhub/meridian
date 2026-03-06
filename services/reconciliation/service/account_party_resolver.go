package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
)

var (
	// ErrNoFacilityReturned is returned when the Current Account service returns no facility.
	ErrNoFacilityReturned = errors.New("no facility returned for account")

	// ErrNoPartyID is returned when the account has no party_id set.
	ErrNoPartyID = errors.New("account has no party_id")
)

// AccountPartyResolver resolves the owning party ID for a given account.
type AccountPartyResolver interface {
	// ResolvePartyID returns the party UUID that owns the given account.
	ResolvePartyID(ctx context.Context, accountID string) (uuid.UUID, error)
}

// GRPCAccountPartyResolver resolves party IDs by calling the Current Account gRPC service.
type GRPCAccountPartyResolver struct {
	client currentaccountv1.CurrentAccountServiceClient
}

// NewGRPCAccountPartyResolver creates a resolver backed by the Current Account gRPC service.
func NewGRPCAccountPartyResolver(client currentaccountv1.CurrentAccountServiceClient) *GRPCAccountPartyResolver {
	return &GRPCAccountPartyResolver{client: client}
}

// ResolvePartyID calls RetrieveCurrentAccount and extracts the party_id.
func (r *GRPCAccountPartyResolver) ResolvePartyID(ctx context.Context, accountID string) (uuid.UUID, error) {
	resp, err := r.client.RetrieveCurrentAccount(ctx, &currentaccountv1.RetrieveCurrentAccountRequest{
		AccountId: accountID,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to retrieve account %s: %w", accountID, err)
	}

	facility := resp.GetFacility()
	if facility == nil {
		return uuid.Nil, fmt.Errorf("%w: %s", ErrNoFacilityReturned, accountID)
	}

	partyIDStr := facility.GetPartyId()
	if partyIDStr == "" {
		return uuid.Nil, fmt.Errorf("%w: %s", ErrNoPartyID, accountID)
	}

	partyID, err := uuid.Parse(partyIDStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid party_id %q for account %s: %w", partyIDStr, accountID, err)
	}

	return partyID, nil
}
