package applier

import (
	"fmt"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/grpc"
)

// PartyClient wraps the party gRPC client to implement PartyService for use as a
// saga handler dependency.
//
// The client translates between the flat map[string]any parameter convention used
// by saga handlers and the typed proto messages required by the gRPC service.
type PartyClient struct {
	client partyv1.PartyServiceClient
}

// NewPartyClient creates a new PartyClient from a gRPC connection.
func NewPartyClient(conn *grpc.ClientConn) *PartyClient {
	return &PartyClient{
		client: partyv1.NewPartyServiceClient(conn),
	}
}

// RegisterOrganization implements PartyService.
// Converts Starlark params to a RegisterPartyRequest with PARTY_TYPE_ORGANIZATION
// and calls the gRPC service.
func (c *PartyClient) RegisterOrganization(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &partyv1.RegisterPartyRequest{
		PartyType: partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
	}
	req.LegalName, _ = params["legal_name"].(string)
	req.DisplayName, _ = params["display_name"].(string)
	req.ExternalReference, _ = params["external_reference"].(string)

	externalRefTypeStr, _ := params["external_reference_type"].(string)
	req.ExternalReferenceType = parseExternalReferenceType(externalRefTypeStr)

	callCtx := clients.PropagateIdempotencyKey(ctx.Context, ctx.IdempotencyKey)
	resp, err := c.client.RegisterParty(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("register organization: %w", err)
	}

	party := resp.GetParty()
	return map[string]any{
		"party_id":   party.GetPartyId(),
		"legal_name": party.GetLegalName(),
		"status":     party.GetStatus().String(),
	}, nil
}

// parseExternalReferenceType converts a string to the proto ExternalReferenceType enum.
func parseExternalReferenceType(s string) partyv1.ExternalReferenceType {
	if v, ok := partyv1.ExternalReferenceType_value[s]; ok {
		return partyv1.ExternalReferenceType(v)
	}
	return partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED
}

// Ensure PartyClient implements PartyService at compile time.
var _ PartyService = (*PartyClient)(nil)
