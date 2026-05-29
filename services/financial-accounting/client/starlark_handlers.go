// Package client - Starlark posting handlers (capture, post-entries, compensation/reversal).
package client

import (
	"fmt"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
//   - status: The server-reported status of the posting (e.g. TRANSACTION_STATUS_PENDING)
func capturePostingHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		req, err := buildCapturePostingRequest(ctx, params)
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)
		resp, err := client.CaptureLedgerPosting(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("financial_accounting.capture_posting: %w", err)
		}

		posting := resp.GetLedgerPosting()
		return map[string]any{
			"posting_id": posting.GetId(),
			"status":     posting.GetStatus().String(),
		}, nil
	}
}

// buildCapturePostingRequest parses Starlark parameters and builds a CaptureLedgerPostingRequest.
func buildCapturePostingRequest(ctx *saga.StarlarkContext, params map[string]any) (*financialaccountingv1.CaptureLedgerPostingRequest, error) {
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

	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return nil, fmt.Errorf("invalid amount: %w", err)
	}

	direction, err := parsePostingDirection(directionStr)
	if err != nil {
		return nil, err
	}

	return &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		AccountId:             accountID,
		PostingDirection:      direction,
		PostingAmount: &quantityv1.InstrumentAmount{
			Amount:         amount.String(),
			InstrumentCode: currencyStr,
			Version:        1,
		},
		ValueDate: timestamppb.Now(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: ctx.IdempotencyKey,
		},
	}, nil
}

// parsePostingDirection converts a string direction to the proto PostingDirection enum.
func parsePostingDirection(directionStr string) (commonv1.PostingDirection, error) {
	switch directionStr {
	case "DEBIT":
		return commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, nil
	case "CREDIT":
		return commonv1.PostingDirection_POSTING_DIRECTION_CREDIT, nil
	default:
		return 0, fmt.Errorf("%w: got %q", ErrInvalidDirection, directionStr)
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
//   - status: The server-reported status after compensation (e.g. TRANSACTION_STATUS_CANCELLED)
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
			"status":     posting.GetStatus().String(),
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
//   - status: The server-reported status propagated from initiate_booking_log
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
			"status":     resultMap["status"],
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
//   - statuses: Array of server-reported posting statuses, parallel to posting_ids
//   - status: Aggregate status - the common posting status when all entries agree,
//     otherwise TRANSACTION_STATUS_UNSPECIFIED (callers should inspect `statuses`)
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
		postingIDs, statuses, err := postEntries(ctx, client, bookingLogID, entriesArray)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"posting_ids": postingIDs,
			"statuses":    statuses,
			"status":      aggregatePostingStatus(statuses),
		}, nil
	}
}

// aggregatePostingStatus returns the common status across all postings when they
// agree. An empty batch or divergent statuses yield TRANSACTION_STATUS_UNSPECIFIED,
// signaling that callers should inspect the per-entry `statuses` array. The value
// is always derived from real server-reported statuses - never a hardcoded literal.
func aggregatePostingStatus(statuses []string) string {
	if len(statuses) == 0 {
		return commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED.String()
	}
	first := statuses[0]
	for _, s := range statuses[1:] {
		if s != first {
			return commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED.String()
		}
	}
	return first
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

// postEntries posts all entries and returns their posting IDs together with the
// server-reported status for each posting (parallel slices).
func postEntries(ctx *saga.StarlarkContext, client *Client, bookingLogID string, entriesArray []any) ([]string, []string, error) {
	postingIDs := make([]string, 0, len(entriesArray))
	statuses := make([]string, 0, len(entriesArray))

	for i, entryRaw := range entriesArray {
		entryMap, ok := entryRaw.(map[string]any)
		if !ok {
			return nil, nil, ErrEntryMustBeObject
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
			return nil, nil, fmt.Errorf("failed to post entry: %w", err)
		}

		resultMap, ok := result.(map[string]any)
		if !ok {
			return nil, nil, ErrInvalidResultType
		}

		postingID, ok := resultMap["posting_id"].(string)
		if !ok {
			return nil, nil, ErrInvalidPostingIDType
		}

		postingStatus, ok := resultMap["status"].(string)
		if !ok {
			return nil, nil, ErrInvalidStatusType
		}

		postingIDs = append(postingIDs, postingID)
		statuses = append(statuses, postingStatus)
	}

	return postingIDs, statuses, nil
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
//   - status: The server-reported status after reversal (e.g. TRANSACTION_STATUS_CANCELLED)
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
			"status": log.GetStatus().String(),
		}, nil
	}
}
