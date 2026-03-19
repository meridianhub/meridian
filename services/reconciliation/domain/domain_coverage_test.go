package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// String() method tests for types that didn't have them

func TestAssertionStatus_String(t *testing.T) {
	assert.Equal(t, "PASSED", AssertionStatusPassed.String())
	assert.Equal(t, "FAILED", AssertionStatusFailed.String())
	assert.Equal(t, "PENDING", AssertionStatusPending.String())
	assert.Equal(t, "OVERRIDE", AssertionStatusOverride.String())
}

func TestDisputeStatus_String(t *testing.T) {
	assert.Equal(t, "OPEN", DisputeStatusOpen.String())
	assert.Equal(t, "UNDER_REVIEW", DisputeStatusUnderReview.String())
	assert.Equal(t, "ESCALATED", DisputeStatusEscalated.String())
	assert.Equal(t, "RESOLVED", DisputeStatusResolved.String())
	assert.Equal(t, "REJECTED", DisputeStatusRejected.String())
}

func TestReconciliationScope_String(t *testing.T) {
	assert.Equal(t, "ACCOUNT", ReconciliationScopeAccount.String())
	assert.Equal(t, "INSTRUMENT", ReconciliationScopeInstrument.String())
	assert.Equal(t, "PORTFOLIO", ReconciliationScopePortfolio.String())
	assert.Equal(t, "FULL", ReconciliationScopeFull.String())
}

func TestSettlementType_String(t *testing.T) {
	assert.Equal(t, "DAILY", SettlementTypeDaily.String())
	assert.Equal(t, "WEEKLY", SettlementTypeWeekly.String())
	assert.Equal(t, "MONTHLY", SettlementTypeMonthly.String())
	assert.Equal(t, "ON_DEMAND", SettlementTypeOnDemand.String())
	assert.Equal(t, "END_OF_DAY", SettlementTypeEndOfDay.String())
	assert.Equal(t, "REAL_TIME", SettlementTypeRealTime.String())
	assert.Equal(t, "FINAL", SettlementTypeFinal.String())
}

func TestVarianceReason_String(t *testing.T) {
	assert.Equal(t, "AMOUNT_MISMATCH", VarianceReasonAmountMismatch.String())
	assert.Equal(t, "MISSING_ENTRY", VarianceReasonMissingEntry.String())
	assert.Equal(t, "OTHER", VarianceReasonOther.String())
}

func TestVarianceStatus_String(t *testing.T) {
	assert.Equal(t, "DETECTED", VarianceStatusDetected.String())
	assert.Equal(t, "VALUED", VarianceStatusValued.String())
	assert.Equal(t, "OPEN", VarianceStatusOpen.String())
	assert.Equal(t, "RESOLVED", VarianceStatusResolved.String())
}

// Settlement run lifecycle tests

func TestSettlementRun_SetVarianceCount(t *testing.T) {
	run := &SettlementRun{
		RunID:   uuid.New(),
		Status:  RunStatusRunning,
		Version: 1,
	}
	run.SetVarianceCount(5)
	assert.Equal(t, 5, run.VarianceCount)
	assert.Equal(t, int64(2), run.Version)
}

func TestSettlementRun_Finalize(t *testing.T) {
	now := time.Now().UTC()
	run := &SettlementRun{
		RunID:       uuid.New(),
		Status:      RunStatusCompleted,
		CompletedAt: &now,
		Version:     1,
	}
	err := run.Finalize()
	require.NoError(t, err)
	assert.Equal(t, RunStatusFinalized, run.Status)
	assert.NotNil(t, run.CompletedAt)
}

