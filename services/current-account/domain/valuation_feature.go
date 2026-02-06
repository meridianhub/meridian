package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ValuationFeature domain errors
var (
	ErrInvalidValuationFeatureTransition = errors.New("invalid valuation feature lifecycle transition")
	ErrValuationFeatureNotActive         = errors.New("valuation feature is not in active status")
	ErrInvalidValuationFeatureParameters = errors.New("invalid valuation feature parameters")
	ErrInvalidTemporalRange              = errors.New("valid_from must be before valid_to")
	ErrInstrumentCodeEmpty               = errors.New("instrument_code cannot be empty")
)

// ValuationFeatureLifecycleStatus represents the lifecycle state of a valuation feature
type ValuationFeatureLifecycleStatus string

// Valuation feature lifecycle status constants following ADR-012
const (
	ValuationFeatureLifecycleStatusInitiated  ValuationFeatureLifecycleStatus = "INITIATED"
	ValuationFeatureLifecycleStatusActive     ValuationFeatureLifecycleStatus = "ACTIVE"
	ValuationFeatureLifecycleStatusTerminated ValuationFeatureLifecycleStatus = "TERMINATED"
)

// ValuationFeature represents a valuation method assignment to an account.
// It maps an input instrument (e.g., USD) to the account's native instrument (e.g., GBP)
// using a specific valuation method from the Valuation Engine Service.
//
// Invariant: One account can have at most one ACTIVE valuation feature per input instrument.
// Example: Account with native_instrument=GBP can have:
//   - USD→GBP feature (active)
//   - EUR→GBP feature (active)
//     But NOT two active USD→GBP features
//
// Note: Fields are exported for persistence layer access. State transitions should only be
// performed via Activate() and Terminate() methods which enforce the state machine invariants.
// The Version field is a persistence concern exposed here for optimistic locking support.
type ValuationFeature struct {
	ID                     uuid.UUID
	AccountID              uuid.UUID
	InstrumentCode         string                 // Input instrument to be valued
	ValuationMethodID      uuid.UUID              // Reference to Valuation Engine Service method
	ValuationMethodVersion int                    // Method version for immutability
	Parameters             map[string]interface{} // Method-specific parameters (JSON)
	LifecycleStatus        ValuationFeatureLifecycleStatus
	ValidFrom              time.Time // Bi-temporal validity start
	ValidTo                time.Time // Bi-temporal validity end
	CreatedAt              time.Time
	CreatedBy              string
	UpdatedAt              time.Time
	UpdatedBy              string
	Version                int
}

// NewValuationFeature creates a new valuation feature in INITIATED status.
// The feature will need to be activated via Activate() before it can be used.
func NewValuationFeature(
	accountID uuid.UUID,
	instrumentCode string,
	methodID uuid.UUID,
	methodVersion int,
	parameters map[string]interface{},
	createdBy string,
) (*ValuationFeature, error) {
	if instrumentCode == "" {
		return nil, ErrInstrumentCodeEmpty
	}

	now := time.Now()
	maxTime := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

	return &ValuationFeature{
		ID:                     uuid.New(),
		AccountID:              accountID,
		InstrumentCode:         instrumentCode,
		ValuationMethodID:      methodID,
		ValuationMethodVersion: methodVersion,
		Parameters:             parameters,
		LifecycleStatus:        ValuationFeatureLifecycleStatusInitiated,
		ValidFrom:              now,
		ValidTo:                maxTime,
		CreatedAt:              now,
		CreatedBy:              createdBy,
		UpdatedAt:              now,
		UpdatedBy:              createdBy,
		Version:                1,
	}, nil
}

// Activate transitions the valuation feature to ACTIVE status.
// This makes the feature available for use in valuation operations.
// Idempotent: Returns nil if already active.
func (vf *ValuationFeature) Activate(updatedBy string) error {
	if vf.LifecycleStatus == ValuationFeatureLifecycleStatusActive {
		return nil // Idempotent
	}

	if vf.LifecycleStatus != ValuationFeatureLifecycleStatusInitiated {
		return ErrInvalidValuationFeatureTransition
	}

	vf.LifecycleStatus = ValuationFeatureLifecycleStatusActive
	vf.UpdatedAt = time.Now()
	vf.UpdatedBy = updatedBy
	return nil
}

// Terminate transitions the valuation feature to TERMINATED status (terminal state).
// This is called when the feature is no longer needed or being replaced.
// The valid_to timestamp is set to the current time to end bi-temporal validity.
// Idempotent: Returns nil if already terminated.
func (vf *ValuationFeature) Terminate(updatedBy string) error {
	if vf.LifecycleStatus == ValuationFeatureLifecycleStatusTerminated {
		return nil // Idempotent
	}

	if vf.LifecycleStatus != ValuationFeatureLifecycleStatusActive {
		return ErrInvalidValuationFeatureTransition
	}

	now := time.Now()
	vf.LifecycleStatus = ValuationFeatureLifecycleStatusTerminated
	vf.ValidTo = now
	vf.UpdatedAt = now
	vf.UpdatedBy = updatedBy
	return nil
}

// IsActive returns true if the valuation feature is in ACTIVE status
func (vf *ValuationFeature) IsActive() bool {
	return vf.LifecycleStatus == ValuationFeatureLifecycleStatusActive
}

// IsTerminal returns true if the valuation feature is in a terminal state (TERMINATED)
func (vf *ValuationFeature) IsTerminal() bool {
	return vf.LifecycleStatus == ValuationFeatureLifecycleStatusTerminated
}

// IsValidAt returns true if the valuation feature is valid at the given knowledge time
// using bi-temporal validity checking.
func (vf *ValuationFeature) IsValidAt(knowledgeAt time.Time) bool {
	return !knowledgeAt.Before(vf.ValidFrom) && knowledgeAt.Before(vf.ValidTo)
}
