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
	// Check required dependencies
	if o.gatewayAccountConfig == nil {
		return "", ErrGatewayAccountConfigNotSet
	}
	if o.financialAccountingClient == nil {
		return "", ErrFinancialAccountingClientNotSet
	}

	contraAccountID, currencyCode, err := o.resolvePostingAccounts(po)
	if err != nil {
		return "", err
	}

	bookingLogID, err := o.createBookingLog(ctx, po, currencyCode)
	if err != nil {
		return "", err
	}

	amountCents := domain.ToMinorUnits(po.Amount)
	postingAmount := buildPostingAmount(currencyCode, amountCents)
	valueDate := timestamppb.Now()

	// Determine if we should use the 4-posting flow with internal clearing
	clearingAccountID, useClearingFlow := o.resolveClearingFlow(ctx, po, currencyCode)

	if useClearingFlow {
		return o.postLedgerEntriesWithClearing(ctx, po, bookingLogID, postingAmount, valueDate,
			clearingAccountID, contraAccountID, amountCents, currencyCode)
	}

	return o.postLedgerEntriesStandard(ctx, po, bookingLogID, postingAmount, valueDate,
		contraAccountID, amountCents, currencyCode)
}

// resolvePostingAccounts resolves the contra-account and validates the currency.
func (o *PaymentOrchestrator) resolvePostingAccounts(po *domain.PaymentOrder) (string, string, error) {
	gatewayID := extractGatewayIDFromRef(po.GatewayReferenceID)
	contraAccountID, err := o.gatewayAccountConfig.GetContraAccount(gatewayID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get contra-account for gateway %s: %w", gatewayID, err)
	}

	currencyCode := domain.CurrencyCode(po.Amount)
	if currencyCode == "" {
		o.logger.Warn("unsupported currency for ledger posting - payment will be marked as failed",
			"currency", currencyCode, "payment_order_id", po.ID.String(), "supported_currencies", "GBP, USD, EUR")
		return "", "", fmt.Errorf("%w: %s", ErrUnsupportedCurrency, currencyCode)
	}

	return contraAccountID, currencyCode, nil
}