func TestSettlementRun_Finalize_InvalidStatus(t *testing.T) {
	run := &SettlementRun{
		RunID:  uuid.New(),
		Status: RunStatusPending,
	}
	err := run.Finalize()
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestSettlementRun_Finalize_NilCompletedAt(t *testing.T) {
	run := &SettlementRun{
		RunID:   uuid.New(),
		Status:  RunStatusCompleted,
		Version: 1,
	}
	err := run.Finalize()
	require.NoError(t, err)
	assert.NotNil(t, run.CompletedAt)
}

func TestSettlementRun_IsFinalSettlement(t *testing.T) {
	run := &SettlementRun{SettlementType: SettlementTypeFinal}
	assert.True(t, run.IsFinalSettlement())

	run2 := &SettlementRun{SettlementType: SettlementTypeDaily}
	assert.False(t, run2.IsFinalSettlement())
}

// Variance Value transition

func TestVariance_Value(t *testing.T) {
	v := &Variance{
		VarianceID: uuid.New(),
		Status:     VarianceStatusDetected,
	}
	err := v.Value(decimal.NewFromFloat(50.25), "GBP")
	require.NoError(t, err)
	assert.Equal(t, VarianceStatusValued, v.Status)
	assert.Equal(t, "GBP", v.Currency)
	assert.True(t, decimal.NewFromFloat(50.25).Equal(v.ValueDelta))
}

func TestVariance_Value_InvalidStatus(t *testing.T) {
	v := &Variance{
		VarianceID: uuid.New(),
		Status:     VarianceStatusResolved,
	}
	err := v.Value(decimal.NewFromFloat(50.25), "GBP")
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

// ImbalanceTrend tests

func TestImbalanceTrend_IsPersistent(t *testing.T) {
	trend := &ImbalanceTrend{ConsecutiveDays: 2}
	assert.False(t, trend.IsPersistent())

	trend.ConsecutiveDays = 3
	assert.True(t, trend.IsPersistent())

	trend.ConsecutiveDays = 5
	assert.True(t, trend.IsPersistent())
}

func TestImbalanceTrend_RecordImbalance(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
	}

	assertionID := uuid.New()
	amount := decimal.NewFromFloat(100.50)

	trend.RecordImbalance(amount, assertionID)
	assert.Equal(t, 1, trend.ConsecutiveDays)
	assert.True(t, amount.Equal(trend.LastImbalanceAmount))
	assert.Equal(t, assertionID, trend.LastAssertionID)
	assert.Nil(t, trend.ResolvedAt)

	// Recording again on the same day should not increment
	trend.RecordImbalance(amount, assertionID)
	assert.Equal(t, 1, trend.ConsecutiveDays)
}

func TestImbalanceTrend_RecordImbalance_DifferentDay(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
		// Set last detected to yesterday
		LastDetectedAt: time.Now().UTC().Add(-25 * time.Hour),
	}

	assertionID := uuid.New()
	amount := decimal.NewFromFloat(100.50)

	trend.RecordImbalance(amount, assertionID)
	assert.Equal(t, 1, trend.ConsecutiveDays)
}

func TestImbalanceTrend_Resolve(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:         uuid.New(),
		ConsecutiveDays: 5,
	}

	trend.Resolve()
	assert.Equal(t, 0, trend.ConsecutiveDays)
	assert.NotNil(t, trend.ResolvedAt)
}

// Balance imbalance event

func TestNewBalanceImbalanceDetectedEvent(t *testing.T) {
	assertionID := uuid.New()
	totalDebits := decimal.NewFromFloat(1000.00)
	totalCredits := decimal.NewFromFloat(900.00)
	imbalance := decimal.NewFromFloat(100.00)

	event := NewBalanceImbalanceDetectedEvent(
		assertionID, "GBP",
		totalDebits, totalCredits, imbalance,
		AssertionScopePositionLedger,
		true, 5,
	)
	assert.NotNil(t, event)
	assert.Equal(t, assertionID, event.AssertionID)
	assert.Equal(t, "GBP", event.InstrumentCode)
	assert.Equal(t, 5, event.ConsecutiveDays)
	assert.True(t, event.IsPersistent)
	assert.Equal(t, "P1_CRITICAL", event.Severity)
	assert.NotEqual(t, uuid.Nil, event.EventID)
}
