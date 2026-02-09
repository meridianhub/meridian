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

// RecordImbalance increments the trend for a new day of imbalance.
func (t *ImbalanceTrend) RecordImbalance(amount decimal.Decimal, assertionID uuid.UUID) {
	t.ConsecutiveDays++
	t.LastImbalanceAmount = amount
	t.LastAssertionID = assertionID
	t.LastDetectedAt = time.Now().UTC()
	t.ResolvedAt = nil
}

// Resolve marks the trend as resolved when balance is restored.
func (t *ImbalanceTrend) Resolve() {
	now := time.Now().UTC()
	t.ResolvedAt = &now
	t.ConsecutiveDays = 0
}
