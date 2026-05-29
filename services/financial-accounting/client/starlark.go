// Package client provides Starlark service bindings for Financial Accounting.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga step execution with real Financial Accounting service integration.
package client

import (
	"context"
	"errors"
	"fmt"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// Handler errors.
var (
	// ErrInvalidDirection is returned when direction is not DEBIT or CREDIT.
	ErrInvalidDirection = errors.New("direction must be DEBIT or CREDIT")

	// ErrEntriesMustBeArray is returned when entries parameter is not an array.
	ErrEntriesMustBeArray = errors.New("entries must be array")

	// ErrEntryMustBeObject is returned when an entry is not an object.
	ErrEntryMustBeObject = errors.New("each entry must be an object")

	// ErrEntryAmountMustBeString is returned when entry amount is not a string.
	ErrEntryAmountMustBeString = errors.New("entry amount must be string")

	// ErrEntryDirectionMustBeString is returned when entry direction is not a string.
	ErrEntryDirectionMustBeString = errors.New("entry direction must be string")

	// ErrUnbalancedJournal is returned when debits don't equal credits.
	ErrUnbalancedJournal = errors.New("unbalanced journal entries")

	// ErrMissingEntriesParam is returned when entries parameter is missing.
	ErrMissingEntriesParam = errors.New("missing required parameter: entries")

	// ErrInvalidResultType is returned when a handler result has unexpected type.
	ErrInvalidResultType = errors.New("invalid result type")

	// ErrInvalidPostingIDType is returned when posting_id has unexpected type.
	ErrInvalidPostingIDType = errors.New("invalid posting_id type")

	// ErrInvalidStatusType is returned when a handler result status has unexpected type.
	ErrInvalidStatusType = errors.New("invalid status type")

	// ErrInvalidStatus is returned when an unknown status value is provided.
	ErrInvalidStatus = errors.New("invalid status")
)

// RegisterStarlarkHandlers registers all Starlark service bindings for Financial Accounting.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
//
// This function is called during service initialization to register Financial Accounting handlers
// with the saga execution engine. Each handler includes metadata for conservation rule
// enforcement and operational categorization.
//
// Category: CategorySettlement - Financial Accounting creates Money instruments (USD, EUR, etc.)
// rather than Physics instruments (KWH, GAS). It settles financial obligations through
// double-entry bookkeeping and ledger postings.
//
// Example usage:
//
//	registry := saga.NewHandlerRegistry()
//	client, cleanup, _ := client.New(client.Config{...})
//	defer cleanup()
//	err := RegisterStarlarkHandlers(registry, client)
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, client *Client) error {
	if err := registerBookingLogHandlers(registry, client); err != nil {
		return err
	}
	return registerPostingHandlers(registry, client)
}

type starlarkHandlerEntry struct {
	handler  saga.Handler
	metadata saga.HandlerMetadata
}

func registerHandlerMap(registry *saga.HandlerRegistry, handlers map[string]starlarkHandlerEntry) error {
	for name, h := range handlers {
		if err := registry.RegisterWithMetadata(name, h.handler, &h.metadata); err != nil {
			return fmt.Errorf("failed to register %s: %w", name, err)
		}
	}
	return nil
}

func registerBookingLogHandlers(registry *saga.HandlerRegistry, client *Client) error {
	return registerHandlerMap(registry, map[string]starlarkHandlerEntry{
		"financial_accounting.initiate_booking_log": {
			handler: initiateBookingLogHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Initiate a booking log for a deposit or withdrawal transaction",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{"USD", "EUR", "GBP", "NZD"},
				ProtoRequestType:     (*financialaccountingv1.InitiateFinancialBookingLogRequest)(nil),
				ProtoResponseType:    (*financialaccountingv1.InitiateFinancialBookingLogResponse)(nil),
				Version:              1,
			},
		},
		"financial_accounting.update_booking_log": {
			handler: updateBookingLogHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Update the status of an existing booking log",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				ProtoRequestType:     (*financialaccountingv1.UpdateFinancialBookingLogRequest)(nil),
				ProtoResponseType:    (*financialaccountingv1.UpdateFinancialBookingLogResponse)(nil),
				Version:              1,
			},
		},
		"financial_accounting.create_booking": {
			handler: createBookingHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Create a booking log entry for audit purposes",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{"USD", "EUR", "GBP", "NZD"},
				ProtoRequestType:     (*financialaccountingv1.InitiateFinancialBookingLogRequest)(nil),
				ProtoResponseType:    (*financialaccountingv1.InitiateFinancialBookingLogResponse)(nil),
				Version:              1,
			},
		},
	})
}

