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
)

// errMissingPaymentMethod is returned when the gRPC response contains no payment method.
var errMissingPaymentMethod = errors.New("missing payment method in response")

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
				ProducesInstruments: []string{},
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

// preparePartyClientContext enriches the gRPC client context with saga metadata.
func preparePartyClientContext(ctx *saga.StarlarkContext) context.Context {
	clientCtx := ctx.Context
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)
	return clientCtx
}
