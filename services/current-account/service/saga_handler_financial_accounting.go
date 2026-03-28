package service

import (
	"fmt"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// currentAccountFinAcctInitiateBookingLog creates a financial booking log.
//
// Required params:
//   - account_id: string - The account identifier
//   - instrument_code: string - Instrument code (e.g., "GBP", "kWh"). Replaces currency.
//   - currency: string - Deprecated: use instrument_code instead.
//   - transaction_id: string - The saga transaction ID
//   - transaction_type: string - "WITHDRAWAL" or "DEPOSIT"
func currentAccountFinAcctInitiateBookingLog(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.financial_accounting.initiate_booking_log"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Extract and validate parameters
	accountID, currency, transactionID, transactionType, err := validateBookingLogParams(params)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	if deps.FinAcctClient == nil {
		return nil, wrapHandlerError(handlerName, errFinAcctClientNil)
	}

	deps.Logger.Info("executing financial_accounting.initiate_booking_log",
		"account_id", accountID,
		"transaction_id", transactionID,
		"transaction_type", transactionType)

	resp, err := deps.FinAcctClient.InitiateFinancialBookingLog(ctx,
		&financialaccountingv1.InitiateFinancialBookingLogRequest{
			FinancialAccountType:    "CURRENT",
			ProductServiceReference: accountID,
			BusinessUnitReference:   "current-account-service",
			ChartOfAccountsRules:    transactionType,
			BaseInstrumentCode:      currency,
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("booking-log-%s", transactionID),
			},
		},
	)
	if err != nil {
		caobservability.RecordExternalServiceError("financial_accounting", "initiate_booking_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to initiate booking log: %w", err))
	}
	if resp.FinancialBookingLog == nil {
		caobservability.RecordExternalServiceError("financial_accounting", "initiate_booking_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: transaction %s", errNilBookingLog, transactionID))
	}

	deps.Logger.Info("financial_accounting.initiate_booking_log completed",
		"booking_log_id", resp.FinancialBookingLog.Id,
		"transaction_id", transactionID)

	return map[string]any{
		"booking_log_id": resp.FinancialBookingLog.Id,
		"status":         "CREATED",
	}, nil
}

// validateBookingLogParams extracts and validates parameters for initiate_booking_log.
func validateBookingLogParams(params map[string]any) (string, string, string, string, error) {
	accountID, err := requireString(params, "account_id")
	if err != nil {
		return "", "", "", "", err
	}
	currency := optionalString(params, "instrument_code")
	if currency == "" {
		currency = optionalString(params, "currency")
	}
	if currency == "" {
		return "", "", "", "", fmt.Errorf("%w: instrument_code", errMissingParameter)
	}
	transactionID, err := requireString(params, "transaction_id")
	if err != nil {
		return "", "", "", "", err
	}
	transactionType, err := requireString(params, "transaction_type")
	if err != nil {
		return "", "", "", "", err
	}
	return accountID, currency, transactionID, transactionType, nil
}

// currentAccountFinAcctCapturePosting captures a ledger posting (debit or credit).
//
// Required params:
//   - booking_log_id: string - The booking log ID
//   - account_id: string - The account to post to
//   - amount: decimal.Decimal - The posting amount
//   - instrument_code: string - Instrument code (e.g., "GBP", "kWh"). Replaces currency.
//   - currency: string - Deprecated: use instrument_code instead.
//   - direction: string - "DEBIT" or "CREDIT"
//   - transaction_id: string - The saga transaction ID
//   - posting_type: string - "debit" or "credit" (for idempotency key suffix)
func currentAccountFinAcctCapturePosting(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.financial_accounting.capture_posting"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Extract and validate posting parameters
	posting, err := validatePostingParams(params)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	if deps.FinAcctClient == nil {
		return nil, wrapHandlerError(handlerName, errFinAcctClientNil)
	}

	deps.Logger.Info("executing financial_accounting.capture_posting",
		"booking_log_id", posting.bookingLogID,
		"account_id", posting.accountID,
		"direction", posting.direction,
		"posting_type", posting.postingType)

	protoAmount := decimalToMoneyAmount(posting.amount, posting.currency)

	resp, err := deps.FinAcctClient.CaptureLedgerPosting(ctx,
		&financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: posting.bookingLogID,
			PostingDirection:      posting.pbDirection,
			PostingAmount:         protoAmount.Amount,
			AccountId:             posting.accountID,
			ValueDate:             timestamppb.Now(),
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("%s-%s", posting.transactionID, posting.postingType),
			},
		},
	)
	if err != nil {
		caobservability.RecordExternalServiceError("financial_accounting", "capture_"+posting.postingType+"_posting")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to capture %s posting: %w", posting.postingType, err))
	}
	if resp.LedgerPosting == nil {
		caobservability.RecordExternalServiceError("financial_accounting", "capture_"+posting.postingType+"_posting")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s posting for transaction %s", errNilPosting, posting.postingType, posting.transactionID))
	}

	deps.Logger.Info("financial_accounting.capture_posting completed",
		"posting_id", resp.LedgerPosting.Id,
		"posting_type", posting.postingType,
		"transaction_id", posting.transactionID)

	return map[string]any{
		"posting_id": resp.LedgerPosting.Id,
		"status":     "POSTED",
	}, nil
}

// postingParams holds validated parameters for a ledger posting operation.
type postingParams struct {
	bookingLogID  string
	accountID     string
	amount        decimal.Decimal
	currency      string
	direction     string
	pbDirection   commonpb.PostingDirection
	transactionID string
	postingType   string
}