// initiateBookingLogHandler creates a new financial booking log via gRPC.
// This handler adapts Starlark parameters to the InitiateFinancialBookingLog RPC call,
// propagating saga metadata for idempotency, tracing, and bi-temporal queries.
//
// Parameters:
//   - product_service_reference (string): The financial product identifier
//   - business_unit_reference (string): The business unit identifier
//   - chart_of_accounts_rules (string): The accounting rules to apply
//
// Returns a map containing:
//   - log_id: The unique booking log identifier
//   - status: The server-reported status of the booking log (e.g. TRANSACTION_STATUS_PENDING)
func initiateBookingLogHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		// 1. Parse Starlark params using helper functions from shared/pkg/saga
		productRef, err := saga.RequireStringParam(params, "product_service_reference")
		if err != nil {
			return nil, err
		}
		businessUnit, err := saga.RequireStringParam(params, "business_unit_reference")
		if err != nil {
			return nil, err
		}
		chartRules, err := saga.RequireStringParam(params, "chart_of_accounts_rules")
		if err != nil {
			return nil, err
		}

		// 2. Prepare client context with saga metadata propagation
		clientCtx := prepareClientContext(ctx)

		// 3. Build the request
		req := &financialaccountingv1.InitiateFinancialBookingLogRequest{
			ProductServiceReference: productRef,
			BusinessUnitReference:   businessUnit,
			ChartOfAccountsRules:    chartRules,
			FinancialAccountType:    "CURRENT", // Default
			BaseInstrumentCode:      "USD",     // Default
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: ctx.IdempotencyKey,
			},
		}

		// 4. Call REAL gRPC client
		resp, err := client.InitiateFinancialBookingLog(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("financial_accounting.initiate_booking_log: %w", err)
		}

		// 5. Convert response to Starlark format (map[string]any)
		log := resp.GetFinancialBookingLog()
		return map[string]any{
			"log_id": log.GetId(),
			"status": log.GetStatus().String(),
		}, nil
	}
}

// updateBookingLogHandler updates an existing financial booking log status.
// This handler is used during saga operations or status transitions.
//
// Parameters:
//   - log_id (string): The booking log identifier to update
//   - status (string): The new status (e.g., "POSTED", "CANCELLED") - optional
//
// Returns a map containing:
//   - log_id: The booking log identifier
//   - status: The server-reported status of the booking log after the update
func updateBookingLogHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		logID, err := saga.RequireStringParam(params, "log_id")
		if err != nil {
			return nil, err
		}

		// Parse optional status parameter
		status := commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING
		if statusStr, ok := params["status"].(string); ok {
			switch statusStr {
			case "POSTED":
				status = commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED
			case "CANCELLED":
				status = commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED
			case "PENDING":
				status = commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING
			default:
				return nil, fmt.Errorf("%w: %s", ErrInvalidStatus, statusStr)
			}
		}

		clientCtx := prepareClientContext(ctx)
		resp, err := client.UpdateFinancialBookingLog(clientCtx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
			Id:     logID,
			Status: status,
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: ctx.IdempotencyKey,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("financial_accounting.update_booking_log: %w", err)
		}

		log := resp.GetFinancialBookingLog()
		return map[string]any{
			"log_id": log.GetId(),
			"status": log.GetStatus().String(),
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
type contextKey string

const correlationIDContextKey contextKey = "x-correlation-id"

func prepareClientContext(ctx *saga.StarlarkContext) context.Context {
	clientCtx := ctx.Context
	if clientCtx == nil {
		clientCtx = context.Background()
	}

	// Add correlation ID to context value so the client's PropagateCorrelationID can extract it
	clientCtx = context.WithValue(clientCtx, correlationIDContextKey, ctx.CorrelationID.String())

	// Propagate idempotency key and knowledge_at timestamp
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)

	return clientCtx
}
