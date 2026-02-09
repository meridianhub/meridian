package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// BalanceImbalanceDetectedEvent is published when a cross-account balance
// assertion fails (total_debits != total_credits for an instrument).
// This is a P1/Critical severity event indicating a ledger integrity violation.
type BalanceImbalanceDetectedEvent struct {
	// EventID uniquely identifies this event.
	EventID uuid.UUID

	// AssertionID references the balance assertion that detected the imbalance.
	AssertionID uuid.UUID

	// InstrumentCode identifies the asset type with the imbalance.
	InstrumentCode string

	// TotalDebits is the aggregated debit amount.
	TotalDebits decimal.Decimal

	// TotalCredits is the aggregated credit amount.
	TotalCredits decimal.Decimal

	// ImbalanceAmount is the difference (total_debits - total_credits).
	ImbalanceAmount decimal.Decimal

	// Scope is the assertion scope that detected this imbalance.
	Scope AssertionScope

	// Severity indicates this is a P1/Critical event.
	Severity string

	// IsPersistent indicates the imbalance has been detected for 3+ consecutive days.
	IsPersistent bool

	// ConsecutiveDays is the number of consecutive days the imbalance has persisted.
	ConsecutiveDays int

	// DetectedAt is when the imbalance was detected.
	DetectedAt time.Time
}

// NewBalanceImbalanceDetectedEvent creates a new critical imbalance event.
func NewBalanceImbalanceDetectedEvent(
	assertionID uuid.UUID,
	instrumentCode string,
	totalDebits, totalCredits, imbalanceAmount decimal.Decimal,
	scope AssertionScope,
	isPersistent bool,
	consecutiveDays int,
) *BalanceImbalanceDetectedEvent {
	return &BalanceImbalanceDetectedEvent{
		EventID:         uuid.New(),
		AssertionID:     assertionID,
		InstrumentCode:  instrumentCode,
		TotalDebits:     totalDebits,
		TotalCredits:    totalCredits,
		ImbalanceAmount: imbalanceAmount,
		Scope:           scope,
		Severity:        "P1_CRITICAL",
		IsPersistent:    isPersistent,
		ConsecutiveDays: consecutiveDays,
		DetectedAt:      time.Now().UTC(),
	}
}
