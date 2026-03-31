package main

import (
	"context"
	"errors"
	"fmt"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"google.golang.org/grpc"
)

// Sentinel errors for party email resolution.
var (
	ErrPartyEmailEmpty    = errors.New("party has empty email attribute")
	ErrPartyEmailNotFound = errors.New("party has no email attribute")
)

// Compile-time interface assertion.
var _ email.PartyEmailResolver = (*grpcPartyEmailResolver)(nil)

// grpcPartyEmailResolver resolves a party ID to an email address by calling the
// party gRPC service and looking up the "email" attribute.
type grpcPartyEmailResolver struct {
	client partyv1.PartyServiceClient
}

// newGRPCPartyEmailResolver creates a resolver that uses the given gRPC connection
// to call the party service.
func newGRPCPartyEmailResolver(conn *grpc.ClientConn) *grpcPartyEmailResolver {
	return &grpcPartyEmailResolver{
		client: partyv1.NewPartyServiceClient(conn),
	}
}

// ResolveEmail retrieves the party and returns the value of the "email" attribute.
func (r *grpcPartyEmailResolver) ResolveEmail(ctx context.Context, partyID string) (string, error) {
	resp, err := r.client.RetrieveParty(ctx, &partyv1.RetrievePartyRequest{
		PartyId: partyID,
	})
	if err != nil {
		return "", fmt.Errorf("retrieve party %s: %w", partyID, err)
	}

	for _, attr := range resp.Party.GetAttributes() {
		if attr.Key == "email" {
			if attr.Value == "" {
				return "", fmt.Errorf("party %s: %w", partyID, ErrPartyEmailEmpty)
			}
			return attr.Value, nil
		}
	}

	return "", fmt.Errorf("party %s: %w", partyID, ErrPartyEmailNotFound)
}
