package domain

import (
	"time"
)

// StatusTracking captures the lifecycle status of the financial position log.
// It maintains current and previous status along with reasons for changes.
type StatusTracking struct {
	CurrentStatus        TransactionStatus
	PreviousStatus       *TransactionStatus
	StatusUpdatedAt      time.Time
	StatusReason         string
	FailureReason        string
	ReconciliationStatus ReconciliationStatus
}

// NewStatusTracking creates a StatusTracking initialized to the pending state.
// The returned record has CurrentStatus set to TransactionStatusPending, PreviousStatus nil,
// StatusUpdatedAt set to the current UTC time, StatusReason set to "Initial creation",
// FailureReason empty, and ReconciliationStatus set to ReconciliationStatusUnreconciled.
func NewStatusTracking() *StatusTracking {
	return &StatusTracking{
		CurrentStatus:        TransactionStatusPending,
		PreviousStatus:       nil,
		StatusUpdatedAt:      time.Now().UTC(),
		StatusReason:         "Initial creation",
		FailureReason:        "",
		ReconciliationStatus: ReconciliationStatusUnreconciled,
	}
}

// UpdateStatus updates the status with validation for state transitions.
func (s *StatusTracking) UpdateStatus(newStatus TransactionStatus, reason string) error {
	if !s.CurrentStatus.CanTransitionTo(newStatus) {
		return ErrInvalidStatusTransition
	}

	previous := s.CurrentStatus
	s.PreviousStatus = &previous
	s.CurrentStatus = newStatus
	s.StatusUpdatedAt = time.Now().UTC()
	s.StatusReason = reason

	return nil
}

// MarkFailed marks the transaction as failed with a reason.
func (s *StatusTracking) MarkFailed(failureReason string) error {
	if err := s.UpdateStatus(TransactionStatusFailed, "Transaction failed"); err != nil {
		return err
	}
	s.FailureReason = failureReason
	return nil
}

// MarkReconciled updates the reconciliation status.
func (s *StatusTracking) MarkReconciled(reconciliationStatus ReconciliationStatus) {
	s.ReconciliationStatus = reconciliationStatus
	s.StatusUpdatedAt = time.Now().UTC()
}

// IsReconciled returns true if the transaction is reconciled.
func (s *StatusTracking) IsReconciled() bool {
	return s.ReconciliationStatus == ReconciliationStatusMatched ||
		s.ReconciliationStatus == ReconciliationStatusResolved
}
