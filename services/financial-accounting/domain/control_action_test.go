package domain

import (
	"errors"
	"testing"
	"time"
)

func TestControlAction_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		action ControlAction
		want   bool
	}{
		{"valid suspend", ControlActionSuspend, true},
		{"valid resume", ControlActionResume, true},
		{"valid terminate", ControlActionTerminate, true},
		{"invalid unspecified", ControlActionUnspecified, false},
		{"invalid empty", ControlAction(""), false},
		{"invalid arbitrary", ControlAction("INVALID"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.action.IsValid(); got != tt.want {
				t.Errorf("ControlAction.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestControlAction_String(t *testing.T) {
	tests := []struct {
		name     string
		action   ControlAction
		expected string
	}{
		{"suspend", ControlActionSuspend, "SUSPEND"},
		{"resume", ControlActionResume, "RESUME"},
		{"terminate", ControlActionTerminate, "TERMINATE"},
		{"unspecified", ControlActionUnspecified, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.action.String(); got != tt.expected {
				t.Errorf("ControlAction.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNewStatusChangeEntry(t *testing.T) {
	beforeCreate := time.Now().UTC()

	entry := NewStatusChangeEntry(
		TransactionStatusPending,
		TransactionStatusSuspended,
		"System maintenance",
		"operator-123",
		ControlActionSuspend,
	)

	afterCreate := time.Now().UTC()

	if entry.PreviousStatus != TransactionStatusPending {
		t.Errorf("Expected PreviousStatus %v, got %v", TransactionStatusPending, entry.PreviousStatus)
	}

	if entry.NewStatus != TransactionStatusSuspended {
		t.Errorf("Expected NewStatus %v, got %v", TransactionStatusSuspended, entry.NewStatus)
	}

	if entry.Reason != "System maintenance" {
		t.Errorf("Expected Reason 'System maintenance', got %v", entry.Reason)
	}

	if entry.OperatorID != "operator-123" {
		t.Errorf("Expected OperatorID 'operator-123', got %v", entry.OperatorID)
	}

	if entry.Action != ControlActionSuspend {
		t.Errorf("Expected Action %v, got %v", ControlActionSuspend, entry.Action)
	}

	if entry.Timestamp.Before(beforeCreate) || entry.Timestamp.After(afterCreate) {
		t.Errorf("Timestamp %v should be between %v and %v", entry.Timestamp, beforeCreate, afterCreate)
	}

	if entry.Timestamp.Location() != time.UTC {
		t.Errorf("Timestamp should be UTC, got %v", entry.Timestamp.Location())
	}
}

func TestTransactionStatus_CanSuspend(t *testing.T) {
	tests := []struct {
		name   string
		status TransactionStatus
		want   bool
	}{
		{"pending can suspend", TransactionStatusPending, true},
		{"posted can suspend", TransactionStatusPosted, true},
		{"failed cannot suspend", TransactionStatusFailed, false},
		{"cancelled cannot suspend", TransactionStatusCancelled, false},
		{"reversed cannot suspend", TransactionStatusReversed, false},
		{"suspended cannot suspend again", TransactionStatusSuspended, false},
		{"terminated cannot suspend", TransactionStatusTerminated, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.CanSuspend(); got != tt.want {
				t.Errorf("TransactionStatus.CanSuspend() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransactionStatus_CanResume(t *testing.T) {
	tests := []struct {
		name   string
		status TransactionStatus
		want   bool
	}{
		{"suspended can resume", TransactionStatusSuspended, true},
		{"pending cannot resume", TransactionStatusPending, false},
		{"posted cannot resume", TransactionStatusPosted, false},
		{"terminated cannot resume", TransactionStatusTerminated, false},
		{"failed cannot resume", TransactionStatusFailed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.CanResume(); got != tt.want {
				t.Errorf("TransactionStatus.CanResume() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransactionStatus_CanTerminate(t *testing.T) {
	tests := []struct {
		name   string
		status TransactionStatus
		want   bool
	}{
		{"suspended can terminate", TransactionStatusSuspended, true},
		{"pending cannot terminate", TransactionStatusPending, false},
		{"posted cannot terminate", TransactionStatusPosted, false},
		{"terminated cannot terminate again", TransactionStatusTerminated, false},
		{"failed cannot terminate", TransactionStatusFailed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.CanTerminate(); got != tt.want {
				t.Errorf("TransactionStatus.CanTerminate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransactionStatus_SuspendedTransitions(t *testing.T) {
	suspended := TransactionStatusSuspended

	tests := []struct {
		name   string
		target TransactionStatus
		want   bool
	}{
		{"can resume to pending", TransactionStatusPending, true},
		{"can resume to posted", TransactionStatusPosted, true},
		{"can terminate", TransactionStatusTerminated, true},
		{"cannot go to failed", TransactionStatusFailed, false},
		{"cannot go to cancelled", TransactionStatusCancelled, false},
		{"cannot stay suspended", TransactionStatusSuspended, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suspended.CanTransitionTo(tt.target); got != tt.want {
				t.Errorf("SUSPENDED.CanTransitionTo(%v) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestTransactionStatus_TerminatedIsFinal(t *testing.T) {
	terminated := TransactionStatusTerminated

	if !terminated.IsFinal() {
		t.Error("TERMINATED should be a final state")
	}

	// TERMINATED cannot transition to any state
	targets := []TransactionStatus{
		TransactionStatusPending,
		TransactionStatusPosted,
		TransactionStatusFailed,
		TransactionStatusSuspended,
	}

	for _, target := range targets {
		if terminated.CanTransitionTo(target) {
			t.Errorf("TERMINATED should not be able to transition to %v", target)
		}
	}
}

func TestFinancialBookingLog_ControlLog_Suspend(t *testing.T) {
	t.Run("suspend from pending", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)

		updated, err := log.ControlLog(ControlActionSuspend, "System maintenance", "operator-123")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Original should be unchanged (immutability)
		if log.Status != TransactionStatusPending {
			t.Error("Original log should remain PENDING")
		}

		// Updated should be suspended
		if updated.Status != TransactionStatusSuspended {
			t.Errorf("Expected status SUSPENDED, got %v", updated.Status)
		}

		if updated.StatusHistoryCount() != 1 {
			t.Errorf("Expected 1 status history entry, got %d", updated.StatusHistoryCount())
		}

		history := updated.StatusHistory()
		if history[0].Action != ControlActionSuspend {
			t.Errorf("Expected action SUSPEND, got %v", history[0].Action)
		}
	})

	t.Run("cannot suspend from failed", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)
		failedLog := log.WithStatus(TransactionStatusFailed)

		_, err := failedLog.ControlLog(ControlActionSuspend, "Cannot happen", "operator-123")
		if !errors.Is(err, ErrCannotSuspend) {
			t.Errorf("Expected ErrCannotSuspend, got: %v", err)
		}
	})

	t.Run("cannot suspend with empty operator ID", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)

		_, err := log.ControlLog(ControlActionSuspend, "Reason", "")
		if !errors.Is(err, ErrEmptyOperatorID) {
			t.Errorf("Expected ErrEmptyOperatorID, got: %v", err)
		}
	})
}

func TestFinancialBookingLog_ControlLog_Resume(t *testing.T) {
	t.Run("resume to original status", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)

		// Suspend first
		suspended, err := log.ControlLog(ControlActionSuspend, "Maintenance", "operator-1")
		if err != nil {
			t.Fatalf("Failed to suspend: %v", err)
		}

		// Resume
		resumed, err := suspended.ControlLog(ControlActionResume, "Maintenance complete", "operator-2")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if resumed.Status != TransactionStatusPending {
			t.Errorf("Expected status PENDING (original), got %v", resumed.Status)
		}

		if resumed.StatusHistoryCount() != 2 {
			t.Errorf("Expected 2 status history entries, got %d", resumed.StatusHistoryCount())
		}
	})

	t.Run("cannot resume from non-suspended state", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)

		_, err := log.ControlLog(ControlActionResume, "Cannot happen", "operator-123")
		if !errors.Is(err, ErrCannotResume) {
			t.Errorf("Expected ErrCannotResume, got: %v", err)
		}
	})
}

func TestFinancialBookingLog_ControlLog_Terminate(t *testing.T) {
	t.Run("terminate from suspended", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)

		// Suspend first
		suspended, _ := log.ControlLog(ControlActionSuspend, "Maintenance", "operator-1")

		// Terminate
		terminated, err := suspended.ControlLog(ControlActionTerminate, "No longer needed", "operator-2")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if terminated.Status != TransactionStatusTerminated {
			t.Errorf("Expected status TERMINATED, got %v", terminated.Status)
		}

		if !terminated.IsTerminated() {
			t.Error("Expected IsTerminated() to return true")
		}
	})

	t.Run("cannot terminate from non-suspended state", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)

		_, err := log.ControlLog(ControlActionTerminate, "Cannot happen", "operator-123")
		if !errors.Is(err, ErrCannotTerminate) {
			t.Errorf("Expected ErrCannotTerminate, got: %v", err)
		}
	})

	t.Run("terminated is truly terminal", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)

		// Suspend and terminate
		suspended, _ := log.Suspend("Maintenance", "operator-1")
		terminated, _ := suspended.Terminate("No longer needed", "operator-2")

		// Try to suspend again
		_, err := terminated.ControlLog(ControlActionSuspend, "Should fail", "operator-3")
		if !errors.Is(err, ErrAlreadyTerminated) {
			t.Errorf("Expected ErrAlreadyTerminated, got: %v", err)
		}

		// Try to resume
		_, err = terminated.ControlLog(ControlActionResume, "Should fail", "operator-3")
		if !errors.Is(err, ErrAlreadyTerminated) {
			t.Errorf("Expected ErrAlreadyTerminated, got: %v", err)
		}
	})
}

func TestFinancialBookingLog_ControlLog_InvalidAction(t *testing.T) {
	log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)

	_, err := log.ControlLog(ControlAction("INVALID"), "Reason", "operator-123")
	if !errors.Is(err, ErrInvalidControlAction) {
		t.Errorf("Expected ErrInvalidControlAction, got: %v", err)
	}

	_, err = log.ControlLog(ControlActionUnspecified, "Reason", "operator-123")
	if !errors.Is(err, ErrInvalidControlAction) {
		t.Errorf("Expected ErrInvalidControlAction for unspecified, got: %v", err)
	}
}

func TestFinancialBookingLog_ConvenienceMethods(t *testing.T) {
	t.Run("Suspend convenience method", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)
		suspended, err := log.Suspend("Maintenance", "operator-1")
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if !suspended.IsSuspended() {
			t.Error("Expected log to be suspended")
		}
	})

	t.Run("Resume convenience method", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)
		suspended, _ := log.Suspend("Maintenance", "operator-1")
		resumed, err := suspended.Resume("Complete", "operator-2")
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if resumed.IsSuspended() {
			t.Error("Expected log to not be suspended after resume")
		}
	})

	t.Run("Terminate convenience method", func(t *testing.T) {
		log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)
		suspended, _ := log.Suspend("Maintenance", "operator-1")
		terminated, err := suspended.Terminate("No longer needed", "operator-2")
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if !terminated.IsTerminated() {
			t.Error("Expected log to be terminated")
		}
	})
}

func TestFinancialBookingLog_StatusHistory_AuditTrail(t *testing.T) {
	log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)

	// Perform a series of control actions (chaining immutable updates)
	step1, _ := log.Suspend("First suspension", "op-1")
	step2, _ := step1.Resume("First resume", "op-2")
	step3, _ := step2.Suspend("Second suspension", "op-3")
	final, _ := step3.Terminate("Final termination", "op-4")

	history := final.StatusHistory()

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

	// Verify last entry is termination
	last := history[len(history)-1]
	if last.NewStatus != TransactionStatusTerminated {
		t.Errorf("Last entry should be TERMINATED, got %v", last.NewStatus)
	}
}

func TestFinancialBookingLog_StatusHistory_DefensiveCopy(t *testing.T) {
	log := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)
	suspended, _ := log.Suspend("Test", "op-1")

	firstCopy := suspended.StatusHistory()
	secondCopy := suspended.StatusHistory()

	// Modify first copy
	firstCopy[0] = nil

	// Second copy should be unaffected
	if secondCopy[0] == nil {
		t.Error("Modifying one copy should not affect another")
	}
}

func TestFinancialBookingLog_Immutability(t *testing.T) {
	original := NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", CurrencyGBP)
	originalID := original.ID

	suspended, _ := original.Suspend("Maintenance", "op-1")
	resumed, _ := suspended.Resume("Complete", "op-2")

	// Original should be completely unchanged
	if original.Status != TransactionStatusPending {
		t.Error("Original status should remain PENDING")
	}
	if original.StatusHistoryCount() != 0 {
		t.Error("Original should have no status history")
	}

	// Suspended should have 1 entry
	if suspended.Status != TransactionStatusSuspended {
		t.Error("Suspended should be SUSPENDED")
	}
	if suspended.StatusHistoryCount() != 1 {
		t.Error("Suspended should have 1 history entry")
	}

	// Resumed should have 2 entries
	if resumed.Status != TransactionStatusPending {
		t.Error("Resumed should be PENDING")
	}
	if resumed.StatusHistoryCount() != 2 {
		t.Error("Resumed should have 2 history entries")
	}

	// All should have same ID
	if suspended.ID != originalID || resumed.ID != originalID {
		t.Error("ID should be preserved through all transformations")
	}
}
