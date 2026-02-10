package domain

import "testing"

func TestDisputeStatus_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		status DisputeStatus
		want   bool
	}{
		{"valid open", DisputeStatusOpen, true},
		{"valid under review", DisputeStatusUnderReview, true},
		{"valid escalated", DisputeStatusEscalated, true},
		{"valid resolved", DisputeStatusResolved, true},
		{"valid rejected", DisputeStatusRejected, true},
		{"invalid status", DisputeStatus("INVALID"), false},
		{"empty status", DisputeStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("DisputeStatus.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDisputeStatus_IsFinal(t *testing.T) {
	tests := []struct {
		name   string
		status DisputeStatus
		want   bool
	}{
		{"open not final", DisputeStatusOpen, false},
		{"under review not final", DisputeStatusUnderReview, false},
		{"escalated not final", DisputeStatusEscalated, false},
		{"resolved is final", DisputeStatusResolved, true},
		{"rejected is final", DisputeStatusRejected, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsFinal(); got != tt.want {
				t.Errorf("DisputeStatus.IsFinal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDisputeStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name    string
		current DisputeStatus
		target  DisputeStatus
		want    bool
	}{
		// From OPEN
		{"open to under review", DisputeStatusOpen, DisputeStatusUnderReview, true},
		{"open to resolved", DisputeStatusOpen, DisputeStatusResolved, true},
		{"open to rejected", DisputeStatusOpen, DisputeStatusRejected, true},
		{"open to escalated", DisputeStatusOpen, DisputeStatusEscalated, false},

		// From UNDER_REVIEW
		{"under review to escalated", DisputeStatusUnderReview, DisputeStatusEscalated, true},
		{"under review to resolved", DisputeStatusUnderReview, DisputeStatusResolved, true},
		{"under review to rejected", DisputeStatusUnderReview, DisputeStatusRejected, true},
		{"under review to open", DisputeStatusUnderReview, DisputeStatusOpen, false},

		// From ESCALATED
		{"escalated to under review", DisputeStatusEscalated, DisputeStatusUnderReview, true},
		{"escalated to resolved", DisputeStatusEscalated, DisputeStatusResolved, true},
		{"escalated to rejected", DisputeStatusEscalated, DisputeStatusRejected, true},
		{"escalated to open", DisputeStatusEscalated, DisputeStatusOpen, false},

		// Final states
		{"resolved cannot transition", DisputeStatusResolved, DisputeStatusOpen, false},
		{"rejected cannot transition", DisputeStatusRejected, DisputeStatusOpen, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.current.CanTransitionTo(tt.target); got != tt.want {
				t.Errorf("DisputeStatus(%s).CanTransitionTo(%s) = %v, want %v",
					tt.current, tt.target, got, tt.want)
			}
		})
	}
}

func TestParseDisputeStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected DisputeStatus
	}{
		{"valid open", "OPEN", DisputeStatusOpen},
		{"valid under review", "UNDER_REVIEW", DisputeStatusUnderReview},
		{"valid escalated", "ESCALATED", DisputeStatusEscalated},
		{"valid resolved", "RESOLVED", DisputeStatusResolved},
		{"valid rejected", "REJECTED", DisputeStatusRejected},
		{"invalid defaults to open", "INVALID", DisputeStatusOpen},
		{"empty defaults to open", "", DisputeStatusOpen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseDisputeStatus(tt.input); got != tt.expected {
				t.Errorf("ParseDisputeStatus(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
