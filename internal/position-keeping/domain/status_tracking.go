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

// NewStatusTracking creates a new status tracking record with initial status.
func NewStatusTracking() *StatusTracking {
	return &StatusTracking{
		CurrentStatus:        TransactionStatusPending,
		PreviousStatus:       nil,
		StatusUpdatedAt:      time.Now(),
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
	s.StatusUpdatedAt = time.Now()
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
	s.StatusUpdatedAt = time.Now()
}

// IsReconciled returns true if the transaction is reconciled.
func (s *StatusTracking) IsReconciled() bool {
	return s.ReconciliationStatus == ReconciliationStatusMatched ||
		s.ReconciliationStatus == ReconciliationStatusResolved
}
