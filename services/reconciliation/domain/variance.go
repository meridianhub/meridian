package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Variance is a Business Query (BQ) entity representing a detected discrepancy
// between expected and actual balances.
type Variance struct {
	// VarianceID is the unique identifier for this variance.
	VarianceID uuid.UUID

	// RunID references the settlement run that detected this variance.
	RunID uuid.UUID

	// SnapshotID references the snapshot where the variance was found.
	SnapshotID uuid.UUID

	// AccountID identifies the account with the discrepancy.
	AccountID string

	// InstrumentCode identifies the asset type.
	InstrumentCode string

	// ExpectedAmount is the expected balance.
	ExpectedAmount decimal.Decimal

	// ActualAmount is the actual balance found.
	ActualAmount decimal.Decimal

	// VarianceAmount is the difference (actual - expected).
	VarianceAmount decimal.Decimal

	// ValueDelta is the monetary impact of the variance in settlement currency.
	ValueDelta decimal.Decimal

	// Currency is the settlement currency for the value delta.
	Currency string

	// Reason classifies the type of discrepancy.
	Reason VarianceReason

	// Status is the resolution state.
	Status VarianceStatus

	// ResolutionNote records how the variance was resolved.
	ResolutionNote string

	// ResolvedBy records who resolved the variance.
	ResolvedBy string

	// ResolvedAt records when the variance was resolved.
	ResolvedAt *time.Time

	// Attributes stores flexible metadata.
	Attributes map[string]string

	// CreatedAt is when this record was created.
	CreatedAt time.Time

	// UpdatedAt is when this record was last updated.
	UpdatedAt time.Time
}

// NewVariance creates a new Variance with validation.
func NewVariance(
	runID uuid.UUID,
	snapshotID uuid.UUID,
	accountID string,
	instrumentCode string,
	expectedAmount decimal.Decimal,
	actualAmount decimal.Decimal,
	reason VarianceReason,
) (*Variance, error) {
	if accountID == "" {
		return nil, ErrEmptyAccountID
	}
	if instrumentCode == "" {
		return nil, ErrEmptyInstrumentCode
	}
	if !reason.IsValid() {
		return nil, ErrEmptyVarianceReason
	}

	now := time.Now().UTC()
	return &Variance{
		VarianceID:     uuid.New(),
		RunID:          runID,
		SnapshotID:     snapshotID,
		AccountID:      accountID,
		InstrumentCode: instrumentCode,
		ExpectedAmount: expectedAmount,
		ActualAmount:   actualAmount,
		VarianceAmount: actualAmount.Sub(expectedAmount),
		Reason:         reason,
		Status:         VarianceStatusDetected,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// Investigate transitions the variance to INVESTIGATING.
func (v *Variance) Investigate() error {
	if !v.Status.CanTransitionTo(VarianceStatusInvestigating) {
		return ErrInvalidStatusTransition
	}
	v.Status = VarianceStatusInvestigating
	v.UpdatedAt = time.Now().UTC()
	return nil
}

// Dispute transitions the variance to DISPUTED.
func (v *Variance) Dispute() error {
	if !v.Status.CanTransitionTo(VarianceStatusDisputed) {
		return ErrInvalidStatusTransition
	}
	v.Status = VarianceStatusDisputed
	v.UpdatedAt = time.Now().UTC()
	return nil
}

// Resolve transitions the variance to RESOLVED with a note.
func (v *Variance) Resolve(note string, resolvedBy string) error {
	if !v.Status.CanTransitionTo(VarianceStatusResolved) {
		return ErrInvalidStatusTransition
	}
	now := time.Now().UTC()
	v.Status = VarianceStatusResolved
	v.ResolutionNote = note
	v.ResolvedBy = resolvedBy
	v.ResolvedAt = &now
	v.UpdatedAt = now
	return nil
}

// Value transitions the variance to VALUED after valuation engine processing.
func (v *Variance) Value(valueDelta decimal.Decimal, currency string) error {
	if !v.Status.CanTransitionTo(VarianceStatusValued) {
		return ErrInvalidStatusTransition
	}
	v.Status = VarianceStatusValued
	v.ValueDelta = valueDelta
	v.Currency = currency
	v.UpdatedAt = time.Now().UTC()
	return nil
}

// Accept transitions the variance to ACCEPTED (accepted as a known difference).
func (v *Variance) Accept(note string, acceptedBy string) error {
	if !v.Status.CanTransitionTo(VarianceStatusAccepted) {
		return ErrInvalidStatusTransition
	}
	now := time.Now().UTC()
	v.Status = VarianceStatusAccepted
	v.ResolutionNote = note
	v.ResolvedBy = acceptedBy
	v.ResolvedAt = &now
	v.UpdatedAt = now
	return nil
}
