package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PostLedgerEntries creates double-entry bookkeeping entries for a completed payment.
// It creates a BookingLog in PENDING status, captures debit and credit postings, then
// updates the BookingLog to POSTED status. Returns the booking log ID on success.
//
// Double-entry accounting supports two flows:
//
// Standard Flow (2 postings):
//   - DEBIT: Customer's account (funds leaving their account)
//   - CREDIT: Gateway's contra-account (liability to payment processor)
//
// Internal Clearing Flow (4 postings) - when internalClearingEnabled and clearing account resolved:
//   - DEBIT: Customer's account (funds leaving their account)
//   - CREDIT: Clearing account (funds enter internal clearing)
//   - DEBIT: Clearing account (funds leave internal clearing)
//   - CREDIT: Gateway's contra-account (liability to payment processor)
//
// The 4-posting flow maintains double-entry balance while routing through the internal
// clearing account, enabling enhanced reconciliation and settlement tracking.
//
// Atomicity considerations:
// Standard flow makes 4 sequential gRPC calls, clearing flow makes 6:
//  1. InitiateFinancialBookingLog (creates BookingLog in PENDING)
//  2. CaptureLedgerPosting (DEBIT customer)
//  3. CaptureLedgerPosting (CREDIT clearing) - only in clearing flow
//  4. CaptureLedgerPosting (DEBIT clearing) - only in clearing flow
//  5. CaptureLedgerPosting (CREDIT gateway)
//  6. UpdateFinancialBookingLog (marks as POSTED)
//
// Partial failure scenarios (documented for operational runbooks):
//   - Step 1 fails: No orphaned state - safe to retry
//   - Posting step fails: BookingLog in PENDING, unbalanced - needs reversal/cleanup
//   - Final status update fails: BookingLog in PENDING, balanced entries exist - just needs status update
//
// All partial failures are logged with RECONCILIATION_REQUIRED prefix and include
// the booking_log_id for manual resolution. See runbook: docs/runbooks/saga-failure-recovery.md
//
// Error handling: If any step fails, the error is returned and the calling code
// should mark the payment as FAILED. The BookingLog will remain in PENDING status
// for reconciliation purposes.
//
// Clearing account fallback: If internal clearing is enabled but the clearing account
// lookup fails, the method falls back to the standard 2-posting flow gracefully.
func (o *PaymentOrchestrator) PostLedgerEntries(ctx context.Context, po *domain.PaymentOrder) (string, error) {
	// Check required dependencies - these may be nil in minimal test configuration
	if o.gatewayAccountConfig == nil {
		return "", ErrGatewayAccountConfigNotSet
	}
	if o.financialAccountingClient == nil {
		return "", ErrFinancialAccountingClientNotSet
	}

	// Get the gateway contra-account from configuration
	// Extract gateway ID from the GatewayReferenceID prefix (e.g., "GW-uuid" -> "mock" for mock gateway)
	gatewayID := extractGatewayIDFromRef(po.GatewayReferenceID)
	contraAccountID, err := o.gatewayAccountConfig.GetContraAccount(gatewayID)
	if err != nil {
		return "", fmt.Errorf("failed to get contra-account for gateway %s: %w", gatewayID, err)
	}

	// Extract instrument code from domain amount
	currencyCode := domain.CurrencyCode(po.Amount)
	if currencyCode == "" {
		o.logger.Warn("unsupported currency for ledger posting - payment will be marked as failed",
			"currency", currencyCode,
			"payment_order_id", po.ID.String(),
			"supported_currencies", "GBP, USD, EUR")
		return "", fmt.Errorf("%w: %s", ErrUnsupportedCurrency, currencyCode)
	}

	// Step 1: Create a BookingLog in PENDING status
	bookingLogIDempKey := fmt.Sprintf("booking-log-%s", po.IdempotencyKey)
	bookingLogResp, err := o.financialAccountingClient.InitiateFinancialBookingLog(ctx, &financialaccountingv1.InitiateFinancialBookingLogRequest{
		FinancialAccountType:    "CURRENT",
		ProductServiceReference: "payment-order",
		BusinessUnitReference:   "payment-order-service",
		ChartOfAccountsRules:    "outbound-payment",
		BaseInstrumentCode:      currencyCode,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: bookingLogIDempKey,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create booking log: %w", err)
	}
	if bookingLogResp.FinancialBookingLog == nil {
		return "", fmt.Errorf("%w: payment order %s", ErrNilBookingLogResponse, po.ID.String())
	}
	bookingLogID := bookingLogResp.FinancialBookingLog.Id

	o.logger.Debug("created booking log for ledger posting",
		"booking_log_id", bookingLogID,
		"payment_order_id", po.ID.String())

	// Convert amount from cents to google.type.Money format.
	// google.type.Money uses Units (whole currency units) + Nanos (10^-9 fraction).
	// Example: 199 cents = 1 unit + 990,000,000 nanos = 1.99 currency units.
	// Formula: Units = cents / 100, Nanos = (cents % 100) * 10,000,000
	amountCents := domain.ToMinorUnits(po.Amount)
	postingAmount := &money.Money{
		CurrencyCode: currencyCode,
		Units:        amountCents / 100,
		Nanos:        int32((amountCents % 100) * 10000000),
	}
	valueDate := timestamppb.Now()

	// Determine if we should use the 4-posting flow with internal clearing
	var clearingAccountID string
	useClearingFlow := false

	if o.internalClearingEnabled && o.accountResolver != nil {
		var resolveErr error
		clearingAccountID, resolveErr = o.accountResolver.GetSettlementClearingAccount(ctx, currencyCode)
		if resolveErr != nil {
			// Log the fallback but continue with standard 2-posting flow
			o.logger.Info("clearing account lookup failed, falling back to standard posting flow",
				"payment_order_id", po.ID.String(),
				"currency", currencyCode,
				"reason", resolveErr.Error())
		} else {
			useClearingFlow = true
			o.logger.Info("using internal clearing flow with 4 postings",
				"payment_order_id", po.ID.String(),
				"clearing_account_id", clearingAccountID,
				"currency", currencyCode)
		}
	} else if o.internalClearingEnabled {
		o.logger.Debug("internal clearing enabled but account resolver not configured, using standard flow",
			"payment_order_id", po.ID.String())
	}

	if useClearingFlow {
		// 4-posting flow: Customer DEBIT -> Clearing CREDIT -> Clearing DEBIT -> Gateway CREDIT
		return o.postLedgerEntriesWithClearing(ctx, po, bookingLogID, postingAmount, valueDate,
			clearingAccountID, contraAccountID, amountCents, currencyCode)
	}

	// Standard 2-posting flow: Customer DEBIT -> Gateway CREDIT
	return o.postLedgerEntriesStandard(ctx, po, bookingLogID, postingAmount, valueDate,
		contraAccountID, amountCents, currencyCode)
}

// postLedgerEntriesStandard creates the standard 2-posting flow for ledger entries.
// Posts: Customer DEBIT -> Gateway CREDIT
func (o *PaymentOrchestrator) postLedgerEntriesStandard(
	ctx context.Context,
	po *domain.PaymentOrder,
	bookingLogID string,
	postingAmount *money.Money,
	valueDate *timestamppb.Timestamp,
	contraAccountID string,
	amountCents int64,
	currencyCode string,
) (string, error) {
	// Step 2: Create DEBIT posting (customer account - funds leaving)
	debitIdempKey := fmt.Sprintf("debit-customer-%s", po.IdempotencyKey)
	_, err := o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount:         postingAmount,
		AccountId:             po.DebtorAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: debitIdempKey,
		},
	})
	if err != nil {
		// RECONCILIATION: BookingLog created but debit posting failed - requires manual cleanup
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after debit posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "debit_customer_posting",
			"posting_flow", "standard",
			"debtor_account", po.DebtorAccountID,
			"error", err.Error())
		return "", fmt.Errorf("failed to create debit posting for account %s: %w", po.DebtorAccountID, err)
	}

	o.logger.Debug("created debit posting (customer)",
		"booking_log_id", bookingLogID,
		"account_id", po.DebtorAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 3: Create CREDIT posting (gateway contra-account - liability to processor)
	creditIdempKey := fmt.Sprintf("credit-gateway-%s", po.IdempotencyKey)
	_, err = o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
		PostingAmount:         postingAmount,
		AccountId:             contraAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: creditIdempKey,
		},
	})
	if err != nil {
		// RECONCILIATION: BookingLog has debit but no credit - unbalanced ledger requires cleanup
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after credit posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "credit_gateway_posting",
			"posting_flow", "standard",
			"debtor_account", po.DebtorAccountID,
			"contra_account", contraAccountID,
			"has_debit_customer_posting", true,
			"error", err.Error())
		return "", fmt.Errorf("failed to create credit posting for account %s: %w", contraAccountID, err)
	}

	o.logger.Debug("created credit posting (gateway)",
		"booking_log_id", bookingLogID,
		"account_id", contraAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 4: Update BookingLog status to POSTED (balanced entries are now complete)
	_, err = o.financialAccountingClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID,
		Status: commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED,
	})
	if err != nil {
		// RECONCILIATION: BookingLog has balanced entries but status update failed
		// The ledger entries exist and are balanced - just need status update
		o.logger.Error("RECONCILIATION_REQUIRED: booking log status update failed after successful postings",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"target_status", "POSTED",
			"payment_order_id", po.ID.String(),
			"failed_step", "status_update",
			"posting_flow", "standard",
			"has_debit_customer_posting", true,
			"has_credit_gateway_posting", true,
			"resolution", "manually update booking log status to POSTED",
			"error", err.Error())
		return "", fmt.Errorf("failed to update booking log to POSTED: %w", err)
	}

	o.logger.Info("ledger posting completed successfully (standard flow)",
		"booking_log_id", bookingLogID,
		"payment_order_id", po.ID.String(),
		"debtor_account", po.DebtorAccountID,
		"contra_account", contraAccountID,
		"posting_count", 2,
		"amount_cents", amountCents,
		"currency", currencyCode)

	return bookingLogID, nil
}

