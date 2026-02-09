package domain

import "testing"

func TestAssertionStatus_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		status AssertionStatus
		want   bool
	}{
		{"valid pending", AssertionStatusPending, true},
		{"valid passed", AssertionStatusPassed, true},
		{"valid failed", AssertionStatusFailed, true},
		{"valid override", AssertionStatusOverride, true},
		{"invalid status", AssertionStatus("INVALID"), false},
		{"empty status", AssertionStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("AssertionStatus.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAssertionStatus_IsFinal(t *testing.T) {
	tests := []struct {
		name   string
		status AssertionStatus
		want   bool
	}{
		{"pending not final", AssertionStatusPending, false},
		{"passed is final", AssertionStatusPassed, true},
		{"failed not final", AssertionStatusFailed, false},
		{"override is final", AssertionStatusOverride, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsFinal(); got != tt.want {
				t.Errorf("AssertionStatus.IsFinal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAssertionStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name    string
		current AssertionStatus
		target  AssertionStatus
		want    bool
	}{
		// From PENDING
		{"pending to passed", AssertionStatusPending, AssertionStatusPassed, true},
		{"pending to failed", AssertionStatusPending, AssertionStatusFailed, true},
		{"pending to override not allowed", AssertionStatusPending, AssertionStatusOverride, false},

		// From FAILED
		{"failed to override", AssertionStatusFailed, AssertionStatusOverride, true},
		{"failed to passed not allowed", AssertionStatusFailed, AssertionStatusPassed, false},

		// Final states
		{"passed cannot transition", AssertionStatusPassed, AssertionStatusPending, false},
		{"override cannot transition", AssertionStatusOverride, AssertionStatusPending, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.current.CanTransitionTo(tt.target); got != tt.want {
				t.Errorf("AssertionStatus(%s).CanTransitionTo(%s) = %v, want %v",
					tt.current, tt.target, got, tt.want)
			}
		})
	}
}

func TestParseAssertionStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected AssertionStatus
	}{
		{"valid pending", "PENDING", AssertionStatusPending},
		{"valid passed", "PASSED", AssertionStatusPassed},
		{"valid failed", "FAILED", AssertionStatusFailed},
		{"valid override", "OVERRIDE", AssertionStatusOverride},
		{"invalid defaults to pending", "INVALID", AssertionStatusPending},
		{"empty defaults to pending", "", AssertionStatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseAssertionStatus(tt.input); got != tt.expected {
				t.Errorf("ParseAssertionStatus(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
