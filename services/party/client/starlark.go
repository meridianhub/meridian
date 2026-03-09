// Package client provides Starlark service bindings for the Party service.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga step execution with real Party service integration.
package client

import (
	"context"
	"errors"
	"fmt"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	_structpb "google.golang.org/protobuf/types/known/structpb"
)

var (
	// errMissingPaymentMethod is returned when the gRPC response contains no payment method.
	errMissingPaymentMethod = errors.New("missing payment method in response")

	// errInvalidRelationshipType is returned when the relationship_type parameter is not recognized.
	errInvalidRelationshipType = errors.New("invalid relationship_type")
)

// RegisterStarlarkHandlers registers all Starlark service bindings for the Party service.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, client *Client) error {
	handlers := map[string]struct {
		handler  saga.Handler
		metadata saga.HandlerMetadata
	}{
		"party.get_default_payment_method": {
			handler: getDefaultPaymentMethodHandler(client),
			metadata: saga.HandlerMetadata{
				Description:          "Retrieve the default payment method for a party",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				ProtoRequestType:     (*partyv1.GetDefaultPaymentMethodRequest)(nil),
				ProtoResponseType:    (*partyv1.GetDefaultPaymentMethodResponse)(nil),
				Version:              1,
			},
		},
		"party.list_participants": {
			handler: listParticipantsHandler(client),
			metadata: saga.HandlerMetadata{
				Description:          "List active participants for a syndicate organization",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				ProtoRequestType:     (*partyv1.ListParticipantsRequest)(nil),
				ProtoResponseType:    (*partyv1.ListParticipantsResponse)(nil),
				Version:              1,
			},
		},
		"party.get_structuring_data": {
			handler: getStructuringDataHandler(client),
			metadata: saga.HandlerMetadata{
				Description:          "Retrieve structuring metadata for a participant in a syndicate",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				ProtoRequestType:     (*partyv1.GetStructuringDataRequest)(nil),
				ProtoResponseType:    (*partyv1.GetStructuringDataResponse)(nil),
				Version:              1,
			},
		},
	}

	for name, h := range handlers {
		if err := registry.RegisterWithMetadata(name, h.handler, &h.metadata); err != nil {
			return fmt.Errorf("failed to register %s: %w", name, err)
		}
	}
	return nil
}

// getDefaultPaymentMethodHandler retrieves the default payment method for a party.
// This is a read-only lookup used by the stripe_payment saga to resolve payment
// method details before creating a lien and sending to the gateway.
//
// Parameters:
//   - party_id (string): Party identifier (UUID)
//
// Returns a map containing:
//   - provider: Payment provider enum string (e.g., "PAYMENT_METHOD_PROVIDER_STRIPE")
//   - provider_customer_id: Provider-assigned customer identifier
//   - provider_method_id: Provider-assigned payment method identifier
//   - method_type: Payment method type enum string (e.g., "PAYMENT_METHOD_TYPE_CARD")
func getDefaultPaymentMethodHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		partyID, err := saga.RequireStringParam(params, "party_id")
		if err != nil {
			return nil, err
		}

		if err := ctx.ValidatePartyAccessFromString(partyID); err != nil {
			return nil, fmt.Errorf("party.get_default_payment_method: %w", err)
		}

		clientCtx := preparePartyClientContext(ctx)

		resp, err := client.GetDefaultPaymentMethod(clientCtx, &partyv1.GetDefaultPaymentMethodRequest{
			PartyId: partyID,
		})
		if err != nil {
			return nil, fmt.Errorf("party.get_default_payment_method: %w", err)
		}

		pm := resp.GetPaymentMethod()
		if pm == nil {
			return nil, fmt.Errorf("party.get_default_payment_method: %w: party_id %s", errMissingPaymentMethod, partyID)
		}
		return map[string]any{
			"provider":             pm.GetProvider().String(),
			"provider_customer_id": pm.GetProviderCustomerId(),
			"provider_method_id":   pm.GetProviderMethodId(),
			"method_type":          pm.GetMethodType().String(),
		}, nil
	}
}

// listParticipantsHandler returns active participants for a syndicate organization.
// Results are cached in the saga LookupCache for deterministic replay.
//
// Parameters:
//   - org_id (string): Organization party identifier (UUID)
//   - relationship_type (string): Relationship type filter (e.g., "SYNDICATE_PARTICIPANT")
//
// Returns a list of dicts, each containing:
//   - party_id: Participant party identifier
//   - metadata: Structuring metadata dict (e.g., allocation_share, role)
func listParticipantsHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		orgID, err := saga.RequireStringParam(params, "org_id")
		if err != nil {
			return nil, err
		}

		if err := ctx.ValidatePartyAccessFromString(orgID); err != nil {
			return nil, fmt.Errorf("party.list_participants: %w", err)
		}

		relType, err := parseRelationshipType(params)
		if err != nil {
			return nil, fmt.Errorf("party.list_participants: %w", err)
		}

		// Check lookup cache for deterministic replay
		cacheKey := saga.GenerateCacheKey("party.list_participants", params)
		if cached, ok := lookupCachedSlice(ctx, cacheKey); ok {
			return cached, nil
		}

		clientCtx := preparePartyClientContext(ctx)

		resp, err := client.ListParticipants(clientCtx, &partyv1.ListParticipantsRequest{
			OrgPartyId:       orgID,
			RelationshipType: relType,
		})
		if err != nil {
			return nil, fmt.Errorf("party.list_participants: %w", err)
		}

		result := convertParticipantsToResult(resp.GetParticipants())

		// Cache result for replay
		if ctx.LookupCache != nil {
			ctx.LookupCache.Set(cacheKey, result)
		}

		return result, nil
	}
}

