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
	"github.com/shopspring/decimal"
	moneypb "google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func registerPostingHandlers(registry *saga.HandlerRegistry, client *Client) error {
	return registerHandlerMap(registry, map[string]starlarkHandlerEntry{
		"financial_accounting.capture_posting": {
			handler: capturePostingHandler(client),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				Description:         "Capture a single-sided posting entry within a booking log",
				Compensate:          "financial_accounting.compensate_posting",
				HasAutoCompensation: true,
				ProducesInstruments: []string{"USD", "EUR", "GBP", "NZD"},
				ProtoRequestType:    (*financialaccountingv1.CaptureLedgerPostingRequest)(nil),
				ProtoResponseType:   (*financialaccountingv1.CaptureLedgerPostingResponse)(nil),
				ParamOverrides: map[string]saga.ParamOverride{
					"amount":    {Type: "Decimal"},
					"direction": {Type: "enum"},
				},
				Version: 1,
			},
		},
		"financial_accounting.compensate_posting": {
			handler: compensatePostingHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Compensate (reverse) a captured posting entry",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				ProtoRequestType:     (*financialaccountingv1.UpdateLedgerPostingRequest)(nil),
				ProtoResponseType:    (*financialaccountingv1.UpdateLedgerPostingResponse)(nil),
				Version:              1,
			},
		},
		"financial_accounting.post_entries": {
			handler: postEntriesHandler(client),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				Description:         "Post double-entry accounting entries to the ledger",
				Compensate:          "financial_accounting.reverse_entries",
				HasAutoCompensation: true,
				ProducesInstruments: []string{"USD", "EUR", "GBP", "NZD"},
				Version:             1,
			},
		},
		"financial_accounting.reverse_entries": {
			handler: reverseEntriesHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Reverse previously posted accounting entries (compensation handler)",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				ProtoRequestType:     (*financialaccountingv1.UpdateFinancialBookingLogRequest)(nil),
				ProtoResponseType:    (*financialaccountingv1.UpdateFinancialBookingLogResponse)(nil),
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
//   - status: Always "INITIATED" for newly created logs
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
			"status": "INITIATED",
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
//   - status: Always "UPDATED"
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
			"status": "UPDATED",
		}, nil
	}
}

// capturePostingHandler creates a new ledger posting via gRPC.
// This handler captures a single posting operation in double-entry bookkeeping.
//
// Parameters:
//   - booking_log_id (string): The parent booking log identifier
//   - account_id (string): The account identifier
//   - amount (string): The amount to post (decimal string)
//   - currency (string): The currency code (e.g., "USD")
//   - direction (string): Either "DEBIT" or "CREDIT"
//
// Returns a map containing:
//   - posting_id: The unique posting identifier
//   - status: Always "POSTED" for newly created postings
func capturePostingHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		bookingLogID, err := saga.RequireStringParam(params, "booking_log_id")
		if err != nil {
			return nil, err
		}
		accountID, err := saga.RequireStringParam(params, "account_id")
		if err != nil {
			return nil, err
		}
		amountStr, err := saga.RequireStringParam(params, "amount")
		if err != nil {
			return nil, err
		}
		currencyStr, err := saga.RequireStringParam(params, "currency")
		if err != nil {
			return nil, err
		}
		directionStr, err := saga.RequireStringParam(params, "direction")
		if err != nil {
			return nil, err
		}

		// Parse amount as decimal
		amount, err := decimal.NewFromString(amountStr)
		if err != nil {
			return nil, fmt.Errorf("invalid amount: %w", err)
		}

		// Convert direction to proto enum
		var direction commonv1.PostingDirection
		switch directionStr {
		case "DEBIT":
			direction = commonv1.PostingDirection_POSTING_DIRECTION_DEBIT
		case "CREDIT":
			direction = commonv1.PostingDirection_POSTING_DIRECTION_CREDIT
		default:
			return nil, fmt.Errorf("%w: got %q", ErrInvalidDirection, directionStr)
		}

		clientCtx := prepareClientContext(ctx)

		// Build the request
		req := &financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: bookingLogID,
			AccountId:             accountID,
			PostingDirection:      direction,
			PostingAmount: &moneypb.Money{
				CurrencyCode: currencyStr,
				Units:        amount.IntPart(),
				Nanos:        int32(amount.Sub(decimal.NewFromInt(amount.IntPart())).Mul(decimal.NewFromInt(1_000_000_000)).IntPart()),
			},
			ValueDate: timestamppb.Now(),
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: ctx.IdempotencyKey,
			},
		}

		resp, err := client.CaptureLedgerPosting(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("financial_accounting.capture_posting: %w", err)
		}

		posting := resp.GetLedgerPosting()
		return map[string]any{
			"posting_id": posting.GetId(),
			"status":     "POSTED",
		}, nil
	}
}

