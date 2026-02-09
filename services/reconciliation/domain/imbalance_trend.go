package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PersistentImbalanceThreshold is the number of consecutive days of imbalance
// that triggers a persistent imbalance alert.
const PersistentImbalanceThreshold = 3

// ImbalanceTrend tracks imbalance history for an instrument code to detect
// persistent imbalance patterns (P1 critical for ledger integrity).
type ImbalanceTrend struct {
	TrendID             uuid.UUID
	InstrumentCode      string
	ConsecutiveDays     int
	LastImbalanceAmount decimal.Decimal
	LastAssertionID     uuid.UUID
	FirstDetectedAt     time.Time
	LastDetectedAt      time.Time
	ResolvedAt          *time.Time
}

// IsPersistent returns true if the imbalance has been present for 3+ consecutive days.
func (t *ImbalanceTrend) IsPersistent() bool {
	return t.ConsecutiveDays >= PersistentImbalanceThreshold
}

// RecordImbalance records a new imbalance detection. The consecutive day count
// only increments if the last detection was on a different calendar day (UTC),
// preventing multiple assertions on the same day from inflating the count.
func (t *ImbalanceTrend) RecordImbalance(amount decimal.Decimal, assertionID uuid.UUID) {
	now := time.Now().UTC()

	// Only increment consecutive days if this is a new calendar day
	if t.LastDetectedAt.IsZero() || !sameUTCDay(t.LastDetectedAt, now) {
		t.ConsecutiveDays++
	}

	t.LastImbalanceAmount = amount
	t.LastAssertionID = assertionID
	t.LastDetectedAt = now
	t.ResolvedAt = nil
}

// sameUTCDay returns true if both timestamps fall on the same UTC calendar day.
func sameUTCDay(a, b time.Time) bool {
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}

// Resolve marks the trend as resolved when balance is restored.
func (t *ImbalanceTrend) Resolve() {
	now := time.Now().UTC()
	t.ResolvedAt = &now
	t.ConsecutiveDays = 0
}