// postLedgerEntriesWithClearing creates the 4-posting flow for ledger entries with internal clearing.
// Posts: Customer DEBIT -> Clearing CREDIT -> Clearing DEBIT -> Gateway CREDIT
// This maintains double-entry balance while routing through the internal clearing account.
func (o *PaymentOrchestrator) postLedgerEntriesWithClearing(
	ctx context.Context,
	po *domain.PaymentOrder,
	bookingLogID string,
	postingAmount *money.Money,
	valueDate *timestamppb.Timestamp,
	clearingAccountID string,
	contraAccountID string,
	amountCents int64,
	currencyCode string,
) (string, error) {
	// Steps 2-3: Debit customer, credit clearing account
	if err := o.postCustomerToClearingLeg(ctx, po, bookingLogID, postingAmount, valueDate, clearingAccountID, amountCents); err != nil {
		return "", err
	}

	// Steps 4-5: Debit clearing account, credit gateway
	if err := o.postClearingToGatewayLeg(ctx, po, bookingLogID, postingAmount, valueDate, clearingAccountID, contraAccountID, amountCents); err != nil {
		return "", err
	}

	// Step 6: Update BookingLog status to POSTED (all 4 balanced entries are complete)
	if err := o.finalizeClearingBookingLog(ctx, po, bookingLogID); err != nil {
		return "", err
	}

	o.logger.Info("ledger posting completed successfully (clearing flow)",
		"booking_log_id", bookingLogID,
		"payment_order_id", po.ID.String(),
		"debtor_account", po.DebtorAccountID,
		"clearing_account", clearingAccountID,
		"contra_account", contraAccountID,
		"posting_count", 4,
		"amount_cents", amountCents,
		"currency", currencyCode)

	return bookingLogID, nil
}