// compensatePostingHandler reverses a captured posting during saga compensation.
// This is typically called in the compensation phase when a saga needs to rollback.
//
// Parameters:
//   - posting_id (string): The posting identifier to compensate
//
// Returns a map containing:
//   - posting_id: The posting identifier
//   - status: Always "COMPENSATED"
func compensatePostingHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		postingID, err := saga.RequireStringParam(params, "posting_id")
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)

		// Update posting to CANCELLED status as compensation
		resp, err := client.UpdateLedgerPosting(clientCtx, &financialaccountingv1.UpdateLedgerPostingRequest{
			Id:     postingID,
			Status: commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: ctx.IdempotencyKey,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("financial_accounting.compensate_posting: %w", err)
		}

		posting := resp.GetLedgerPosting()
		return map[string]any{
			"posting_id": posting.GetId(),
			"status":     "COMPENSATED",
		}, nil
	}
}

// createBookingHandler creates a new booking log (alias for initiate_booking_log).
// This handler provides an alternative naming convention for booking log creation.
//
// Parameters: Same as initiateBookingLogHandler
//
// Returns a map containing:
//   - booking_id: The unique booking log identifier (alias for log_id)
//   - status: Always "CREATED"
func createBookingHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		// Reuse initiate logic
		result, err := initiateBookingLogHandler(client)(ctx, params)
		if err != nil {
			return nil, err
		}

		// Transform response to use booking_id instead of log_id
		resultMap, ok := result.(map[string]any)
		if !ok {
			return nil, ErrInvalidResultType
		}
		return map[string]any{
			"booking_id": resultMap["log_id"],
			"status":     "CREATED",
		}, nil
	}
}

// postEntriesHandler posts multiple GL entries as a balanced journal entry.
// This handler accepts an array of entries and validates that debits equal credits
// before posting to the ledger.
//
// Parameters:
//   - booking_log_id (string): The parent booking log identifier
//   - entries ([]any): Array of entry objects, each containing:
//   - account_id (string): The account identifier
//   - amount (string): The amount (decimal string)
//   - currency (string): The currency code
//   - direction (string): Either "DEBIT" or "CREDIT"
//
// Returns a map containing:
//   - posting_ids: Array of created posting identifiers
//   - status: Always "POSTED" for successful operations
func postEntriesHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		bookingLogID, err := saga.RequireStringParam(params, "booking_log_id")
		if err != nil {
			return nil, err
		}

		// Parse entries array
		entriesArray, err := parseEntriesArray(params)
		if err != nil {
			return nil, err
		}

		// Validate balanced entries (debits = credits)
		if err := validateBalancedEntries(entriesArray); err != nil {
			return nil, err
		}

		// Post each entry
		postingIDs, err := postEntries(ctx, client, bookingLogID, entriesArray)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"posting_ids": postingIDs,
			"status":      "POSTED",
		}, nil
	}
}

// parseEntriesArray extracts and validates the entries array from params.
func parseEntriesArray(params map[string]any) ([]any, error) {
	entriesRaw, ok := params["entries"]
	if !ok {
		return nil, ErrMissingEntriesParam
	}

	entriesArray, ok := entriesRaw.([]any)
	if !ok {
		return nil, ErrEntriesMustBeArray
	}

	return entriesArray, nil
}

