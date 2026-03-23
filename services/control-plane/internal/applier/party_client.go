package applier

import (
	"errors"
	"fmt"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/grpc"
)

// ErrUnknownExternalReferenceType is returned when an unrecognized external_reference_type is provided.
var ErrUnknownExternalReferenceType = errors.New("unknown external_reference_type")

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
	externalRefType, err := parseExternalReferenceType(externalRefTypeStr)
	if err != nil {
		return nil, fmt.Errorf("register organization: %w", err)
	}
	req.ExternalReferenceType = externalRefType

	callCtx := prepareCallContext(ctx)
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
// Accepts both prefixed ("EXTERNAL_REFERENCE_TYPE_LEI") and stripped ("LEI") forms,
// since the Starlark handler schema uses stripped names while proto uses prefixed names.
// Returns an error for non-empty strings that do not match a known type.
func parseExternalReferenceType(s string) (partyv1.ExternalReferenceType, error) {
	if s == "" {
		return partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED, nil
	}
	if v, ok := partyv1.ExternalReferenceType_value[s]; ok {
		return partyv1.ExternalReferenceType(v), nil
	}
	// Try with prefix added (handles stripped form like "LEI").
	if v, ok := partyv1.ExternalReferenceType_value["EXTERNAL_REFERENCE_TYPE_"+s]; ok {
		return partyv1.ExternalReferenceType(v), nil
	}
	return 0, fmt.Errorf("%w: %q", ErrUnknownExternalReferenceType, s)
}

// ControlOrganization implements PartyService.
// Converts Starlark params to a ControlPartyRequest with CONTROL_ACTION_TERMINATE
// and calls the gRPC service. This is used when a manifest DELETE removes an organization.
func (c *PartyClient) ControlOrganization(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &partyv1.ControlPartyRequest{
		ControlAction: partyv1.ControlAction_CONTROL_ACTION_TERMINATE,
	}
	req.PartyId, _ = params["party_id"].(string)
	req.Reason, _ = params["reason"].(string)
	if req.Reason == "" {
		req.Reason = "Organization removed from manifest"
	}

	callCtx := prepareCallContext(ctx)
	resp, err := c.client.ControlParty(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("control organization: %w", err)
	}

	party := resp.GetParty()
	return map[string]any{
		"party_id": party.GetPartyId(),
		"status":   party.GetStatus().String(),
	}, nil
}

// Ensure PartyClient implements PartyService at compile time.
var _ PartyService = (*PartyClient)(nil)