// postCustomerToClearingLeg creates the first two postings of the clearing flow:
// DEBIT customer account and CREDIT clearing account.
func (o *PaymentOrchestrator) postCustomerToClearingLeg(
	ctx context.Context,
	po *domain.PaymentOrder,
	bookingLogID string,
	postingAmount *money.Money,
	valueDate *timestamppb.Timestamp,
	clearingAccountID string,
	amountCents int64,
) error {
	// Step 2: Create DEBIT posting (customer account - funds leaving)
	debitCustomerIdempKey := fmt.Sprintf("debit-customer-%s", po.IdempotencyKey)
	_, err := o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount:         postingAmount,
		AccountId:             po.DebtorAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: debitCustomerIdempKey,
		},
	})
	if err != nil {
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after debit posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "debit_customer_posting",
			"posting_flow", "clearing",
			"debtor_account", po.DebtorAccountID,
			"clearing_account", clearingAccountID,
			"error", err.Error())
		return fmt.Errorf("failed to create debit posting for customer account %s: %w", po.DebtorAccountID, err)
	}

	o.logger.Debug("created debit posting (customer) in clearing flow",
		"booking_log_id", bookingLogID,
		"account_id", po.DebtorAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 3: Create CREDIT posting (clearing account - funds enter clearing)
	creditClearingIdempKey := fmt.Sprintf("credit-clearing-%s", po.IdempotencyKey)
	_, err = o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
		PostingAmount:         postingAmount,
		AccountId:             clearingAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: creditClearingIdempKey,
		},
	})
	if err != nil {
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after credit clearing posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "credit_clearing_posting",
			"posting_flow", "clearing",
			"debtor_account", po.DebtorAccountID,
			"clearing_account", clearingAccountID,
			"has_debit_customer_posting", true,
			"error", err.Error())
		return fmt.Errorf("failed to create credit posting for clearing account %s: %w", clearingAccountID, err)
	}

	o.logger.Debug("created credit posting (clearing) in clearing flow",
		"booking_log_id", bookingLogID,
		"account_id", clearingAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	return nil
}

