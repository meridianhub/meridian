package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistentImbalanceThreshold(t *testing.T) {
	assert.Equal(t, 3, PersistentImbalanceThreshold, "threshold must be 3 consecutive days")
}

func TestImbalanceTrend_NewTrend_InitialState(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
	}

	assert.Equal(t, 0, trend.ConsecutiveDays)
	assert.True(t, trend.LastImbalanceAmount.IsZero())
	assert.Equal(t, uuid.Nil, trend.LastAssertionID)
	assert.True(t, trend.FirstDetectedAt.IsZero())
	assert.True(t, trend.LastDetectedAt.IsZero())
	assert.Nil(t, trend.ResolvedAt)
}

func TestImbalanceTrend_RecordImbalance_FirstRecord(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
	}

	assertionID := uuid.New()
	amount := decimal.NewFromFloat(500.00)

	trend.RecordImbalance(amount, assertionID)

	assert.Equal(t, 1, trend.ConsecutiveDays)
	assert.True(t, amount.Equal(trend.LastImbalanceAmount))
	assert.Equal(t, assertionID, trend.LastAssertionID)
	assert.False(t, trend.LastDetectedAt.IsZero())
	assert.Nil(t, trend.ResolvedAt)
}

func TestImbalanceTrend_RecordImbalance_SameDayNoIncrement(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
	}

	assertionID1 := uuid.New()
	assertionID2 := uuid.New()
	amount := decimal.NewFromFloat(500.00)

	trend.RecordImbalance(amount, assertionID1)
	assert.Equal(t, 1, trend.ConsecutiveDays)

	// Same day call should not increment consecutive days
	trend.RecordImbalance(amount, assertionID2)
	assert.Equal(t, 1, trend.ConsecutiveDays, "same-day detection must not increment consecutive days")
	assert.Equal(t, assertionID2, trend.LastAssertionID, "last assertion ID should be updated")
}

func TestImbalanceTrend_RecordImbalance_MultipleSameDayCalls(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
	}

	amount := decimal.NewFromFloat(100.00)

	for i := 0; i < 10; i++ {
		trend.RecordImbalance(amount, uuid.New())
	}

	assert.Equal(t, 1, trend.ConsecutiveDays, "10 same-day calls should still count as 1 day")
}

func TestImbalanceTrend_RecordImbalance_NextDayIncrement(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:         uuid.New(),
		InstrumentCode:  "GBP",
		ConsecutiveDays: 1,
		LastDetectedAt:  time.Now().UTC().Add(-25 * time.Hour), // yesterday
	}

	assertionID := uuid.New()
	amount := decimal.NewFromFloat(300.00)

	trend.RecordImbalance(amount, assertionID)

	assert.Equal(t, 2, trend.ConsecutiveDays, "different-day detection should increment consecutive days")
}

func TestImbalanceTrend_RecordImbalance_ThresholdBreach(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
	}

	amount := decimal.NewFromFloat(250.00)

	// Simulate 3 consecutive days
	trend.RecordImbalance(amount, uuid.New())
	assert.False(t, trend.IsPersistent())

	trend.LastDetectedAt = trend.LastDetectedAt.Add(-25 * time.Hour)
	trend.RecordImbalance(amount, uuid.New())
	assert.False(t, trend.IsPersistent())

	trend.LastDetectedAt = trend.LastDetectedAt.Add(-25 * time.Hour)
	trend.RecordImbalance(amount, uuid.New())
	assert.True(t, trend.IsPersistent(), "3 consecutive days must trigger persistent imbalance")
}

func TestImbalanceTrend_RecordImbalance_AmountUpdated(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
	}

	amount1 := decimal.NewFromFloat(100.00)
	amount2 := decimal.NewFromFloat(200.00)

	trend.RecordImbalance(amount1, uuid.New())
	assert.True(t, amount1.Equal(trend.LastImbalanceAmount))

	// Different day, different amount
	trend.LastDetectedAt = trend.LastDetectedAt.Add(-25 * time.Hour)
	trend.RecordImbalance(amount2, uuid.New())
	assert.True(t, amount2.Equal(trend.LastImbalanceAmount), "imbalance amount should update on each recording")
}

func TestImbalanceTrend_RecordImbalance_ClearsResolvedAt(t *testing.T) {
	now := time.Now().UTC()
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
		ResolvedAt:     &now,
	}

	trend.RecordImbalance(decimal.NewFromFloat(100.00), uuid.New())

	assert.Nil(t, trend.ResolvedAt, "recording an imbalance must clear the resolved state")
}

func TestImbalanceTrend_IsPersistent_BelowThreshold(t *testing.T) {
	trend := &ImbalanceTrend{ConsecutiveDays: 0}
	assert.False(t, trend.IsPersistent())

	trend.ConsecutiveDays = 1
	assert.False(t, trend.IsPersistent())

	trend.ConsecutiveDays = 2
	assert.False(t, trend.IsPersistent())
}