// validatePostingParams extracts and validates common parameters for capture/compensate posting.
func validatePostingParams(params map[string]any) (*postingParams, error) {
	bookingLogID, err := requireString(params, "booking_log_id")
	if err != nil {
		return nil, err
	}
	accountID, err := requireString(params, "account_id")
	if err != nil {
		return nil, err
	}
	amount, err := requireDecimal(params, "amount")
	if err != nil {
		return nil, err
	}
	currency := optionalString(params, "instrument_code")
	if currency == "" {
		currency = optionalString(params, "currency")
	}
	if currency == "" {
		return nil, fmt.Errorf("%w: instrument_code", errMissingParameter)
	}
	direction, err := requireString(params, "direction")
	if err != nil {
		return nil, err
	}
	transactionID, err := requireString(params, "transaction_id")
	if err != nil {
		return nil, err
	}
	postingType, err := requireString(params, "posting_type")
	if err != nil {
		return nil, err
	}

	var pbDirection commonpb.PostingDirection
	switch direction {
	case directionDebit:
		pbDirection = commonpb.PostingDirection_POSTING_DIRECTION_DEBIT
	case directionCredit:
		pbDirection = commonpb.PostingDirection_POSTING_DIRECTION_CREDIT
	default:
		return nil, fmt.Errorf("%w: %s", errInvalidDirection, direction)
	}

	return &postingParams{
		bookingLogID:  bookingLogID,
		accountID:     accountID,
		amount:        amount,
		currency:      currency,
		direction:     direction,
		pbDirection:   pbDirection,
		transactionID: transactionID,
		postingType:   postingType,
	}, nil
}

// currentAccountFinAcctUpdateBookingLog updates a booking log status (typically to POSTED).
//
// Required params:
//   - booking_log_id: string - The booking log ID
//   - status: string - The new status (e.g., "POSTED", "CANCELLED")
func currentAccountFinAcctUpdateBookingLog(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.financial_accounting.update_booking_log"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	bookingLogID, err := requireString(params, "booking_log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	statusStr, err := requireString(params, "status")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	var pbStatus commonpb.TransactionStatus
	switch statusStr {
	case "POSTED":
		pbStatus = commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED
	case "CANCELLED":
		pbStatus = commonpb.TransactionStatus_TRANSACTION_STATUS_CANCELLED
	default:
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s", errInvalidStatus, statusStr))
	}

	if deps.FinAcctClient == nil {
		return nil, wrapHandlerError(handlerName, errFinAcctClientNil)
	}

	deps.Logger.Info("executing financial_accounting.update_booking_log",
		"booking_log_id", bookingLogID,
		"status", statusStr)

	_, err = deps.FinAcctClient.UpdateFinancialBookingLog(ctx,
		&financialaccountingv1.UpdateFinancialBookingLogRequest{
			Id:     bookingLogID,
			Status: pbStatus,
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("update-booking-log-%s-%s", bookingLogID, statusStr),
			},
		},
	)
	if err != nil {
		caobservability.RecordExternalServiceError("financial_accounting", "update_booking_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to update booking log: %w", err))
	}

	deps.Logger.Info("financial_accounting.update_booking_log completed",
		"booking_log_id", bookingLogID,
		"status", statusStr)

	return map[string]any{
		"booking_log_id": bookingLogID,
		"status":         statusStr,
	}, nil
}

// currentAccountFinAcctCompensatePosting creates a compensating posting entry.
//
// Required params:
//   - booking_log_id: string - The booking log ID
//   - account_id: string - The account to post to
//   - amount: decimal.Decimal - The posting amount
//   - instrument_code: string - Instrument code (e.g., "GBP", "kWh"). Replaces currency.
//   - currency: string - Deprecated: use instrument_code instead.
//   - direction: string - "DEBIT" or "CREDIT" (opposite of original)
//   - transaction_id: string - The saga transaction ID
//   - posting_type: string - "debit" or "credit" (original posting type being compensated)
func currentAccountFinAcctCompensatePosting(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.financial_accounting.compensate_posting"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Reuse the same parameter validation as capture posting
	posting, err := validatePostingParams(params)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	if deps.FinAcctClient == nil {
		return nil, wrapHandlerError(handlerName, errFinAcctClientNil)
	}

	deps.Logger.Info("executing financial_accounting.compensate_posting",
		"booking_log_id", posting.bookingLogID,
		"account_id", posting.accountID,
		"direction", posting.direction,
		"posting_type", posting.postingType)

	protoAmount := decimalToMoneyAmount(posting.amount, posting.currency)

	_, err = deps.FinAcctClient.CaptureLedgerPosting(ctx,
		&financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: posting.bookingLogID,
			PostingDirection:      posting.pbDirection,
			PostingAmount:         protoAmount.Amount,
			AccountId:             posting.accountID,
			ValueDate:             timestamppb.Now(),
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("COMP-%s-%s", posting.transactionID, posting.postingType),
			},
		},
	)
	if err != nil {
		// CRITICAL: Manual intervention required - ledger may be inconsistent
		deps.Logger.Error("CRITICAL: failed to compensate posting - manual ledger reconciliation required",
			"booking_log_id", posting.bookingLogID,
			"account_id", posting.accountID,
			"transaction_id", posting.transactionID,
			"error", err,
			"runbook", "docs/runbooks/saga-failure-recovery.md")
		caobservability.RecordInlineCompensationFailure("current_account", posting.postingType)
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to compensate %s posting: %w", posting.postingType, err))
	}

	deps.Logger.Info("financial_accounting.compensate_posting completed",
		"booking_log_id", posting.bookingLogID,
		"posting_type", posting.postingType)

	return map[string]any{
		"status": "COMPENSATED",
	}, nil
}