// createBookingLog creates a BookingLog in PENDING status for a payment order.
func (o *PaymentOrchestrator) createBookingLog(ctx context.Context, po *domain.PaymentOrder, currencyCode string) (string, error) {
	bookingLogIDempKey := fmt.Sprintf("booking-log-%s", po.IdempotencyKey)
	bookingLogResp, err := o.financialAccountingClient.InitiateFinancialBookingLog(ctx, &financialaccountingv1.InitiateFinancialBookingLogRequest{
		FinancialAccountType:    "CURRENT",
		ProductServiceReference: "payment-order",
		BusinessUnitReference:   "payment-order-service",
		ChartOfAccountsRules:    "outbound-payment",
		BaseInstrumentCode:      currencyCode,
		IdempotencyKey:          &commonpb.IdempotencyKey{Key: bookingLogIDempKey},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create booking log: %w", err)
	}
	if bookingLogResp.FinancialBookingLog == nil {
		return "", fmt.Errorf("%w: payment order %s", ErrNilBookingLogResponse, po.ID.String())
	}

	o.logger.Debug("created booking log for ledger posting",
		"booking_log_id", bookingLogResp.FinancialBookingLog.Id, "payment_order_id", po.ID.String())

	return bookingLogResp.FinancialBookingLog.Id, nil
}

// buildPostingAmount converts cents to google.type.Money format.
func buildPostingAmount(currencyCode string, amountCents int64) *money.Money {
	return &money.Money{
		CurrencyCode: currencyCode,
		Units:        amountCents / 100,
		Nanos:        int32((amountCents % 100) * 10000000),
	}
}

// resolveClearingFlow determines whether to use the 4-posting clearing flow.
func (o *PaymentOrchestrator) resolveClearingFlow(ctx context.Context, po *domain.PaymentOrder, currencyCode string) (string, bool) {
	if !o.internalClearingEnabled {
		return "", false
	}
	if o.accountResolver == nil {
		o.logger.Debug("internal clearing enabled but account resolver not configured, using standard flow",
			"payment_order_id", po.ID.String())
		return "", false
	}

	clearingAccountID, resolveErr := o.accountResolver.GetSettlementClearingAccount(ctx, currencyCode)
	if resolveErr != nil {
		o.logger.Info("clearing account lookup failed, falling back to standard posting flow",
			"payment_order_id", po.ID.String(), "currency", currencyCode, "reason", resolveErr.Error())
		return "", false
	}

	o.logger.Info("using internal clearing flow with 4 postings",
		"payment_order_id", po.ID.String(), "clearing_account_id", clearingAccountID, "currency", currencyCode)
	return clearingAccountID, true
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
	if err := o.capturePosting(ctx, po, bookingLogID, postingAmount, valueDate,
		po.DebtorAccountID, "debit-customer", commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
		"debit_customer_posting", "standard", amountCents, "debit posting",
		"debtor_account", po.DebtorAccountID); err != nil {
		return "", err
	}

	// Step 3: Create CREDIT posting (gateway contra-account)
	if err := o.capturePosting(ctx, po, bookingLogID, postingAmount, valueDate,
		contraAccountID, "credit-gateway", commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
		"credit_gateway_posting", "standard", amountCents, "credit posting",
		"debtor_account", po.DebtorAccountID,
		"contra_account", contraAccountID,
		"has_debit_customer_posting", true); err != nil {
		return "", err
	}

	// Step 4: Update BookingLog status to POSTED
	if err := o.finalizeBookingLog(ctx, po, bookingLogID, "standard",
		"has_debit_customer_posting", true,
		"has_credit_gateway_posting", true); err != nil {
		return "", err
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

// capturePosting creates a single ledger posting entry with reconciliation-aware error logging.
// extraLogFields are appended to the RECONCILIATION_REQUIRED log to provide per-flow context
// (e.g. debtor_account, clearing_account, has_debit_customer_posting) for incident response.
func (o *PaymentOrchestrator) capturePosting(
	ctx context.Context,
	po *domain.PaymentOrder,
	bookingLogID string,
	postingAmount *money.Money,
	valueDate *timestamppb.Timestamp,
	accountID string,
	idempKeyPrefix string,
	direction commonpb.PostingDirection,
	failedStep string,
	postingFlow string,
	amountCents int64,
	postingDescription string,
	extraLogFields ...any,
) error {
	idempKey := fmt.Sprintf("%s-%s", idempKeyPrefix, po.IdempotencyKey)
	_, err := o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      direction,
		PostingAmount:         postingAmount,
		AccountId:             accountID,
		ValueDate:             valueDate,
		IdempotencyKey:        &commonpb.IdempotencyKey{Key: idempKey},
	})
	if err != nil {
		fields := []any{
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", failedStep,
			"posting_flow", postingFlow,
		}
		fields = append(fields, extraLogFields...)
		fields = append(fields, "error", err.Error())
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after posting failure", fields...)
		return fmt.Errorf("failed to create %s for account %s: %w", postingDescription, accountID, err)
	}

	o.logger.Debug("created posting",
		"booking_log_id", bookingLogID,
		"account_id", accountID,
		"direction", direction.String(),
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())
	return nil
}

// finalizeBookingLog updates the booking log status to POSTED.
// extraLogFields are appended to the RECONCILIATION_REQUIRED log to provide per-flow context
// (e.g. has_debit_customer_posting, has_credit_gateway_posting) for incident response.
func (o *PaymentOrchestrator) finalizeBookingLog(ctx context.Context, po *domain.PaymentOrder, bookingLogID, postingFlow string, extraLogFields ...any) error {
	_, err := o.financialAccountingClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID,
		Status: commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED,
	})
	if err != nil {
		fields := []any{
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"target_status", "POSTED",
			"payment_order_id", po.ID.String(),
			"failed_step", "status_update",
			"posting_flow", postingFlow,
		}
		fields = append(fields, extraLogFields...)
		fields = append(fields,
			"resolution", "manually update booking log status to POSTED",
			"error", err.Error())
		o.logger.Error("RECONCILIATION_REQUIRED: booking log status update failed after successful postings", fields...)
		return fmt.Errorf("failed to update booking log to POSTED: %w", err)
	}
	return nil
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
	if err := o.capturePosting(ctx, po, bookingLogID, postingAmount, valueDate,
		po.DebtorAccountID, "debit-customer", commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
		"debit_customer_posting", "clearing", amountCents, "debit posting for customer account",
		"debtor_account", po.DebtorAccountID,
		"clearing_account", clearingAccountID); err != nil {
		return err
	}

	return o.capturePosting(ctx, po, bookingLogID, postingAmount, valueDate,
		clearingAccountID, "credit-clearing", commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
		"credit_clearing_posting", "clearing", amountCents, "credit posting for clearing account",
		"debtor_account", po.DebtorAccountID,
		"clearing_account", clearingAccountID,
		"has_debit_customer_posting", true)
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
	if err := o.capturePosting(ctx, po, bookingLogID, postingAmount, valueDate,
		clearingAccountID, "debit-clearing", commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
		"debit_clearing_posting", "clearing", amountCents, "debit posting for clearing account",
		"debtor_account", po.DebtorAccountID,
		"clearing_account", clearingAccountID,
		"has_debit_customer_posting", true,
		"has_credit_clearing_posting", true); err != nil {
		return err
	}

	return o.capturePosting(ctx, po, bookingLogID, postingAmount, valueDate,
		contraAccountID, "credit-gateway", commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
		"credit_gateway_posting", "clearing", amountCents, "credit posting for gateway account",
		"debtor_account", po.DebtorAccountID,
		"clearing_account", clearingAccountID,
		"contra_account", contraAccountID,
		"has_debit_customer_posting", true,
		"has_credit_clearing_posting", true,
		"has_debit_clearing_posting", true)
}

// finalizeClearingBookingLog updates the booking log status to POSTED after all 4 postings complete.
func (o *PaymentOrchestrator) finalizeClearingBookingLog(
	ctx context.Context,
	po *domain.PaymentOrder,
	bookingLogID string,
) error {
	return o.finalizeBookingLog(ctx, po, bookingLogID, "clearing",
		"has_debit_customer_posting", true,
		"has_credit_clearing_posting", true,
		"has_debit_clearing_posting", true,
		"has_credit_gateway_posting", true)
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
