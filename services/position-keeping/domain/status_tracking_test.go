package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewStatusTracking_Initialization(t *testing.T) {
	t.Parallel()

	before := time.Now().UTC()
	st := NewStatusTracking()
	after := time.Now().UTC()

	if st.CurrentStatus != TransactionStatusPending {
		t.Errorf("Expected CurrentStatus to be PENDING, got: %v", st.CurrentStatus)
	}

	if st.PreviousStatus != nil {
		t.Errorf("Expected PreviousStatus to be nil, got: %v", st.PreviousStatus)
	}

	if st.StatusUpdatedAt.Before(before) || st.StatusUpdatedAt.After(after) {
		t.Errorf("Expected StatusUpdatedAt to be between %v and %v, got: %v", before, after, st.StatusUpdatedAt)
	}

	if st.StatusReason != "Initial creation" {
		t.Errorf("Expected StatusReason to be 'Initial creation', got: %v", st.StatusReason)
	}

	if st.FailureReason != "" {
		t.Errorf("Expected FailureReason to be empty, got: %v", st.FailureReason)
	}

	if st.ReconciliationStatus != ReconciliationStatusUnreconciled {
		t.Errorf("Expected ReconciliationStatus to be UNRECONCILED, got: %v", st.ReconciliationStatus)
	}
}

func TestStatusTracking_UpdateStatus_ValidTransition(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()

	err := st.UpdateStatus(TransactionStatusPosted, "Transaction posted to ledger")
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if st.CurrentStatus != TransactionStatusPosted {
		t.Errorf("Expected CurrentStatus to be POSTED, got: %v", st.CurrentStatus)
	}

	if st.PreviousStatus == nil {
		t.Error("Expected PreviousStatus to be set")
	} else if *st.PreviousStatus != TransactionStatusPending {
		t.Errorf("Expected PreviousStatus to be PENDING, got: %v", *st.PreviousStatus)
	}

	if st.StatusReason != "Transaction posted to ledger" {
		t.Errorf("Expected StatusReason to be 'Transaction posted to ledger', got: %v", st.StatusReason)
	}
}

func TestStatusTracking_UpdateStatus_InvalidTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		initialStatus TransactionStatus
		targetStatus  TransactionStatus
		setupFunc     func(*StatusTracking) error
	}{
		{
			name:          "PENDING to REVERSED",
			initialStatus: TransactionStatusPending,
			targetStatus:  TransactionStatusReversed,
			setupFunc:     nil,
		},
		{
			name:          "FAILED to POSTED",
			initialStatus: TransactionStatusFailed,
			targetStatus:  TransactionStatusPosted,
			setupFunc: func(st *StatusTracking) error {
				return st.UpdateStatus(TransactionStatusFailed, "Failed")
			},
		},
		{
			name:          "CANCELLED to POSTED",
			initialStatus: TransactionStatusCancelled,
			targetStatus:  TransactionStatusPosted,
			setupFunc: func(st *StatusTracking) error {
				return st.UpdateStatus(TransactionStatusCancelled, "Cancelled")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := NewStatusTracking()

			if tt.setupFunc != nil {
				if err := tt.setupFunc(st); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			err := st.UpdateStatus(tt.targetStatus, "Invalid transition")

			if !errors.Is(err, ErrInvalidStatusTransition) {
				t.Errorf("Expected ErrInvalidStatusTransition, got: %v", err)
			}
		})
	}
}

func TestStatusTracking_UpdateStatus_UpdatesPreviousStatusCorrectly(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()

	// First transition: PENDING -> RECONCILED
	err := st.UpdateStatus(TransactionStatusReconciled, "Reconciled")
	if err != nil {
		t.Fatalf("First transition failed: %v", err)
	}

	if st.CurrentStatus != TransactionStatusReconciled {
		t.Errorf("Expected CurrentStatus to be RECONCILED, got: %v", st.CurrentStatus)
	}

	if st.PreviousStatus == nil || *st.PreviousStatus != TransactionStatusPending {
		t.Error("Expected PreviousStatus to be PENDING after first transition")
	}

	// Second transition: RECONCILED -> POSTED
	err = st.UpdateStatus(TransactionStatusPosted, "Posted")
	if err != nil {
		t.Fatalf("Second transition failed: %v", err)
	}

	if st.CurrentStatus != TransactionStatusPosted {
		t.Errorf("Expected CurrentStatus to be POSTED, got: %v", st.CurrentStatus)
	}

	if st.PreviousStatus == nil || *st.PreviousStatus != TransactionStatusReconciled {
		t.Error("Expected PreviousStatus to be RECONCILED after second transition")
	}
}

