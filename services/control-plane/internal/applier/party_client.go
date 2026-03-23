package applier

import (
	"context"
	"errors"
	"fmt"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrUnknownExternalReferenceType is returned when an unrecognized external_reference_type is provided.
var ErrUnknownExternalReferenceType = errors.New("unknown external_reference_type")

// errPartyNotFound is returned when a party lookup finds no matching party.
var errPartyNotFound = errors.New("party not found")

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
		// Idempotency: treat AlreadyExists as success for manifest re-apply scenarios
		// where the underlying party was already created by a previous apply.
		if status.Code(err) == codes.AlreadyExists {
			return c.handleAlreadyExists(callCtx, req)
		}
		return nil, fmt.Errorf("register organization: %w", err)
	}

	party := resp.GetParty()
	return map[string]any{
		"party_id":   party.GetPartyId(),
		"legal_name": party.GetLegalName(),
		"status":     party.GetStatus().String(),
	}, nil
}

// handleAlreadyExists resolves the existing party on AlreadyExists so that
// downstream saga steps receive the party_id. Falls back to a best-effort
// result without party_id if the lookup fails.
func (c *PartyClient) handleAlreadyExists(ctx context.Context, req *partyv1.RegisterPartyRequest) (any, error) {
	existing, _ := c.findPartyByExternalRef(ctx, req.GetExternalReference(), req.GetExternalReferenceType())
	if existing != nil {
		return map[string]any{
			"party_id":   existing.GetPartyId(),
			"legal_name": existing.GetLegalName(),
			"status":     existing.GetStatus().String(),
		}, nil
	}
	return map[string]any{
		"legal_name": req.GetLegalName(),
		"status":     partyv1.PartyStatus_PARTY_STATUS_ACTIVE.String(),
	}, nil
}

// findPartyByExternalRef pages through ListParties to locate an existing party
// that matches the given external reference and type. Returns nil if not found.
func (c *PartyClient) findPartyByExternalRef(ctx context.Context, extRef string, extRefType partyv1.ExternalReferenceType) (*partyv1.Party, error) {
	pageToken := ""
	for {
		resp, err := c.client.ListParties(ctx, &partyv1.ListPartiesRequest{
			PartyType: partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
			PageSize:  100,
			PageToken: pageToken,
		})
		if err != nil {
			return nil, fmt.Errorf("list parties for lookup: %w", err)
		}
		for _, p := range resp.GetParties() {
			if p.GetExternalReference() == extRef && p.GetExternalReferenceType() == extRefType {
				return p, nil
			}
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return nil, errPartyNotFound
		}
	}
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
