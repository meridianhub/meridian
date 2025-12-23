package domain

import (
	"errors"
	"testing"
)

func TestFinancialPositionLog_ControlLog_Suspend(t *testing.T) {
	t.Run("suspend from pending", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", nil, nil)
		if err != nil {
			t.Fatalf("Failed to create log: %v", err)
		}

		initialVersion := log.Version

		err = log.ControlLog(ControlActionSuspend, "System maintenance", "operator-123")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if log.StatusTracking.CurrentStatus != TransactionStatusSuspended {
			t.Errorf("Expected status SUSPENDED, got %v", log.StatusTracking.CurrentStatus)
		}

		if log.PreSuspendStatus == nil || *log.PreSuspendStatus != TransactionStatusPending {
			t.Error("Expected PreSuspendStatus to be PENDING")
		}

		if log.Version != initialVersion+1 {
			t.Errorf("Expected version %d, got %d", initialVersion+1, log.Version)
		}

		if log.StatusHistoryCount() != 1 {
			t.Errorf("Expected 1 status history entry, got %d", log.StatusHistoryCount())
		}

		history := log.GetStatusHistory()
		if history[0].Action != ControlActionSuspend {
			t.Errorf("Expected action SUSPEND, got %v", history[0].Action)
		}
		if history[0].OperatorID != "operator-123" {
			t.Errorf("Expected operator 'operator-123', got %v", history[0].OperatorID)
		}
	})

	t.Run("suspend from posted", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", nil, nil)
		if err != nil {
			t.Fatalf("Failed to create log: %v", err)
		}

		// Move to posted state
		err = log.StatusTracking.UpdateStatus(TransactionStatusPosted, "All balanced")
		if err != nil {
			t.Fatalf("Failed to update status: %v", err)
		}

		err = log.ControlLog(ControlActionSuspend, "Investigation required", "operator-456")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if log.StatusTracking.CurrentStatus != TransactionStatusSuspended {
			t.Errorf("Expected status SUSPENDED, got %v", log.StatusTracking.CurrentStatus)
		}

		if log.PreSuspendStatus == nil || *log.PreSuspendStatus != TransactionStatusPosted {
			t.Error("Expected PreSuspendStatus to be POSTED")
		}
	})

	t.Run("cannot suspend from failed", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", nil, nil)
		if err != nil {
			t.Fatalf("Failed to create log: %v", err)
		}

		// Move to failed state
		err = log.StatusTracking.MarkFailed("Validation error")
		if err != nil {
			t.Fatalf("Failed to update status: %v", err)
		}

		err = log.ControlLog(ControlActionSuspend, "Cannot happen", "operator-123")
		if !errors.Is(err, ErrCannotSuspend) {
			t.Errorf("Expected ErrCannotSuspend, got: %v", err)
		}
	})

	t.Run("cannot suspend with empty operator ID", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", nil, nil)
		if err != nil {
			t.Fatalf("Failed to create log: %v", err)
		}

		err = log.ControlLog(ControlActionSuspend, "Reason", "")
		if !errors.Is(err, ErrEmptyOperatorID) {
			t.Errorf("Expected ErrEmptyOperatorID, got: %v", err)
		}
	})
}

func TestFinancialPositionLog_ControlLog_Resume(t *testing.T) {
	t.Run("resume to original status", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", nil, nil)
		if err != nil {
			t.Fatalf("Failed to create log: %v", err)
		}

		// Suspend first
		err = log.ControlLog(ControlActionSuspend, "Maintenance", "operator-1")
		if err != nil {
			t.Fatalf("Failed to suspend: %v", err)
		}

		// Resume
		err = log.ControlLog(ControlActionResume, "Maintenance complete", "operator-2")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if log.StatusTracking.CurrentStatus != TransactionStatusPending {
			t.Errorf("Expected status PENDING (original), got %v", log.StatusTracking.CurrentStatus)
		}

		if log.PreSuspendStatus != nil {
			t.Error("Expected PreSuspendStatus to be nil after resume")
		}

		if log.StatusHistoryCount() != 2 {
			t.Errorf("Expected 2 status history entries, got %d", log.StatusHistoryCount())
		}
	})

	t.Run("cannot resume from non-suspended state", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", nil, nil)
		if err != nil {
			t.Fatalf("Failed to create log: %v", err)
		}

		err = log.ControlLog(ControlActionResume, "Cannot happen", "operator-123")
		if !errors.Is(err, ErrCannotResume) {
			t.Errorf("Expected ErrCannotResume, got: %v", err)
		}
	})
}