// postClearingToGatewayLeg creates the second two postings of the clearing flow:
// DEBIT clearing account and CREDIT gateway contra-account.
func (o *PaymentOrchestrator) postClearingToGatewayLeg(
	ctx context.Context,
	po *domain.PaymentOrder,
	bookingLogID string,
	postingAmount *money.Money,
	valueDate *timestamppb.Timestamp,
	clearingAccountID string,
	contraAccountID string,
	amountCents int64,
) error {
	// Step 4: Create DEBIT posting (clearing account - funds leave clearing)
	debitClearingIdempKey := fmt.Sprintf("debit-clearing-%s", po.IdempotencyKey)
	_, err := o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount:         postingAmount,
		AccountId:             clearingAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: debitClearingIdempKey,
		},
	})
	if err != nil {
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after debit clearing posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "debit_clearing_posting",
			"posting_flow", "clearing",
			"debtor_account", po.DebtorAccountID,
			"clearing_account", clearingAccountID,
			"has_debit_customer_posting", true,
			"has_credit_clearing_posting", true,
			"error", err.Error())
		return fmt.Errorf("failed to create debit posting for clearing account %s: %w", clearingAccountID, err)
	}

	o.logger.Debug("created debit posting (clearing) in clearing flow",
		"booking_log_id", bookingLogID,
		"account_id", clearingAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 5: Create CREDIT posting (gateway contra-account - liability to processor)
	creditGatewayIdempKey := fmt.Sprintf("credit-gateway-%s", po.IdempotencyKey)
	_, err = o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
		PostingAmount:         postingAmount,
		AccountId:             contraAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: creditGatewayIdempKey,
		},
	})
	if err != nil {
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after credit gateway posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "credit_gateway_posting",
			"posting_flow", "clearing",
			"debtor_account", po.DebtorAccountID,
			"clearing_account", clearingAccountID,
			"contra_account", contraAccountID,
			"has_debit_customer_posting", true,
			"has_credit_clearing_posting", true,
			"has_debit_clearing_posting", true,
			"error", err.Error())
		return fmt.Errorf("failed to create credit posting for gateway account %s: %w", contraAccountID, err)
	}

	o.logger.Debug("created credit posting (gateway) in clearing flow",
		"booking_log_id", bookingLogID,
		"account_id", contraAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	return nil
}