// getStructuringDataHandler retrieves structuring metadata for a participant in a syndicate.
// Results are cached in the saga LookupCache for deterministic replay.
//
// Parameters:
//   - party_id (string): Participant party identifier (UUID)
//   - org_id (string): Organization party identifier (UUID)
//   - relationship_type (string): Relationship type (e.g., "SYNDICATE_PARTICIPANT")
//
// Returns a dict of metadata or empty dict if not found.
func getStructuringDataHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		partyID, err := saga.RequireStringParam(params, "party_id")
		if err != nil {
			return nil, err
		}

		orgID, err := saga.RequireStringParam(params, "org_id")
		if err != nil {
			return nil, err
		}

		if err := ctx.ValidatePartyAccessFromString(partyID); err != nil {
			return nil, fmt.Errorf("party.get_structuring_data: %w", err)
		}
		if err := ctx.ValidatePartyAccessFromString(orgID); err != nil {
			return nil, fmt.Errorf("party.get_structuring_data: %w", err)
		}

		relType, err := parseRelationshipType(params)
		if err != nil {
			return nil, fmt.Errorf("party.get_structuring_data: %w", err)
		}

		// Check lookup cache for deterministic replay
		cacheKey := saga.GenerateCacheKey("party.get_structuring_data", params)
		if cached, ok := lookupCachedMap(ctx, cacheKey); ok {
			return cached, nil
		}

		clientCtx := preparePartyClientContext(ctx)

		resp, err := client.GetStructuringData(clientCtx, &partyv1.GetStructuringDataRequest{
			PartyId:          partyID,
			OrgPartyId:       orgID,
			RelationshipType: relType,
		})
		if err != nil {
			return nil, fmt.Errorf("party.get_structuring_data: %w", err)
		}

		result := structToMap(resp.GetMetadata())

		// Cache result for replay
		if ctx.LookupCache != nil {
			ctx.LookupCache.Set(cacheKey, result)
		}

		return result, nil
	}
}

// structToMap converts a protobuf Struct to map[string]any.
func structToMap(s *_structpb.Struct) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	m := s.AsMap()
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

// parseRelationshipType extracts and validates the relationship_type parameter.
func parseRelationshipType(params map[string]any) (partyv1.RelationshipType, error) {
	relTypeStr, err := saga.RequireStringParam(params, "relationship_type")
	if err != nil {
		return 0, err
	}

	val, ok := partyv1.RelationshipType_value[relTypeStr]
	if !ok {
		return 0, fmt.Errorf("%w: %q", errInvalidRelationshipType, relTypeStr)
	}
	return partyv1.RelationshipType(val), nil
}

// lookupCachedSlice checks the LookupCache for a cached slice result.
func lookupCachedSlice(ctx *saga.StarlarkContext, cacheKey string) ([]any, bool) {
	if ctx.LookupCache == nil {
		return nil, false
	}
	cached, found := ctx.LookupCache.Get(cacheKey)
	if !found {
		return nil, false
	}
	if result, ok := cached.([]any); ok {
		return result, true
	}
	return nil, false
}

// lookupCachedMap checks the LookupCache for a cached map result.
func lookupCachedMap(ctx *saga.StarlarkContext, cacheKey string) (map[string]any, bool) {
	if ctx.LookupCache == nil {
		return nil, false
	}
	cached, found := ctx.LookupCache.Get(cacheKey)
	if !found {
		return nil, false
	}
	if result, ok := cached.(map[string]any); ok {
		return result, true
	}
	return nil, false
}

// convertParticipantsToResult converts proto Association messages to a result slice.
func convertParticipantsToResult(participants []*partyv1.Association) []any {
	result := make([]any, 0, len(participants))
	for _, p := range participants {
		result = append(result, map[string]any{
			"party_id": p.GetRelatedPartyId(),
			"metadata": structToMap(p.GetMetadata()),
		})
	}
	return result
}

// preparePartyClientContext enriches the gRPC client context with saga metadata.
func preparePartyClientContext(ctx *saga.StarlarkContext) context.Context {
	clientCtx := ctx.Context
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)
	return clientCtx
}