func TestFinancialPositionLog_ControlLog_Terminate(t *testing.T) {
	t.Run("terminate from suspended", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", nil, nil)
		if err != nil {
			t.Fatalf("Failed to create log: %v", err)
		}

		// Suspend first
		err = log.ControlLog(ControlActionSuspend, "Maintenance", "operator-1")
		if err != nil {
			t.Fatalf("Failed to suspend: %v", err)
		}

		// Terminate
		err = log.ControlLog(ControlActionTerminate, "No longer needed", "operator-2")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if log.StatusTracking.CurrentStatus != TransactionStatusTerminated {
			t.Errorf("Expected status TERMINATED, got %v", log.StatusTracking.CurrentStatus)
		}

		if !log.IsTerminated() {
			t.Error("Expected IsTerminated() to return true")
		}

		if log.StatusHistoryCount() != 2 {
			t.Errorf("Expected 2 status history entries, got %d", log.StatusHistoryCount())
		}
	})

	t.Run("cannot terminate from non-suspended state", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", nil, nil)
		if err != nil {
			t.Fatalf("Failed to create log: %v", err)
		}

		err = log.ControlLog(ControlActionTerminate, "Cannot happen", "operator-123")
		if !errors.Is(err, ErrCannotTerminate) {
			t.Errorf("Expected ErrCannotTerminate, got: %v", err)
		}
	})

	t.Run("terminated is truly terminal", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", nil, nil)
		if err != nil {
			t.Fatalf("Failed to create log: %v", err)
		}

		// Suspend and terminate
		_ = log.ControlLog(ControlActionSuspend, "Maintenance", "operator-1")
		_ = log.ControlLog(ControlActionTerminate, "No longer needed", "operator-2")

		// Try to suspend again
		err = log.ControlLog(ControlActionSuspend, "Should fail", "operator-3")
		if !errors.Is(err, ErrAlreadyTerminated) {
			t.Errorf("Expected ErrAlreadyTerminated, got: %v", err)
		}

		// Try to resume
		err = log.ControlLog(ControlActionResume, "Should fail", "operator-3")
		if !errors.Is(err, ErrAlreadyTerminated) {
			t.Errorf("Expected ErrAlreadyTerminated, got: %v", err)
		}

		// Try to terminate again
		err = log.ControlLog(ControlActionTerminate, "Should fail", "operator-3")
		if !errors.Is(err, ErrAlreadyTerminated) {
			t.Errorf("Expected ErrAlreadyTerminated, got: %v", err)
		}
	})
}

func TestFinancialPositionLog_ControlLog_InvalidAction(t *testing.T) {
	log, err := NewFinancialPositionLog("ACC-001", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create log: %v", err)
	}

	err = log.ControlLog(ControlAction("INVALID"), "Reason", "operator-123")
	if !errors.Is(err, ErrInvalidControlAction) {
		t.Errorf("Expected ErrInvalidControlAction, got: %v", err)
	}

	err = log.ControlLog(ControlActionUnspecified, "Reason", "operator-123")
	if !errors.Is(err, ErrInvalidControlAction) {
		t.Errorf("Expected ErrInvalidControlAction for unspecified, got: %v", err)
	}
}

