package domain

import "testing"

func TestVarianceStatus_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		status VarianceStatus
		want   bool
	}{
		{"valid detected", VarianceStatusDetected, true},
		{"valid valued", VarianceStatusValued, true},
		{"valid open", VarianceStatusOpen, true},
		{"valid investigating", VarianceStatusInvestigating, true},
		{"valid disputed", VarianceStatusDisputed, true},
		{"valid resolved", VarianceStatusResolved, true},
		{"valid accepted", VarianceStatusAccepted, true},
		{"invalid status", VarianceStatus("INVALID"), false},
		{"empty status", VarianceStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("VarianceStatus.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVarianceStatus_IsFinal(t *testing.T) {
	tests := []struct {
		name   string
		status VarianceStatus
		want   bool
	}{
		{"detected not final", VarianceStatusDetected, false},
		{"valued not final", VarianceStatusValued, false},
		{"open not final", VarianceStatusOpen, false},
		{"investigating not final", VarianceStatusInvestigating, false},
		{"disputed not final", VarianceStatusDisputed, false},
		{"resolved is final", VarianceStatusResolved, true},
		{"accepted is final", VarianceStatusAccepted, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsFinal(); got != tt.want {
				t.Errorf("VarianceStatus.IsFinal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVarianceStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name    string
		current VarianceStatus
		target  VarianceStatus
		want    bool
	}{
		// From DETECTED
		{"detected to valued", VarianceStatusDetected, VarianceStatusValued, true},
		{"detected to open", VarianceStatusDetected, VarianceStatusOpen, true},
		{"detected to resolved", VarianceStatusDetected, VarianceStatusResolved, true},
		{"detected to accepted", VarianceStatusDetected, VarianceStatusAccepted, true},
		{"detected to investigating", VarianceStatusDetected, VarianceStatusInvestigating, false},

		// From VALUED
		{"valued to open", VarianceStatusValued, VarianceStatusOpen, true},
		{"valued to investigating", VarianceStatusValued, VarianceStatusInvestigating, true},
		{"valued to disputed", VarianceStatusValued, VarianceStatusDisputed, true},
		{"valued to resolved", VarianceStatusValued, VarianceStatusResolved, true},
		{"valued to accepted", VarianceStatusValued, VarianceStatusAccepted, true},
		{"valued to detected", VarianceStatusValued, VarianceStatusDetected, false},

		// From OPEN
		{"open to investigating", VarianceStatusOpen, VarianceStatusInvestigating, true},
		{"open to disputed", VarianceStatusOpen, VarianceStatusDisputed, true},
		{"open to resolved", VarianceStatusOpen, VarianceStatusResolved, true},
		{"open to accepted", VarianceStatusOpen, VarianceStatusAccepted, true},

		// From INVESTIGATING
		{"investigating to disputed", VarianceStatusInvestigating, VarianceStatusDisputed, true},
		{"investigating to resolved", VarianceStatusInvestigating, VarianceStatusResolved, true},
		{"investigating to accepted", VarianceStatusInvestigating, VarianceStatusAccepted, true},
		{"investigating to open", VarianceStatusInvestigating, VarianceStatusOpen, false},

		// From DISPUTED
		{"disputed to investigating", VarianceStatusDisputed, VarianceStatusInvestigating, true},
		{"disputed to resolved", VarianceStatusDisputed, VarianceStatusResolved, true},
		{"disputed to accepted", VarianceStatusDisputed, VarianceStatusAccepted, true},
		{"disputed to open", VarianceStatusDisputed, VarianceStatusOpen, false},

		// Final states
		{"resolved cannot transition", VarianceStatusResolved, VarianceStatusOpen, false},
		{"accepted cannot transition", VarianceStatusAccepted, VarianceStatusOpen, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.current.CanTransitionTo(tt.target); got != tt.want {
				t.Errorf("VarianceStatus(%s).CanTransitionTo(%s) = %v, want %v",
					tt.current, tt.target, got, tt.want)
			}
		})
	}
}

func TestParseVarianceStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected VarianceStatus
	}{
		{"valid detected", "DETECTED", VarianceStatusDetected},
		{"valid valued", "VALUED", VarianceStatusValued},
		{"valid open", "OPEN", VarianceStatusOpen},
		{"valid investigating", "INVESTIGATING", VarianceStatusInvestigating},
		{"valid disputed", "DISPUTED", VarianceStatusDisputed},
		{"valid resolved", "RESOLVED", VarianceStatusResolved},
		{"valid accepted", "ACCEPTED", VarianceStatusAccepted},
		{"invalid defaults to open", "INVALID", VarianceStatusOpen},
		{"empty defaults to open", "", VarianceStatusOpen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseVarianceStatus(tt.input); got != tt.expected {
				t.Errorf("ParseVarianceStatus(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
