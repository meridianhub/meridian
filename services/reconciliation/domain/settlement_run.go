package domain

import (
	"time"

	"github.com/google/uuid"
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
