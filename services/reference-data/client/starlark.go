// Package client provides Starlark service bindings for Reference Data.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga step execution with real Reference Data service integration.
package client

import (
	"context"
	"errors"
	"fmt"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// ErrInvalidVersionType is returned when version parameter is not an integer type.
var ErrInvalidVersionType = errors.New("version must be an integer")

// RegisterStarlarkHandlers registers all Starlark service bindings for Reference Data.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
//
// This function is called during service initialization to register Reference Data handlers
// with the saga execution engine. Each handler includes metadata for conservation rule
// enforcement and operational categorization.
//
// Example usage:
//
//	registry := saga.NewHandlerRegistry()
//	client, cleanup, _ := client.New(client.Config{...})
//	defer cleanup()
//	err := RegisterStarlarkHandlers(registry, client)
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, client *Client) error {
	handlers := map[string]struct {
		handler  saga.Handler
		metadata saga.HandlerMetadata
	}{
		"reference_data.retrieve_instrument": {
			handler: retrieveInstrumentHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategoryValuation,
				Description:          "Retrieve an instrument definition by code and version",
				CompensationStrategy: "none",
				// Reference data handlers are read-only lookups, they don't produce new instruments
				ProducesInstruments: []string{},
				ProtoRequestType:    (*referencedatav1.RetrieveInstrumentRequest)(nil),
				ProtoResponseType:   (*referencedatav1.RetrieveInstrumentResponse)(nil),
				Version:             1,
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

// retrieveInstrumentHandler fetches an instrument definition by code and version.
// This handler provides read-only access to instrument metadata including fungibility
// expressions used in bucket-aware solvency validation.
//
// Parameters:
//   - instrument_code (string): The instrument code (e.g., "USD", "KWH")
//   - version (int): The instrument version (use 0 for latest ACTIVE version)
//
// Returns a map containing:
//   - instrument_code: The instrument code
//   - version: The instrument version
//   - fungibility_key_expression: CEL expression for bucket key generation
//   - is_fungible: Whether the instrument is fungible (true if fungibility_key_expression is set)
//   - dimension: The instrument dimension (e.g., "CURRENCY", "ENERGY")
func retrieveInstrumentHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		// 1. Parse Starlark params using helper functions from shared/pkg/saga
		instrumentCode, err := saga.RequireStringParam(params, "instrument_code")
		if err != nil {
			return nil, err
		}

		// Version defaults to 0 (latest ACTIVE version) if not provided
		version := int32(0)
		if v, ok := params["version"]; ok {
			if vInt, ok := v.(int64); ok {
				version = int32(vInt)
			} else if vInt32, ok := v.(int32); ok {
				version = vInt32
			} else {
				return nil, fmt.Errorf("%w: got %T", ErrInvalidVersionType, v)
			}
		}

		// 2. Prepare client context with saga metadata propagation
		clientCtx := prepareClientContext(ctx)

		// 3. Call gRPC client to retrieve instrument
		instrument, err := client.RetrieveInstrument(clientCtx, instrumentCode, int(version))
		if err != nil {
			return nil, fmt.Errorf("reference_data.retrieve_instrument: %w", err)
		}

		// 4. Convert dimension enum to string
		dimensionStr := dimensionToString(instrument.Dimension)

		// 5. Convert response to Starlark format (map[string]any)
		return map[string]any{
			"instrument_code":            instrument.Code,
			"version":                    instrument.Version,
			"fungibility_key_expression": instrument.FungibilityKeyExpression,
			"is_fungible":                instrument.FungibilityKeyExpression != "",
			"dimension":                  dimensionStr,
		}, nil
	}
}

// prepareClientContext enriches the gRPC client context with saga metadata.
// This function centralizes metadata propagation logic used by all handlers.
//
// Propagated metadata:
//   - Idempotency key: Ensures duplicate saga replays don't create duplicate records
//   - Knowledge_at timestamp: Enables bi-temporal queries (what we knew at a specific time)
//   - Correlation ID: Links all related operations across the distributed system for tracing
//
// The propagation functions (clients.PropagateIdempotencyKey, etc.) add this metadata
// to the gRPC context's outgoing metadata headers, which downstream services can extract.
func prepareClientContext(ctx *saga.StarlarkContext) context.Context {
	clientCtx := ctx.Context

	// Add correlation ID to context value using string literal key so ExtractCorrelationID can find it.
	// ExtractCorrelationID in shared/pkg/clients/common.go uses ctx.Value("x-correlation-id") with
	// string keys, so we must use the same type here despite the linter preference for typed keys.
	//nolint:revive,staticcheck // SA1029,context-keys-type: string key required for ExtractCorrelationID compatibility
	clientCtx = context.WithValue(clientCtx, "x-correlation-id", ctx.CorrelationID.String())

	// Propagate idempotency key and knowledge_at timestamp
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)

	// Note: PropagateCorrelationID is called by the Client methods
	return clientCtx
}

// dimensionToString converts a Dimension enum to a readable string.
func dimensionToString(d referencedatav1.Dimension) string {
	switch d {
	case referencedatav1.Dimension_DIMENSION_UNSPECIFIED:
		return "UNSPECIFIED"
	case referencedatav1.Dimension_DIMENSION_CURRENCY:
		return "CURRENCY"
	case referencedatav1.Dimension_DIMENSION_ENERGY:
		return "ENERGY"
	case referencedatav1.Dimension_DIMENSION_MASS:
		return "MASS"
	case referencedatav1.Dimension_DIMENSION_VOLUME:
		return "VOLUME"
	case referencedatav1.Dimension_DIMENSION_TIME:
		return "TIME"
	case referencedatav1.Dimension_DIMENSION_COMPUTE:
		return "COMPUTE"
	case referencedatav1.Dimension_DIMENSION_CARBON:
		return "CARBON"
	case referencedatav1.Dimension_DIMENSION_DATA:
		return "DATA"
	case referencedatav1.Dimension_DIMENSION_COUNT:
		return "COUNT"
	default:
		return "UNSPECIFIED"
	}
}
