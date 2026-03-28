// Package client provides Starlark service bindings for Market Information.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga step execution with real Market Information service integration.
package client

import (
	"context"
	"fmt"
	"time"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
)

// RegisterStarlarkHandlers registers all Starlark service bindings for Market Information.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
//
// This function is called during service initialization to register Market Information handlers
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
		"market_information.get_rate": {
			handler: getRateHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategoryValuation,
				Description:          "Fetch FX rates for currency pair conversion",
				CompensationStrategy: "none",
				// Read-only handler - provides reference data for valuation
				// Does not produce instruments, only queries existing rates
				ProducesInstruments: []string{},
				ProtoRequestType:    (*marketinformationv1.ListObservationsRequest)(nil),
				ProtoResponseType:   (*marketinformationv1.ListObservationsResponse)(nil),
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

// getRateHandler fetches FX rates for currency pair conversion via gRPC.
// This handler adapts Starlark parameters to the GetRate RPC call,
// propagating saga metadata for idempotency, tracing, and bi-temporal queries.
//
// FX rates are used in multi-currency payment sagas to calculate settlement amounts
// when the payer and payee use different currencies.
//
// Parameters:
//   - from_currency (string): The source currency code (e.g., "USD", "EUR")
//   - to_currency (string): The destination currency code (e.g., "EUR", "GBP")
//   - rate_date (optional time.Time): The date for which to fetch the rate (defaults to now)
//
// Returns a map containing:
//   - from_currency: The source currency code
//   - to_currency: The destination currency code
//   - rate: The exchange rate as decimal (e.g., 1.0856 for USD/EUR)
//   - rate_date: The effective date of the rate
//   - source: The data source identifier
//
// Edge cases:
//   - Same currency (USD->USD): Returns rate = 1.0 without calling the service
//   - Rate not found: Returns error from service
//   - Future date: Service validates and returns INVALID_ARGUMENT error
func getRateHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		fromCurrency, err := saga.RequireStringParam(params, "from_currency")
		if err != nil {
			return nil, err
		}

		toCurrency, err := saga.RequireStringParam(params, "to_currency")
		if err != nil {
			return nil, err
		}

		// Handle same currency case without calling the service
		if fromCurrency == toCurrency {
			return buildIdentityRateResult(fromCurrency, toCurrency), nil
		}

		rateDate := time.Now()
		if val, ok := params["rate_date"].(time.Time); ok {
			rateDate = val
		}

		clientCtx := prepareClientContext(ctx)
		datasetCode := fmt.Sprintf("%s_%s_FX", fromCurrency, toCurrency)

		obs, err := client.GetRate(
			clientCtx,
			datasetCode,
			"spot",
			rateDate,
			marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		)
		if err != nil {
			return nil, fmt.Errorf("market_information.get_rate: %w", err)
		}

		return buildRateResult(fromCurrency, toCurrency, obs)
	}
}

// buildIdentityRateResult returns a rate result for same-currency conversions (rate = 1.0).
func buildIdentityRateResult(fromCurrency, toCurrency string) map[string]any {
	return map[string]any{
		"from_currency": fromCurrency,
		"to_currency":   toCurrency,
		"rate":          decimal.NewFromInt(1),
		"rate_date":     time.Now(),
		"source":        "IDENTITY",
	}
}

// buildRateResult converts a gRPC observation response into the Starlark result map.
func buildRateResult(fromCurrency, toCurrency string, obs *marketinformationv1.MarketPriceObservation) (map[string]any, error) {
	rate, err := decimal.NewFromString(obs.Value)
	if err != nil {
		return nil, fmt.Errorf("market_information.get_rate: invalid rate value: %w", err)
	}

	return map[string]any{
		"from_currency": fromCurrency,
		"to_currency":   toCurrency,
		"rate":          rate,
		"rate_date":     obs.ValidFrom.AsTime(),
		"source":        obs.SourceId,
	}, nil
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

	// Add correlation ID to context value using string literal key so PropagateCorrelationID can extract it.
	// ExtractCorrelationID in shared/pkg/clients/common.go uses ctx.Value("x-correlation-id") with
	// string keys, so we must use the same type here despite the linter preference for typed keys.
	//nolint:revive,staticcheck // SA1029,context-keys-type: string key required for PropagateCorrelationID/ExtractCorrelationID compatibility
	clientCtx = context.WithValue(clientCtx, "x-correlation-id", ctx.CorrelationID.String())
	clientCtx = clients.PropagateCorrelationID(clientCtx) // Adds to gRPC metadata headers

	// Propagate and apply idempotency key and knowledge_at timestamp to gRPC metadata
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.ApplyIdempotencyKey(clientCtx, nil) // Adds to gRPC metadata headers

	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)
	clientCtx = clients.ApplyKnowledgeAt(clientCtx) // Adds to gRPC metadata headers

	return clientCtx
}
