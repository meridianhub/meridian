package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBalanceImbalanceDetectedEvent_UniqueEventIDs(t *testing.T) {
	assertionID := uuid.New()
	debits := decimal.NewFromFloat(1000.00)
	credits := decimal.NewFromFloat(900.00)
	imbalance := decimal.NewFromFloat(100.00)

	event1 := NewBalanceImbalanceDetectedEvent(assertionID, "GBP", debits, credits, imbalance, AssertionScopePositionLedger, false, 1)
	event2 := NewBalanceImbalanceDetectedEvent(assertionID, "GBP", debits, credits, imbalance, AssertionScopePositionLedger, false, 1)

	assert.NotEqual(t, event1.EventID, event2.EventID, "each event must have a unique EventID")
}

func TestBalanceImbalanceDetectedEvent_AlwaysP1Critical(t *testing.T) {
	event := NewBalanceImbalanceDetectedEvent(
		uuid.New(), "GBP",
		decimal.NewFromFloat(1000), decimal.NewFromFloat(900), decimal.NewFromFloat(100),
		AssertionScopePositionLedger, false, 1,
	)

	assert.Equal(t, "P1_CRITICAL", event.Severity, "all imbalance events must be P1_CRITICAL")
}

func TestBalanceImbalanceDetectedEvent_DetectedAtIsUTC(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)

	event := NewBalanceImbalanceDetectedEvent(
		uuid.New(), "GBP",
		decimal.NewFromFloat(1000), decimal.NewFromFloat(900), decimal.NewFromFloat(100),
		AssertionScopePositionLedger, false, 1,
	)

	after := time.Now().UTC().Add(time.Second)

	assert.True(t, event.DetectedAt.After(before), "DetectedAt should be after test start")
	assert.True(t, event.DetectedAt.Before(after), "DetectedAt should be before test end")
	assert.Equal(t, time.UTC, event.DetectedAt.Location(), "DetectedAt must be UTC")
}

func TestBalanceImbalanceDetectedEvent_PersistenceFields(t *testing.T) {
	t.Run("not persistent", func(t *testing.T) {
		event := NewBalanceImbalanceDetectedEvent(
			uuid.New(), "GBP",
			decimal.NewFromFloat(1000), decimal.NewFromFloat(900), decimal.NewFromFloat(100),
			AssertionScopePositionLedger, false, 2,
		)

		assert.False(t, event.IsPersistent)
		assert.Equal(t, 2, event.ConsecutiveDays)
	})

	t.Run("persistent", func(t *testing.T) {
		event := NewBalanceImbalanceDetectedEvent(
			uuid.New(), "GBP",
			decimal.NewFromFloat(1000), decimal.NewFromFloat(900), decimal.NewFromFloat(100),
			AssertionScopePositionLedger, true, 5,
		)

		assert.True(t, event.IsPersistent)
		assert.Equal(t, 5, event.ConsecutiveDays)
	})
}

func TestBalanceImbalanceDetectedEvent_AmountFields(t *testing.T) {
	debits := decimal.RequireFromString("123456.789")
	credits := decimal.RequireFromString("123000.000")
	imbalance := decimal.RequireFromString("456.789")

	event := NewBalanceImbalanceDetectedEvent(
		uuid.New(), "GBP",
		debits, credits, imbalance,
		AssertionScopePositionLedger, false, 1,
	)

	assert.True(t, debits.Equal(event.TotalDebits))
	assert.True(t, credits.Equal(event.TotalCredits))
	assert.True(t, imbalance.Equal(event.ImbalanceAmount))
}

func TestBalanceImbalanceDetectedEvent_ScopePreserved(t *testing.T) {
	scopes := []AssertionScope{
		AssertionScopePositionLedger,
		AssertionScopeCrossAccount,
	}

	for _, scope := range scopes {
		t.Run(scope.String(), func(t *testing.T) {
			event := NewBalanceImbalanceDetectedEvent(
				uuid.New(), "GBP",
				decimal.NewFromFloat(1000), decimal.NewFromFloat(900), decimal.NewFromFloat(100),
				scope, false, 1,
			)

			assert.Equal(t, scope, event.Scope)
		})
	}
}

func TestBalanceImbalanceDetectedEvent_NonCurrencyInstruments(t *testing.T) {
	instruments := []string{"KWH", "TONNE_CO2E", "GPU_HOUR"}

	for _, code := range instruments {
		t.Run(code, func(t *testing.T) {
			event := NewBalanceImbalanceDetectedEvent(
				uuid.New(), code,
				decimal.NewFromFloat(500), decimal.NewFromFloat(400), decimal.NewFromFloat(100),
				AssertionScopePositionLedger, false, 1,
			)

			require.NotNil(t, event)
			assert.Equal(t, code, event.InstrumentCode)
			assert.Equal(t, "P1_CRITICAL", event.Severity)
		})
	}
}

func TestBalanceImbalanceDetectedEvent_AssertionIDLinked(t *testing.T) {
	assertionID := uuid.New()

	event := NewBalanceImbalanceDetectedEvent(
		assertionID, "GBP",
		decimal.NewFromFloat(1000), decimal.NewFromFloat(900), decimal.NewFromFloat(100),
		AssertionScopePositionLedger, false, 1,
	)

	assert.Equal(t, assertionID, event.AssertionID, "event must reference the triggering assertion")
	assert.NotEqual(t, assertionID, event.EventID, "EventID must be different from AssertionID")
}

func TestBalanceImbalanceDetectedEvent_SmallImbalance(t *testing.T) {
	// Even a tiny imbalance produces a P1_CRITICAL event
	debits := decimal.RequireFromString("100000.000000000001")
	credits := decimal.RequireFromString("100000.000000000000")
	imbalance := decimal.RequireFromString("0.000000000001")

	event := NewBalanceImbalanceDetectedEvent(
		uuid.New(), "GBP",
		debits, credits, imbalance,
		AssertionScopePositionLedger, false, 1,
	)

	assert.Equal(t, "P1_CRITICAL", event.Severity)
	assert.True(t, imbalance.Equal(event.ImbalanceAmount))
}
