package clients

import (
	"context"
	"errors"
	"fmt"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
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

// GetPartyEmail retrieves the contact email address for a party by looking up
// the "email" attribute in the party's attribute list.
func (c *PartyClientWrapper) GetPartyEmail(ctx context.Context, partyID string) (string, error) {
	resp, err := c.client.RetrieveParty(ctx, &partyv1.RetrievePartyRequest{
		PartyId: partyID,
	})
	if err != nil {
		return "", fmt.Errorf("retrieve party %s: %w", partyID, err)
	}

	if resp.Party == nil {
		return "", fmt.Errorf("%w: %s", ErrPartyNotFound, partyID)
	}

	for _, attr := range resp.Party.Attributes {
		if attr.Key == partyEmailAttributeKey && attr.Value != "" {
			return attr.Value, nil
		}
	}

	return "", fmt.Errorf("%w: party %s", ErrPartyEmailMissing, partyID)
}

// Close terminates the gRPC connection.
func (c *PartyClientWrapper) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