// validateBalancedEntries checks that total debits equal total credits.
func validateBalancedEntries(entriesArray []any) error {
	var totalDebits, totalCredits decimal.Decimal

	for _, entryRaw := range entriesArray {
		entryMap, ok := entryRaw.(map[string]any)
		if !ok {
			return ErrEntryMustBeObject
		}

		amountStr, ok := entryMap["amount"].(string)
		if !ok {
			return ErrEntryAmountMustBeString
		}

		amount, err := decimal.NewFromString(amountStr)
		if err != nil {
			return fmt.Errorf("invalid entry amount: %w", err)
		}

		direction, ok := entryMap["direction"].(string)
		if !ok {
			return ErrEntryDirectionMustBeString
		}

		switch direction {
		case "DEBIT":
			totalDebits = totalDebits.Add(amount)
		case "CREDIT":
			totalCredits = totalCredits.Add(amount)
		default:
			return fmt.Errorf("%w: got %q", ErrInvalidDirection, direction)
		}
	}

	// Validate balanced journal
	if !totalDebits.Equal(totalCredits) {
		return fmt.Errorf("%w: debits=%s, credits=%s", ErrUnbalancedJournal, totalDebits.String(), totalCredits.String())
	}

	return nil
}

// postEntries posts all entries and returns their posting IDs.
func postEntries(ctx *saga.StarlarkContext, client *Client, bookingLogID string, entriesArray []any) ([]string, error) {
	postingIDs := make([]string, 0, len(entriesArray))

	for i, entryRaw := range entriesArray {
		entryMap, ok := entryRaw.(map[string]any)
		if !ok {
			return nil, ErrEntryMustBeObject
		}

		// Create posting params
		postingParams := map[string]any{
			"booking_log_id": bookingLogID,
			"account_id":     entryMap["account_id"],
			"amount":         entryMap["amount"],
			"currency":       entryMap["currency"],
			"direction":      entryMap["direction"],
		}

		// Create context with unique idempotency key per entry
		// Note: We create a new StarlarkContext to avoid copying the mutex in the original context
		entryCtx := &saga.StarlarkContext{
			Context:           ctx.Context,
			PartyScope:        ctx.PartyScope,
			SagaExecutionID:   ctx.SagaExecutionID,
			CorrelationID:     ctx.CorrelationID,
			KnowledgeAt:       ctx.KnowledgeAt,
			IdempotencyKey:    fmt.Sprintf("%s_entry_%d", ctx.IdempotencyKey, i),
			Logger:            ctx.Logger,
			LookupCache:       ctx.LookupCache,
			TriggerInstrument: ctx.TriggerInstrument,
		}

		// Call capture_posting handler
		result, err := capturePostingHandler(client)(entryCtx, postingParams)
		if err != nil {
			return nil, fmt.Errorf("failed to post entry: %w", err)
		}

		resultMap, ok := result.(map[string]any)
		if !ok {
			return nil, ErrInvalidResultType
		}

		postingID, ok := resultMap["posting_id"].(string)
		if !ok {
			return nil, ErrInvalidPostingIDType
		}

		postingIDs = append(postingIDs, postingID)
	}

	return postingIDs, nil
}

// reverseEntriesHandler reverses posted GL entries during saga compensation.
// This is a critical compensation handler for saga rollback.
//
// Implementation Note: This handler cancels the booking log to prevent its entries
// from being finalized. An alternative approach would be to create offsetting
// entries (a new booking log with type=REVERSAL), but cancellation is simpler
// for compensation and maintains audit trail through the CANCELLED status.
// The choice depends on the Financial Accounting service's reversal semantics.
//
// Parameters:
//   - booking_log_id (string): The booking log identifier containing entries to reverse
//
// Returns a map containing:
//   - log_id: The booking log identifier
//   - status: Always "REVERSED"
func reverseEntriesHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		bookingLogID, err := saga.RequireStringParam(params, "booking_log_id")
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)

		// Cancel the booking log to reverse its entries
		resp, err := client.UpdateFinancialBookingLog(clientCtx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
			Id:     bookingLogID,
			Status: commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: ctx.IdempotencyKey,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("financial_accounting.reverse_entries: %w", err)
		}

		log := resp.GetFinancialBookingLog()
		return map[string]any{
			"log_id": log.GetId(),
			"status": "REVERSED",
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
