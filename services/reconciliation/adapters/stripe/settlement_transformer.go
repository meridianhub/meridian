package stripe

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	stripego "github.com/stripe/stripe-go/v82"
)

// SettlementTransactionType maps Stripe balance transaction types
// to normalized settlement transaction types.
type SettlementTransactionType string

// Normalized settlement transaction types for Stripe balance transactions.
const (
	SettlementTypePayment    SettlementTransactionType = "PAYMENT"
	SettlementTypeRefund     SettlementTransactionType = "REFUND"
	SettlementTypePayout     SettlementTransactionType = "PAYOUT"
	SettlementTypeFee        SettlementTransactionType = "FEE"
	SettlementTypeDispute    SettlementTransactionType = "DISPUTE"
	SettlementTypeTransfer   SettlementTransactionType = "TRANSFER"
	SettlementTypeAdjustment SettlementTransactionType = "ADJUSTMENT"
	SettlementTypeOther      SettlementTransactionType = "OTHER"

	// SourceSystemStripe identifies Stripe as the data source.
	SourceSystemStripe = "STRIPE"
)

// SettlementTransformer converts Stripe BalanceTransaction objects into
// reconciliation domain SettlementSnapshot objects.
type SettlementTransformer struct{}

// NewSettlementTransformer creates a new transformer.
func NewSettlementTransformer() *SettlementTransformer {
	return &SettlementTransformer{}
}

// TransformToSnapshots converts a slice of Stripe balance transactions into
// SettlementSnapshot domain objects for a given settlement run.
func (t *SettlementTransformer) TransformToSnapshots(
	runID uuid.UUID,
	accountID string,
	transactions []*stripego.BalanceTransaction,
) ([]*domain.SettlementSnapshot, error) {
	snapshots := make([]*domain.SettlementSnapshot, 0, len(transactions))
	for _, bt := range transactions {
		snapshot, err := t.transformOne(runID, accountID, bt)
		if err != nil {
			return nil, fmt.Errorf("failed to transform balance transaction %s: %w", bt.ID, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

// transformOne converts a single Stripe BalanceTransaction to a SettlementSnapshot.
func (t *SettlementTransformer) transformOne(
	runID uuid.UUID,
	accountID string,
	bt *stripego.BalanceTransaction,
) (*domain.SettlementSnapshot, error) {
	// Stripe amounts are in the smallest currency unit (cents for most currencies).
	// Zero-decimal currencies (JPY, KRW, etc.) are not divided by 100.
	currency := strings.ToUpper(string(bt.Currency))
	netAmount := amountToDecimal(bt.Net, currency)

	instrumentCode := currency
	settlementDate := time.Unix(bt.AvailableOn, 0).UTC()

	attrs := map[string]string{
		"external_reference_id": bt.ID,
		"stripe_type":           string(bt.Type),
		"settlement_type":       string(mapTransactionType(bt.Type)),
		"settlement_date":       settlementDate.Format(time.RFC3339),
		"gross_amount":          amountToDecimal(bt.Amount, currency).String(),
		"fee_amount":            amountToDecimal(bt.Fee, currency).String(),
		"net_amount":            netAmount.String(),
		"source_system":         SourceSystemStripe,
		"data_source_type":      "NOSTRO_VOSTRO",
	}

	if bt.Description != "" {
		attrs["description"] = bt.Description
	}

	if bt.Source != nil && bt.Source.ID != "" {
		attrs["stripe_source_id"] = bt.Source.ID
	}

	// For settlement snapshots, the actual balance is the net amount from Stripe.
	// The expected balance comes from the internal ledger and will be zero here
	// (set during the reconciliation comparison phase).
	snapshot, err := domain.NewSettlementSnapshot(
		runID,
		accountID,
		instrumentCode,
		decimal.Zero, // expected from internal ledger (populated during comparison)
		netAmount,    // actual from Stripe
		SourceSystemStripe,
		attrs,
	)
	if err != nil {
		return nil, err
	}

	return snapshot, nil
}

// mapTransactionType maps a Stripe BalanceTransactionType to a normalized settlement type.
func mapTransactionType(stripeType stripego.BalanceTransactionType) SettlementTransactionType {
	switch stripeType { //nolint:exhaustive // remaining types handled by default case
	case stripego.BalanceTransactionTypeCharge,
		stripego.BalanceTransactionTypePayment:
		return SettlementTypePayment

	case stripego.BalanceTransactionTypeRefund,
		stripego.BalanceTransactionTypePaymentRefund,
		stripego.BalanceTransactionTypeApplicationFeeRefund,
		stripego.BalanceTransactionTypeTransferRefund,
		stripego.BalanceTransactionTypeRefundFailure,
		stripego.BalanceTransactionTypePaymentFailureRefund:
		return SettlementTypeRefund

	case stripego.BalanceTransactionTypePayout,
		stripego.BalanceTransactionTypePayoutCancel,
		stripego.BalanceTransactionTypePayoutFailure,
		stripego.BalanceTransactionTypePayoutMinimumBalanceHold,
		stripego.BalanceTransactionTypePayoutMinimumBalanceRelease:
		return SettlementTypePayout

	case stripego.BalanceTransactionTypeStripeFee,
		stripego.BalanceTransactionTypeStripeFxFee,
		stripego.BalanceTransactionTypeTaxFee,
		stripego.BalanceTransactionTypeApplicationFee:
		return SettlementTypeFee

	case stripego.BalanceTransactionTypeIssuingDispute:
		return SettlementTypeDispute

	case stripego.BalanceTransactionTypeTransfer,
		stripego.BalanceTransactionTypeTransferCancel,
		stripego.BalanceTransactionTypeTransferFailure,
		stripego.BalanceTransactionTypeConnectCollectionTransfer:
		return SettlementTypeTransfer

	case stripego.BalanceTransactionTypeAdjustment:
		return SettlementTypeAdjustment

	default:
		return SettlementTypeOther
	}
}

// zeroDecimalCurrencies lists Stripe currencies that do not use subunits.
// See: https://docs.stripe.com/currencies#zero-decimal
var zeroDecimalCurrencies = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true,
	"KMF": true, "KRW": true, "MGA": true, "PYG": true, "RWF": true,
	"UGX": true, "VND": true, "VUV": true, "XAF": true, "XOF": true, "XPF": true,
}

// amountToDecimal converts a Stripe amount to decimal based on currency.
// Zero-decimal currencies (JPY, KRW, etc.) are not divided by 100.
func amountToDecimal(amount int64, currency string) decimal.Decimal {
	if zeroDecimalCurrencies[strings.ToUpper(currency)] {
		return decimal.NewFromInt(amount)
	}
	return decimal.NewFromInt(amount).Div(decimal.NewFromInt(100))
}