func TestFinancialPositionLog_ConvenienceMethods(t *testing.T) {
	t.Run("Suspend convenience method", func(t *testing.T) {
		log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
		err := log.Suspend("Maintenance", "operator-1")
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if !log.IsSuspended() {
			t.Error("Expected log to be suspended")
		}
	})

	t.Run("Resume convenience method", func(t *testing.T) {
		log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
		_ = log.Suspend("Maintenance", "operator-1")
		err := log.Resume("Complete", "operator-2")
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if log.IsSuspended() {
			t.Error("Expected log to not be suspended after resume")
		}
	})

	t.Run("Terminate convenience method", func(t *testing.T) {
		log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
		_ = log.Suspend("Maintenance", "operator-1")
		err := log.Terminate("No longer needed", "operator-2")
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if !log.IsTerminated() {
			t.Error("Expected log to be terminated")
		}
	})
}

func TestFinancialPositionLog_StatusHistory_AuditTrail(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)

	// Perform a series of control actions
	_ = log.Suspend("First suspension", "op-1")
	_ = log.Resume("First resume", "op-2")
	_ = log.Suspend("Second suspension", "op-3")
	_ = log.Terminate("Final termination", "op-4")

	history := log.GetStatusHistory()

	if len(history) != 4 {
		t.Fatalf("Expected 4 history entries, got %d", len(history))
	}

	// Verify first entry
	if history[0].PreviousStatus != TransactionStatusPending {
		t.Errorf("Entry 0: expected previous PENDING, got %v", history[0].PreviousStatus)
	}
	if history[0].NewStatus != TransactionStatusSuspended {
		t.Errorf("Entry 0: expected new SUSPENDED, got %v", history[0].NewStatus)
	}
	if history[0].Action != ControlActionSuspend {
		t.Errorf("Entry 0: expected action SUSPEND, got %v", history[0].Action)
	}
	if history[0].OperatorID != "op-1" {
		t.Errorf("Entry 0: expected operator 'op-1', got %v", history[0].OperatorID)
	}

	// Verify timestamps are in order
	for i := 1; i < len(history); i++ {
		if history[i].Timestamp.Before(history[i-1].Timestamp) {
			t.Errorf("Entry %d timestamp should be after entry %d", i, i-1)
		}
	}

	// Verify last entry is termination
	last := history[len(history)-1]
	if last.NewStatus != TransactionStatusTerminated {
		t.Errorf("Last entry should be TERMINATED, got %v", last.NewStatus)
	}
}

func TestFinancialPositionLog_GetStatusHistory_DefensiveCopy(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	_ = log.Suspend("Test", "op-1")

	firstCopy := log.GetStatusHistory()
	secondCopy := log.GetStatusHistory()

	// Modify first copy
	firstCopy[0] = nil

	// Second copy should be unaffected
	if secondCopy[0] == nil {
		t.Error("Modifying one copy should not affect another")
	}
}

func TestFinancialPositionLog_CanTransitionTo(t *testing.T) {
	t.Run("valid transition returns nil", func(t *testing.T) {
		log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
		err := log.CanTransitionTo(TransactionStatusSuspended)
		if err != nil {
			t.Errorf("Expected no error for valid transition, got: %v", err)
		}
	})

	t.Run("invalid transition returns error", func(t *testing.T) {
		log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
		err := log.CanTransitionTo(TransactionStatusReversed)
		if !errors.Is(err, ErrInvalidStatusTransition) {
			t.Errorf("Expected ErrInvalidStatusTransition, got: %v", err)
		}
	})

	t.Run("terminated returns ErrAlreadyTerminated", func(t *testing.T) {
		log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
		_ = log.Suspend("Test", "op-1")
		_ = log.Terminate("Test", "op-1")

		err := log.CanTransitionTo(TransactionStatusPending)
		if !errors.Is(err, ErrAlreadyTerminated) {
			t.Errorf("Expected ErrAlreadyTerminated, got: %v", err)
		}
	})
}
