package domain

import "testing"

func TestVarianceReason_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		reason VarianceReason
		want   bool
	}{
		{"valid amount mismatch", VarianceReasonAmountMismatch, true},
		{"valid missing entry", VarianceReasonMissingEntry, true},
		{"valid duplicate entry", VarianceReasonDuplicateEntry, true},
		{"valid timing difference", VarianceReasonTimingDifference, true},
		{"valid currency mismatch", VarianceReasonCurrencyMismatch, true},
		{"valid direction error", VarianceReasonDirectionError, true},
		{"valid other", VarianceReasonOther, true},
		{"invalid", VarianceReason("INVALID"), false},
		{"empty", VarianceReason(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.reason.IsValid(); got != tt.want {
				t.Errorf("VarianceReason.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseVarianceReason(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected VarianceReason
	}{
		{"valid amount mismatch", "AMOUNT_MISMATCH", VarianceReasonAmountMismatch},
		{"valid missing entry", "MISSING_ENTRY", VarianceReasonMissingEntry},
		{"invalid defaults to other", "INVALID", VarianceReasonOther},
		{"empty defaults to other", "", VarianceReasonOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseVarianceReason(tt.input); got != tt.expected {
				t.Errorf("ParseVarianceReason(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
