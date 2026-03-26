package clients

import (
	"context"
	"errors"
	"fmt"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/payment-order/worker"
	"google.golang.org/grpc"
)

var (
	// ErrPartyNotFound is returned when the party does not exist.
	ErrPartyNotFound = errors.New("party not found")

	// ErrPartyEmailMissing is returned when the party has no email attribute.
	ErrPartyEmailMissing = errors.New("party has no email attribute")
)

// partyEmailAttributeKey is the attribute key used to store a party's contact email.
const partyEmailAttributeKey = "email"

// PartyClientWrapper wraps the gRPC client for the party service.
type PartyClientWrapper struct {
	conn   *grpc.ClientConn
	client partyv1.PartyServiceClient
}

// NewPartyClient creates a new party client wrapper.
func NewPartyClient(conn *grpc.ClientConn) *PartyClientWrapper {
	return &PartyClientWrapper{
		conn:   conn,
		client: partyv1.NewPartyServiceClient(conn),
	}
}

// GetPartyContact retrieves the contact email and display name for a party.
// Email is extracted from the party's "email" attribute. Name uses DisplayName if set,
// falling back to LegalName.
func (c *PartyClientWrapper) GetPartyContact(ctx context.Context, partyID string) (worker.PartyContact, error) {
	resp, err := c.client.RetrieveParty(ctx, &partyv1.RetrievePartyRequest{
		PartyId: partyID,
	})
	if err != nil {
		return worker.PartyContact{}, fmt.Errorf("retrieve party %s: %w", partyID, err)
	}

	if resp.Party == nil {
		return worker.PartyContact{}, fmt.Errorf("%w: %s", ErrPartyNotFound, partyID)
	}

	var email string
	for _, attr := range resp.Party.Attributes {
		if attr.Key == partyEmailAttributeKey && attr.Value != "" {
			email = attr.Value
			break
		}
	}

	if email == "" {
		return worker.PartyContact{}, fmt.Errorf("%w: party %s", ErrPartyEmailMissing, partyID)
	}

	name := resp.Party.DisplayName
	if name == "" {
		name = resp.Party.LegalName
	}

	return worker.PartyContact{Email: email, Name: name}, nil
}

// Close terminates the gRPC connection.
func (c *PartyClientWrapper) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