func TestStatusTracking_MarkFailed_Behavior(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()

	failureReason := "Network timeout during posting"
	err := st.MarkFailed(failureReason)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if st.CurrentStatus != TransactionStatusFailed {
		t.Errorf("Expected CurrentStatus to be FAILED, got: %v", st.CurrentStatus)
	}

	if st.FailureReason != failureReason {
		t.Errorf("Expected FailureReason to be '%s', got: %v", failureReason, st.FailureReason)
	}

	if st.StatusReason != "Transaction failed" {
		t.Errorf("Expected StatusReason to be 'Transaction failed', got: %v", st.StatusReason)
	}

	if st.PreviousStatus == nil || *st.PreviousStatus != TransactionStatusPending {
		t.Error("Expected PreviousStatus to be PENDING")
	}
}

func TestStatusTracking_MarkReconciled_WithMatched(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()

	before := time.Now().UTC()
	st.MarkReconciled(ReconciliationStatusMatched)
	after := time.Now().UTC()

	if st.ReconciliationStatus != ReconciliationStatusMatched {
		t.Errorf("Expected ReconciliationStatus to be MATCHED, got: %v", st.ReconciliationStatus)
	}

	if st.StatusUpdatedAt.Before(before) || st.StatusUpdatedAt.After(after) {
		t.Errorf("Expected StatusUpdatedAt to be updated between %v and %v, got: %v", before, after, st.StatusUpdatedAt)
	}
}

func TestStatusTracking_MarkReconciled_WithResolved(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()

	st.MarkReconciled(ReconciliationStatusResolved)

	if st.ReconciliationStatus != ReconciliationStatusResolved {
		t.Errorf("Expected ReconciliationStatus to be RESOLVED, got: %v", st.ReconciliationStatus)
	}
}

func TestStatusTracking_MarkReconciled_WithMismatched(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()

	st.MarkReconciled(ReconciliationStatusMismatched)

	if st.ReconciliationStatus != ReconciliationStatusMismatched {
		t.Errorf("Expected ReconciliationStatus to be MISMATCHED, got: %v", st.ReconciliationStatus)
	}
}

func TestStatusTracking_MarkReconciled_WithUnreconciled(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()
	st.MarkReconciled(ReconciliationStatusMatched)

	// Mark back as unreconciled
	st.MarkReconciled(ReconciliationStatusUnreconciled)

	if st.ReconciliationStatus != ReconciliationStatusUnreconciled {
		t.Errorf("Expected ReconciliationStatus to be UNRECONCILED, got: %v", st.ReconciliationStatus)
	}
}

func TestStatusTracking_IsReconciled_WithMatched(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()
	st.MarkReconciled(ReconciliationStatusMatched)

	if !st.IsReconciled() {
		t.Error("Expected IsReconciled to be true for MATCHED status")
	}
}

func TestStatusTracking_IsReconciled_WithResolved(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()
	st.MarkReconciled(ReconciliationStatusResolved)

	if !st.IsReconciled() {
		t.Error("Expected IsReconciled to be true for RESOLVED status")
	}
}

func TestStatusTracking_IsReconciled_WithUnreconciled(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()

	if st.IsReconciled() {
		t.Error("Expected IsReconciled to be false for UNRECONCILED status")
	}
}

func TestStatusTracking_IsReconciled_WithMismatched(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()
	st.MarkReconciled(ReconciliationStatusMismatched)

	if st.IsReconciled() {
		t.Error("Expected IsReconciled to be false for MISMATCHED status")
	}
}

func TestStatusTracking_TimestampsAreUTC(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()

	if st.StatusUpdatedAt.Location() != time.UTC {
		t.Errorf("Expected initial StatusUpdatedAt to be UTC, got: %v", st.StatusUpdatedAt.Location())
	}

	err := st.UpdateStatus(TransactionStatusPosted, "Posted")
	if err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}

	if st.StatusUpdatedAt.Location() != time.UTC {
		t.Errorf("Expected StatusUpdatedAt after UpdateStatus to be UTC, got: %v", st.StatusUpdatedAt.Location())
	}

	st.MarkReconciled(ReconciliationStatusMatched)

	if st.StatusUpdatedAt.Location() != time.UTC {
		t.Errorf("Expected StatusUpdatedAt after MarkReconciled to be UTC, got: %v", st.StatusUpdatedAt.Location())
	}
}

func TestStatusTracking_DoubleTransition_SameStatus(t *testing.T) {
	t.Parallel()

	st := NewStatusTracking()

	// First transition to FAILED
	err := st.MarkFailed("First failure")
	if err != nil {
		t.Fatalf("First MarkFailed failed: %v", err)
	}

	// Attempt to transition to FAILED again (should fail)
	err = st.MarkFailed("Second failure")
	if !errors.Is(err, ErrInvalidStatusTransition) {
		t.Errorf("Expected ErrInvalidStatusTransition for double FAILED transition, got: %v", err)
	}

	// Verify status hasn't changed
	if st.CurrentStatus != TransactionStatusFailed {
		t.Errorf("Expected CurrentStatus to still be FAILED, got: %v", st.CurrentStatus)
	}

	if st.FailureReason != "First failure" {
		t.Errorf("Expected FailureReason to be 'First failure', got: %v", st.FailureReason)
	}
}
