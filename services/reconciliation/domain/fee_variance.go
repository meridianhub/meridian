package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// FeeVariance represents a discrepancy between the expected platform fee
// (calculated by Meridian) and the actual application fee charged by Stripe.
type FeeVariance struct {
	// PaymentOrderID is the payment order that has a fee discrepancy.
	PaymentOrderID uuid.UUID

	// GatewayReferenceID is the Stripe PaymentIntent ID.
	GatewayReferenceID string

	// ExpectedFeeCents is the platform fee calculated by Meridian (in minor units).
	ExpectedFeeCents int64

	// ActualFeeCents is the application_fee from Stripe BalanceTransaction (in minor units).
	ActualFeeCents int64

	// VarianceCents is the difference: actual - expected.
	VarianceCents int64

	// VariancePercent is the percentage difference relative to expected fee.
	// Zero if expected fee is zero.
	VariancePercent decimal.Decimal

	// Currency is the fee currency (lowercase ISO code).
	Currency string

	// TenantID identifies which tenant this fee belongs to.
	TenantID string

	// DetectedAt is when the variance was detected.
	DetectedAt time.Time
}

// VarianceType returns whether the actual fee was over or under the expected fee.
func (v FeeVariance) VarianceType() string {
	if v.VarianceCents > 0 {
		return "over"
	}
	if v.VarianceCents < 0 {
		return "under"
	}
	return "match"
}

// FeeReconciliationReport holds the results of comparing expected vs actual
// platform fees for a set of payments within a time period.
type FeeReconciliationReport struct {
	// TenantID is the tenant this report covers.
	TenantID string

	// PeriodStart is the beginning of the reporting period.
	PeriodStart time.Time

	// PeriodEnd is the end of the reporting period.
	PeriodEnd time.Time

	// TotalPayments is the count of payments examined.
	TotalPayments int

	// TotalVariances is the count of payments with fee discrepancies.
	TotalVariances int

	// Variances contains the individual fee discrepancies.
	Variances []FeeVariance

	// GeneratedAt is when this report was generated.
	GeneratedAt time.Time
}

// NewFeeVariance creates a FeeVariance from expected and actual fee amounts.
func NewFeeVariance(
	paymentOrderID uuid.UUID,
	gatewayReferenceID string,
	expectedFeeCents int64,
	actualFeeCents int64,
	currency string,
	tenantID string,
) FeeVariance {
	varianceCents := actualFeeCents - expectedFeeCents

	var variancePercent decimal.Decimal
	if expectedFeeCents != 0 {
		variancePercent = decimal.NewFromInt(varianceCents).
			Mul(decimal.NewFromInt(100)).
			Div(decimal.NewFromInt(expectedFeeCents))
	}

	return FeeVariance{
		PaymentOrderID:     paymentOrderID,
		GatewayReferenceID: gatewayReferenceID,
		ExpectedFeeCents:   expectedFeeCents,
		ActualFeeCents:     actualFeeCents,
		VarianceCents:      varianceCents,
		VariancePercent:    variancePercent,
		Currency:           currency,
		TenantID:           tenantID,
		DetectedAt:         time.Now().UTC(),
	}
}
