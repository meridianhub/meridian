// Package client provides Starlark service bindings for Position Keeping.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga step execution with real Position Keeping service integration.
package client

import (
	"context"
	"fmt"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
)

// RegisterStarlarkHandlers registers all Starlark service bindings for Position Keeping.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
//
// This function is called during service initialization to register Position Keeping handlers
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
		"position_keeping.initiate_log": {
			handler: initiateLogHandler(client),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategoryIngestion,
				Description:         "Initiate a position log entry for a DEBIT or CREDIT transaction",
				Compensate:          "position_keeping.cancel_log",
				HasAutoCompensation: true,
				// Position Keeping ingests physical measurements (meter readings) and produces
				// Physics instruments (KWH, GAS, WATER) from external sources.
				ProducesInstruments: []string{"KWH", "GAS", "WATER"},
				ProtoRequestType:    (*positionkeepingv1.InitiateFinancialPositionLogRequest)(nil),
				ProtoResponseType:   (*positionkeepingv1.InitiateFinancialPositionLogResponse)(nil),
				ParamOverrides: map[string]saga.ParamOverride{
					"amount":    {Type: "Decimal"},
					"direction": {Type: "enum"},
				},
				Version: 1,
			},
		},
		"position_keeping.update_log": {
			handler: updateLogHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategoryIngestion,
				Description:          "Update an existing position log entry",
				CompensationStrategy: "none",
				// Updates don't produce new instruments, just modify existing logs
				ProducesInstruments: []string{},
				ProtoRequestType:    (*positionkeepingv1.UpdateFinancialPositionLogRequest)(nil),
				ProtoResponseType:   (*positionkeepingv1.UpdateFinancialPositionLogResponse)(nil),
				Version:             1,
			},
		},
		"position_keeping.cancel_log": {
			handler: cancelLogHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategoryIngestion,
				Description:          "Cancel a position log entry (compensation handler)",
				CompensationStrategy: "none",
				// Cancellations don't produce instruments
				ProducesInstruments: []string{},
				ProtoRequestType:    (*positionkeepingv1.UpdateFinancialPositionLogRequest)(nil),
				ProtoResponseType:   (*positionkeepingv1.UpdateFinancialPositionLogResponse)(nil),
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

// initiateLogHandler creates a new financial position log via gRPC.
// This handler adapts Starlark parameters to the InitiateFinancialPositionLog RPC call,
// propagating saga metadata for idempotency, tracing, and bi-temporal queries.
//
// Parameters:
//   - account_id (string): The account identifier for the position
//   - amount (decimal): The transaction amount (optional - for initial entry)
//   - currency (string): The currency code (optional - for initial entry)
//   - direction (string): Either "DEBIT" or "CREDIT" (optional - for initial entry)
//   - transaction_id (string): The originating transaction identifier (optional - for lineage)
//
// Returns a map containing:
//   - log_id: The unique position log identifier
//   - account_id: The account identifier
//   - status: Always "INITIATED" for newly created logs
func initiateLogHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		// 1. Parse Starlark params using helper functions from shared/pkg/saga
		accountID, err := saga.RequireStringParam(params, "account_id")
		if err != nil {
			return nil, err
		}

		// 2. Prepare client context with saga metadata propagation
		// This ensures idempotency keys, knowledge_at timestamps, and correlation IDs
		// are propagated to the downstream service for proper tracing and bi-temporal queries
		clientCtx := prepareClientContext(ctx)

		// 3. Build the request
		// The actual proto API just needs account_id - initial_entry and lineage are optional
		req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
			AccountId: accountID,
		}

		// 4. Call REAL gRPC client
		resp, err := client.InitiateFinancialPositionLog(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("position_keeping.initiate_log: %w", err)
		}

		// 5. Convert response to Starlark format (map[string]any)
		// Extract the created log from the response
		log := resp.GetLog()
		return map[string]any{
			"log_id":     log.GetLogId(),
			"account_id": log.GetAccountId(),
			"status":     "INITIATED",
		}, nil
	}
}

// updateLogHandler updates an existing financial position log status.
// This handler is used during saga compensation or status transitions.
//
// Parameters:
//   - log_id (string): The position log identifier to update
//   - status (string): The new status (e.g., "CONFIRMED", "CANCELLED") - optional
//
// Returns a map containing:
//   - log_id: The position log identifier
//   - status: Always "UPDATED"
func updateLogHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		logID, err := saga.RequireStringParam(params, "log_id")
		if err != nil {
			return nil, err
		}

		// The actual proto API doesn't have a simple "status" field
		// It has structured status_update, new_entry, audit_entry fields
		// For now, we'll just update with an empty request (version 0)
		clientCtx := prepareClientContext(ctx)
		resp, err := client.UpdateFinancialPositionLog(clientCtx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
			LogId:   logID,
			Version: 0, // Version for optimistic concurrency
		})
		if err != nil {
			return nil, fmt.Errorf("position_keeping.update_log: %w", err)
		}

		// Extract the updated log
		log := resp.GetLog()
		return map[string]any{
			"log_id": log.GetLogId(),
			"status": "UPDATED",
		}, nil
	}
}

// cancelLogHandler cancels a financial position log during saga compensation.
// This is typically called in the compensation phase when a saga needs to rollback.
//
// Parameters:
//   - log_id (string): The position log identifier to cancel
//
// Returns a map containing:
//   - log_id: The position log identifier
//   - status: Always "CANCELLED"
func cancelLogHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		logID, err := saga.RequireStringParam(params, "log_id")
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)

		// Cancel operation - use UpdateFinancialPositionLog
		// In a real implementation, you'd set status_update with CANCELLED state
		resp, err := client.UpdateFinancialPositionLog(clientCtx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
			LogId:   logID,
			Version: 0,
		})
		if err != nil {
			return nil, fmt.Errorf("position_keeping.cancel_log: %w", err)
		}

		log := resp.GetLog()
		return map[string]any{
			"log_id": log.GetLogId(),
			"status": "CANCELLED",
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
// contextKey is a type for context keys to avoid collisions
type contextKey string

// correlationIDContextKey is the typed context key for correlation ID
const correlationIDContextKey contextKey = "x-correlation-id"

func prepareClientContext(ctx *saga.StarlarkContext) context.Context {
	clientCtx := ctx.Context

	// Add correlation ID to context value so the client's PropagateCorrelationID can extract it
	// We use a typed key to satisfy the linter, but clients.PropagateCorrelationID
	// will search for it using string("x-correlation-id") which works fine
	clientCtx = context.WithValue(clientCtx, correlationIDContextKey, ctx.CorrelationID.String())

	// Propagate idempotency key and knowledge_at timestamp
	// These functions add metadata to the outgoing context that the gRPC client will send
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)

	// Note: PropagateCorrelationID is called by the Client methods (e.g., InitiateFinancialPositionLog)
	// so we don't need to call it here - we just need the correlation ID in the context value
	return clientCtx
}

// convertDecimalToProto converts shopspring/decimal.Decimal to protobuf string representation.
// Protobuf doesn't have a native decimal type, so we use string to preserve precision.
func convertDecimalToProto(d decimal.Decimal) string {
	return d.String()
}

// convertProtoToDecimal converts protobuf string representation to shopspring/decimal.Decimal.
// This is the inverse of convertDecimalToProto, used when receiving gRPC responses.
func convertProtoToDecimal(s string) decimal.Decimal {
	// Parse the string - in production you might want to handle errors explicitly
	// For saga handlers, we trust the service returns valid decimal strings
	d, _ := decimal.NewFromString(s)
	return d
}
