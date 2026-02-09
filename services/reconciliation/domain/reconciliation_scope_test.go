package domain

import "testing"

func TestReconciliationScope_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		scope ReconciliationScope
		want  bool
	}{
		{"valid account", ReconciliationScopeAccount, true},
		{"valid instrument", ReconciliationScopeInstrument, true},
		{"valid portfolio", ReconciliationScopePortfolio, true},
		{"valid full", ReconciliationScopeFull, true},
		{"invalid", ReconciliationScope("INVALID"), false},
		{"empty", ReconciliationScope(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.scope.IsValid(); got != tt.want {
				t.Errorf("ReconciliationScope.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseReconciliationScope(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ReconciliationScope
	}{
		{"valid account", "ACCOUNT", ReconciliationScopeAccount},
		{"valid full", "FULL", ReconciliationScopeFull},
		{"invalid defaults to account", "INVALID", ReconciliationScopeAccount},
		{"empty defaults to account", "", ReconciliationScopeAccount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseReconciliationScope(tt.input); got != tt.expected {
				t.Errorf("ParseReconciliationScope(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