func TestImbalanceTrend_IsPersistent_AtThreshold(t *testing.T) {
	trend := &ImbalanceTrend{ConsecutiveDays: PersistentImbalanceThreshold}
	assert.True(t, trend.IsPersistent(), "exactly at threshold should be persistent")
}

func TestImbalanceTrend_IsPersistent_AboveThreshold(t *testing.T) {
	trend := &ImbalanceTrend{ConsecutiveDays: 10}
	assert.True(t, trend.IsPersistent())
}

func TestImbalanceTrend_Resolve_ResetsDays(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:         uuid.New(),
		InstrumentCode:  "GBP",
		ConsecutiveDays: 7,
	}

	before := time.Now().UTC().Add(-time.Second)
	trend.Resolve()
	after := time.Now().UTC().Add(time.Second)

	assert.Equal(t, 0, trend.ConsecutiveDays)
	require.NotNil(t, trend.ResolvedAt)
	assert.True(t, trend.ResolvedAt.After(before))
	assert.True(t, trend.ResolvedAt.Before(after))
}

func TestImbalanceTrend_Resolve_SetsPersistentToFalse(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:         uuid.New(),
		ConsecutiveDays: 5,
	}
	require.True(t, trend.IsPersistent())

	trend.Resolve()

	assert.False(t, trend.IsPersistent(), "resolved trend must not be persistent")
	assert.Equal(t, 0, trend.ConsecutiveDays)
}

func TestImbalanceTrend_RecordAfterResolve_ResetsToZeroSameDay(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
	}

	// Simulate 3 days of imbalance then resolution
	amount := decimal.NewFromFloat(100.00)
	trend.RecordImbalance(amount, uuid.New())
	trend.LastDetectedAt = trend.LastDetectedAt.Add(-25 * time.Hour)
	trend.RecordImbalance(amount, uuid.New())
	trend.LastDetectedAt = trend.LastDetectedAt.Add(-25 * time.Hour)
	trend.RecordImbalance(amount, uuid.New())
	assert.True(t, trend.IsPersistent())

	trend.Resolve()
	assert.Equal(t, 0, trend.ConsecutiveDays)

	// New imbalance detected on the same day as Resolve - same day means no increment
	trend.RecordImbalance(amount, uuid.New())
	assert.Equal(t, 0, trend.ConsecutiveDays, "same-day detection after resolve does not increment")
	assert.Nil(t, trend.ResolvedAt, "recording imbalance must clear ResolvedAt")
}

func TestImbalanceTrend_RecordAfterResolve_NextDayStartsFromOne(t *testing.T) {
	trend := &ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
	}

	// Simulate 3 days of imbalance then resolution yesterday
	amount := decimal.NewFromFloat(100.00)
	trend.RecordImbalance(amount, uuid.New())
	trend.LastDetectedAt = trend.LastDetectedAt.Add(-25 * time.Hour)
	trend.RecordImbalance(amount, uuid.New())
	trend.LastDetectedAt = trend.LastDetectedAt.Add(-25 * time.Hour)
	trend.RecordImbalance(amount, uuid.New())
	assert.True(t, trend.IsPersistent())

	trend.Resolve()
	assert.Equal(t, 0, trend.ConsecutiveDays)

	// Simulate yesterday's LastDetectedAt so next recording counts as a new day
	trend.LastDetectedAt = trend.LastDetectedAt.Add(-25 * time.Hour)
	trend.RecordImbalance(amount, uuid.New())
	assert.Equal(t, 1, trend.ConsecutiveDays, "next-day detection after resolve should start counter at 1")
	assert.Nil(t, trend.ResolvedAt)
}

func TestImbalanceTrend_SameUTCDay(t *testing.T) {
	t.Run("same day same timezone", func(t *testing.T) {
		a := time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC)
		b := time.Date(2024, 1, 15, 23, 59, 59, 0, time.UTC)
		assert.True(t, sameUTCDay(a, b))
	})

	t.Run("different day", func(t *testing.T) {
		a := time.Date(2024, 1, 15, 23, 59, 59, 0, time.UTC)
		b := time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC)
		assert.False(t, sameUTCDay(a, b))
	})

	t.Run("same UTC day despite different local timezones", func(t *testing.T) {
		// 2024-01-15 23:00 UTC = 2024-01-16 07:00 in UTC+8 (next local day)
		// 2024-01-15 01:00 UTC = 2024-01-14 17:00 in UTC-8 (previous local day)
		// Both are 2024-01-15 UTC, so sameUTCDay must return true.
		eastZone := time.FixedZone("UTC+8", 8*60*60)
		westZone := time.FixedZone("UTC-8", -8*60*60)
		a := time.Date(2024, 1, 15, 23, 0, 0, 0, time.UTC).In(eastZone)
		b := time.Date(2024, 1, 15, 1, 0, 0, 0, time.UTC).In(westZone)
		assert.True(t, sameUTCDay(a, b))
	})

	t.Run("different years", func(t *testing.T) {
		a := time.Date(2023, 12, 31, 12, 0, 0, 0, time.UTC)
		b := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		assert.False(t, sameUTCDay(a, b))
	})
}
