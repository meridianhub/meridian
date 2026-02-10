package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestNewFeeVariance_OverCharge(t *testing.T) {
	poID := uuid.New()
	v := NewFeeVariance(poID, "pi_123", 250, 260, "gbp", "tenant_a")

	assert.Equal(t, poID, v.PaymentOrderID)
	assert.Equal(t, "pi_123", v.GatewayReferenceID)
	assert.Equal(t, int64(250), v.ExpectedFeeCents)
	assert.Equal(t, int64(260), v.ActualFeeCents)
	assert.Equal(t, int64(10), v.VarianceCents)
	assert.Equal(t, "over", v.VarianceType())
	assert.Equal(t, "gbp", v.Currency)
	assert.Equal(t, "tenant_a", v.TenantID)
	// 10/250 * 100 = 4%
	assert.True(t, v.VariancePercent.Equal(decimal.NewFromInt(4)))
}

func TestNewFeeVariance_UnderCharge(t *testing.T) {
	v := NewFeeVariance(uuid.New(), "pi_456", 300, 280, "usd", "tenant_b")

	assert.Equal(t, int64(-20), v.VarianceCents)
	assert.Equal(t, "under", v.VarianceType())
}

func TestNewFeeVariance_ExactMatch(t *testing.T) {
	v := NewFeeVariance(uuid.New(), "pi_789", 250, 250, "eur", "tenant_c")

	assert.Equal(t, int64(0), v.VarianceCents)
	assert.Equal(t, "match", v.VarianceType())
	assert.True(t, v.VariancePercent.IsZero())
}

func TestNewFeeVariance_ZeroExpectedFee(t *testing.T) {
	v := NewFeeVariance(uuid.New(), "pi_000", 0, 50, "gbp", "tenant_d")

	assert.Equal(t, int64(50), v.VarianceCents)
	assert.Equal(t, "over", v.VarianceType())
	// Variance percent is zero when expected is zero (avoid division by zero)
	assert.True(t, v.VariancePercent.IsZero())
}

func TestFeeReconciliationReport_Structure(t *testing.T) {
	report := FeeReconciliationReport{
		TenantID:       "tenant_a",
		TotalPayments:  100,
		TotalVariances: 2,
		Variances: []FeeVariance{
			NewFeeVariance(uuid.New(), "pi_1", 250, 260, "gbp", "tenant_a"),
			NewFeeVariance(uuid.New(), "pi_2", 300, 280, "gbp", "tenant_a"),
		},
	}

	assert.Equal(t, 2, len(report.Variances))
	assert.Equal(t, 100, report.TotalPayments)
	assert.Equal(t, 2, report.TotalVariances)
}