// finalizeClearingBookingLog updates the booking log status to POSTED after all 4 postings complete.
func (o *PaymentOrchestrator) finalizeClearingBookingLog(
	ctx context.Context,
	po *domain.PaymentOrder,
	bookingLogID string,
) error {
	_, err := o.financialAccountingClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID,
		Status: commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED,
	})
	if err != nil {
		o.logger.Error("RECONCILIATION_REQUIRED: booking log status update failed after successful postings",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"target_status", "POSTED",
			"payment_order_id", po.ID.String(),
			"failed_step", "status_update",
			"posting_flow", "clearing",
			"has_debit_customer_posting", true,
			"has_credit_clearing_posting", true,
			"has_debit_clearing_posting", true,
			"has_credit_gateway_posting", true,
			"resolution", "manually update booking log status to POSTED",
			"error", err.Error())
		return fmt.Errorf("failed to update booking log to POSTED: %w", err)
	}
	return nil
}

// PostLedgerEntriesFromParams creates double-entry bookkeeping entries using map params.
// This method is used by Starlark saga handlers that pass parameters as maps.
// It constructs a minimal PaymentOrder from the params and delegates to PostLedgerEntries.
//
// Required params:
//   - payment_order_id: string
//   - debtor_account_id: string
//   - gateway_reference_id: string
//   - amount_cents: int64
//   - currency: string
//   - idempotency_key: string
//
// Optional params:
//   - internal_clearing_enabled: bool (overrides orchestrator setting if present)
func (o *PaymentOrchestrator) PostLedgerEntriesFromParams(ctx context.Context, params map[string]any) (string, error) {
	// Extract required parameters
	paymentOrderIDStr, ok := params["payment_order_id"].(string)
	if !ok || paymentOrderIDStr == "" {
		return "", ErrMissingPaymentOrderID
	}
	paymentOrderID, err := uuid.Parse(paymentOrderIDStr)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMissingPaymentOrderID, err)
	}

	debtorAccountID, ok := params["debtor_account_id"].(string)
	if !ok || debtorAccountID == "" {
		return "", ErrMissingDebtorAccountID
	}

	gatewayReferenceID, ok := params["gateway_reference_id"].(string)
	if !ok || gatewayReferenceID == "" {
		return "", ErrMissingGatewayReferenceID
	}

	amountCents, err := extractInt64Param(params, "amount_cents")
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMissingAmountCents, err)
	}

	currency, ok := params["currency"].(string)
	if !ok || currency == "" {
		return "", ErrMissingCurrency
	}

	idempotencyKey, ok := params["idempotency_key"].(string)
	if !ok || idempotencyKey == "" {
		return "", ErrMissingIdempotencyKey
	}

	// Construct Money - NewMoney takes (currency, amountCents)
	amount, err := domain.NewMoney(currency, amountCents)
	if err != nil {
		return "", fmt.Errorf("invalid currency %s: %w", currency, err)
	}

	// Construct minimal PaymentOrder for PostLedgerEntries
	po := &domain.PaymentOrder{
		ID:                 paymentOrderID,
		DebtorAccountID:    debtorAccountID,
		GatewayReferenceID: gatewayReferenceID,
		Amount:             amount,
		IdempotencyKey:     idempotencyKey,
	}

	return o.PostLedgerEntries(ctx, po)
}

// extractInt64Param extracts an int64 from params, handling various numeric types.
func extractInt64Param(params map[string]any, key string) (int64, error) {
	val, ok := params[key]
	if !ok {
		return 0, ErrParamKeyNotFound
	}
	switch v := val.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%w: got %T", ErrParamInvalidType, val)
	}
}
