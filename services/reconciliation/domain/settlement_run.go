package domain

import (
	"time"

	"github.com/google/uuid"
)

// ReconciliationPhase represents a phase in the reconciliation pipeline.
type ReconciliationPhase string

// Reconciliation pipeline phases in execution order.
const (
	PhaseSnapshotCapture   ReconciliationPhase = "SNAPSHOT_CAPTURE"
	PhaseVarianceDetection ReconciliationPhase = "VARIANCE_DETECTION"
	PhaseVarianceValuation ReconciliationPhase = "VARIANCE_VALUATION"
	PhaseBalanceAssertion  ReconciliationPhase = "BALANCE_ASSERTION"
)

// SettlementRun is the Command Record (CR) that orchestrates a reconciliation run.
// It captures the scope, type, period, and outcome of a reconciliation process.
//
// The Version field implements optimistic concurrency control. The persistence
// layer should use WHERE run_id = ? AND version = ? to detect conflicts.
type SettlementRun struct {
	// RunID is the unique identifier for this settlement run.
	RunID uuid.UUID

	// AccountID identifies the primary account being reconciled.
	AccountID string

	// Scope defines what is being reconciled (account, instrument, portfolio, full).
	Scope ReconciliationScope

	// SettlementType defines the type of settlement (daily, weekly, on-demand, etc.).
	SettlementType SettlementType

	// Status is the current state of the run.
	Status RunStatus

	// PeriodStart is the beginning of the reconciliation period.
	PeriodStart time.Time

	// PeriodEnd is the end of the reconciliation period.
	PeriodEnd time.Time

	// InitiatedBy is the user or system that started the run.
	InitiatedBy string

	// CompletedAt is when the run finished (nil if still in progress).
	CompletedAt *time.Time

	// VarianceCount is the number of variances detected during the run.
	VarianceCount int

	// FailureReason records why the run failed (empty if not failed).
	FailureReason string

	// LastCompletedPhase records the last pipeline phase that completed before a pause.
	// nil when the run has not been paused or no phases have completed.
	LastCompletedPhase *ReconciliationPhase

	// Attributes stores flexible metadata for this run.
	Attributes map[string]string

	// CreatedAt is when this record was created.
	CreatedAt time.Time

	// UpdatedAt is when this record was last updated.
	UpdatedAt time.Time

	// Version is the optimistic lock version, incremented on state changes.
	Version int64
}

// NewSettlementRun creates a new SettlementRun with validation.
func NewSettlementRun(
	accountID string,
	scope ReconciliationScope,
	settlementType SettlementType,
	periodStart time.Time,
	periodEnd time.Time,
	initiatedBy string,
) (*SettlementRun, error) {
	if accountID == "" {
		return nil, ErrEmptyAccountID
	}
	if !scope.IsValid() {
		return nil, ErrEmptyScope
	}
	if !settlementType.IsValid() {
		return nil, ErrEmptySettlementType
	}
	if !periodStart.Before(periodEnd) {
		return nil, ErrInvalidPeriod
	}
	if initiatedBy == "" {
		return nil, ErrEmptyInitiatedBy
	}

	now := time.Now().UTC()
	return &SettlementRun{
		RunID:          uuid.New(),
		AccountID:      accountID,
		Scope:          scope,
		SettlementType: settlementType,
		Status:         RunStatusPending,
		PeriodStart:    periodStart,
		PeriodEnd:      periodEnd,
		InitiatedBy:    initiatedBy,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}, nil
}

// Start transitions the run to RUNNING.
func (r *SettlementRun) Start() error {
	if !r.Status.CanTransitionTo(RunStatusRunning) {
		return ErrInvalidStatusTransition
	}
	r.Status = RunStatusRunning
	r.UpdatedAt = time.Now().UTC()
	r.Version++
	return nil
}

// Complete transitions the run to COMPLETED with a final variance count.
func (r *SettlementRun) Complete(varianceCount int) error {
	if !r.Status.CanTransitionTo(RunStatusCompleted) {
		return ErrInvalidStatusTransition
	}
	now := time.Now().UTC()
	r.Status = RunStatusCompleted
	r.CompletedAt = &now
	r.VarianceCount = varianceCount
	r.UpdatedAt = now
	r.Version++
	return nil
}

// Fail transitions the run to FAILED with a reason.
func (r *SettlementRun) Fail(reason string) error {
	if !r.Status.CanTransitionTo(RunStatusFailed) {
		return ErrInvalidStatusTransition
	}
	now := time.Now().UTC()
	r.Status = RunStatusFailed
	r.CompletedAt = &now
	r.FailureReason = reason
	r.UpdatedAt = now
	r.Version++
	return nil
}

// SetVarianceCount updates the variance count on a running settlement run.
func (r *SettlementRun) SetVarianceCount(count int) {
	r.VarianceCount = count
	r.UpdatedAt = time.Now().UTC()
	r.Version++
}

// Finalize transitions the run from COMPLETED to FINALIZED.
// This indicates that position locks have been acquired and the settlement
// period is sealed for further modifications.
func (r *SettlementRun) Finalize() error {
	if !r.Status.CanTransitionTo(RunStatusFinalized) {
		return ErrInvalidStatusTransition
	}
	now := time.Now().UTC()
	r.Status = RunStatusFinalized
	// Preserve original CompletedAt from the COMPLETED transition;
	// only set if not already present (defensive).
	if r.CompletedAt == nil {
		r.CompletedAt = &now
	}
	r.UpdatedAt = now
	r.Version++
	return nil
}

// IsFinalSettlement returns true if this run's type qualifies for finalization.
func (r *SettlementRun) IsFinalSettlement() bool {
	return r.SettlementType == SettlementTypeFinal
}

// Cancel transitions the run to CANCELLED.
func (r *SettlementRun) Cancel() error {
	if !r.Status.CanTransitionTo(RunStatusCancelled) {
		return ErrInvalidStatusTransition
	}
	now := time.Now().UTC()
	r.Status = RunStatusCancelled
	r.CompletedAt = &now
	r.UpdatedAt = now
	r.Version++
	return nil
}

// Pause transitions the run to PAUSED and records the last completed phase as a checkpoint.
func (r *SettlementRun) Pause(checkpoint ReconciliationPhase) error {
	if !r.Status.CanTransitionTo(RunStatusPaused) {
		return ErrInvalidStatusTransition
	}
	r.Status = RunStatusPaused
	r.LastCompletedPhase = &checkpoint
	r.UpdatedAt = time.Now().UTC()
	r.Version++
	return nil
}

// SetCheckpoint records the last completed pipeline phase on a running settlement run.
func (r *SettlementRun) SetCheckpoint(phase ReconciliationPhase) {
	r.LastCompletedPhase = &phase
	r.UpdatedAt = time.Now().UTC()
	r.Version++
}

// Resume transitions the run from PAUSED back to RUNNING.
// The LastCompletedPhase is preserved so the pipeline can skip already-completed phases.
func (r *SettlementRun) Resume() error {
	if r.Status != RunStatusPaused {
		return ErrInvalidStatusTransition
	}
	r.Status = RunStatusRunning
	r.UpdatedAt = time.Now().UTC()
	r.Version++
	return nil
}
