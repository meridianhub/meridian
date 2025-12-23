package domain

import (
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
		{"reconciled can suspend", TransactionStatusReconciled, true},
		{"amended can suspend", TransactionStatusAmended, true},
		{"posted can suspend", TransactionStatusPosted, true},
		{"failed cannot suspend", TransactionStatusFailed, false},
		{"rejected cannot suspend", TransactionStatusRejected, false},
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
		{"can resume to reconciled", TransactionStatusReconciled, true},
		{"can resume to posted", TransactionStatusPosted, true},
		{"can terminate", TransactionStatusTerminated, true},
		{"cannot go to failed", TransactionStatusFailed, false},
		{"cannot go to rejected", TransactionStatusRejected, false},
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
		TransactionStatusReconciled,
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
