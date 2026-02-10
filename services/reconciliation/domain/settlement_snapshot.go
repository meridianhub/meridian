package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// SettlementSnapshot is a Business Query (BQ) entity that captures a point-in-time
// balance for an account/instrument combination during a settlement run.
type SettlementSnapshot struct {
	// SnapshotID is the unique identifier for this snapshot.
	SnapshotID uuid.UUID

	// RunID references the settlement run that created this snapshot.
	RunID uuid.UUID

	// AccountID identifies the account whose balance was captured.
	AccountID string

	// InstrumentCode identifies the asset type (e.g., "GBP", "KWH").
	InstrumentCode string

	// ExpectedBalance is the balance expected based on transaction logs.
	ExpectedBalance decimal.Decimal

	// ActualBalance is the balance reported by the source system.
	ActualBalance decimal.Decimal

	// VarianceAmount is the difference (actual - expected).
	VarianceAmount decimal.Decimal

	// SourceSystem identifies where the actual balance came from.
	SourceSystem string

	// Attributes stores flexible metadata (e.g., bucket key, dimension).
	Attributes map[string]string

	// CapturedAt is when the snapshot was taken.
	CapturedAt time.Time

	// CreatedAt is when this record was created.
	CreatedAt time.Time
}

// NewSettlementSnapshot creates a new snapshot with computed variance.
func NewSettlementSnapshot(
	runID uuid.UUID,
	accountID string,
	instrumentCode string,
	expectedBalance decimal.Decimal,
	actualBalance decimal.Decimal,
	sourceSystem string,
	attributes map[string]string,
) (*SettlementSnapshot, error) {
	if accountID == "" {
		return nil, ErrEmptyAccountID
	}
	if instrumentCode == "" {
		return nil, ErrEmptyInstrumentCode
	}

	now := time.Now().UTC()
	return &SettlementSnapshot{
		SnapshotID:      uuid.New(),
		RunID:           runID,
		AccountID:       accountID,
		InstrumentCode:  instrumentCode,
		ExpectedBalance: expectedBalance,
		ActualBalance:   actualBalance,
		VarianceAmount:  actualBalance.Sub(expectedBalance),
		SourceSystem:    sourceSystem,
		Attributes:      attributes,
		CapturedAt:      now,
		CreatedAt:       now,
	}, nil
}

// HasVariance returns true if the expected and actual balances do not match.
func (s *SettlementSnapshot) HasVariance() bool {
	return !s.VarianceAmount.IsZero()
}
