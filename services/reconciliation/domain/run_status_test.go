package domain

import "testing"

func TestRunStatus_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		status RunStatus
		want   bool
	}{
		{"valid pending", RunStatusPending, true},
		{"valid running", RunStatusRunning, true},
		{"valid completed", RunStatusCompleted, true},
		{"valid failed", RunStatusFailed, true},
		{"valid cancelled", RunStatusCancelled, true},
		{"valid finalized", RunStatusFinalized, true},
		{"invalid status", RunStatus("INVALID"), false},
		{"empty status", RunStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("RunStatus.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunStatus_IsFinal(t *testing.T) {
	tests := []struct {
		name   string
		status RunStatus
		want   bool
	}{
		{"pending not final", RunStatusPending, false},
		{"running not final", RunStatusRunning, false},
		{"completed not final", RunStatusCompleted, false},
		{"finalized is final", RunStatusFinalized, true},
		{"failed is final", RunStatusFailed, true},
		{"cancelled is final", RunStatusCancelled, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsFinal(); got != tt.want {
				t.Errorf("RunStatus.IsFinal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name    string
		current RunStatus
		target  RunStatus
		want    bool
	}{
		// Valid transitions from PENDING
		{"pending to running", RunStatusPending, RunStatusRunning, true},
		{"pending to cancelled", RunStatusPending, RunStatusCancelled, true},

		// Invalid transitions from PENDING
		{"pending to completed", RunStatusPending, RunStatusCompleted, false},
		{"pending to failed", RunStatusPending, RunStatusFailed, false},

		// Valid transitions from RUNNING
		{"running to completed", RunStatusRunning, RunStatusCompleted, true},
		{"running to failed", RunStatusRunning, RunStatusFailed, true},
		{"running to cancelled", RunStatusRunning, RunStatusCancelled, true},

		// Invalid transitions from RUNNING
		{"running to pending", RunStatusRunning, RunStatusPending, false},

		// Valid transitions from COMPLETED
		{"completed to finalized", RunStatusCompleted, RunStatusFinalized, true},

		// Invalid transitions from COMPLETED
		{"completed to pending", RunStatusCompleted, RunStatusPending, false},
		{"completed to running", RunStatusCompleted, RunStatusRunning, false},

		// Final states cannot transition
		{"finalized cannot transition", RunStatusFinalized, RunStatusPending, false},
		{"failed cannot transition", RunStatusFailed, RunStatusPending, false},
		{"cancelled cannot transition", RunStatusCancelled, RunStatusPending, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.current.CanTransitionTo(tt.target); got != tt.want {
				t.Errorf("RunStatus(%s).CanTransitionTo(%s) = %v, want %v",
					tt.current, tt.target, got, tt.want)
			}
		})
	}
}

func TestRunStatus_String(t *testing.T) {
	tests := []struct {
		name     string
		status   RunStatus
		expected string
	}{
		{"pending", RunStatusPending, "PENDING"},
		{"running", RunStatusRunning, "RUNNING"},
		{"completed", RunStatusCompleted, "COMPLETED"},
		{"failed", RunStatusFailed, "FAILED"},
		{"cancelled", RunStatusCancelled, "CANCELLED"},
		{"finalized", RunStatusFinalized, "FINALIZED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("RunStatus.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestParseRunStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected RunStatus
	}{
		{"valid pending", "PENDING", RunStatusPending},
		{"valid running", "RUNNING", RunStatusRunning},
		{"valid completed", "COMPLETED", RunStatusCompleted},
		{"invalid defaults to pending", "INVALID", RunStatusPending},
		{"empty defaults to pending", "", RunStatusPending},
		{"lowercase defaults to pending", "pending", RunStatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseRunStatus(tt.input); got != tt.expected {
				t.Errorf("ParseRunStatus(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
