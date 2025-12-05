package domain

import (
	"testing"
)

func TestReconciliationStatus_IsValid_AllValidStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status ReconciliationStatus
	}{
		{"UNRECONCILED", ReconciliationStatusUnreconciled},
		{"MATCHED", ReconciliationStatusMatched},
		{"MISMATCHED", ReconciliationStatusMismatched},
		{"RESOLVED", ReconciliationStatusResolved},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.status.IsValid() {
				t.Errorf("Expected %v to be valid", tt.status)
			}
		})
	}
}

func TestReconciliationStatus_IsValid_EmptyString(t *testing.T) {
	t.Parallel()

	empty := ReconciliationStatus("")
	if empty.IsValid() {
		t.Error("Expected empty string to be invalid")
	}
}

func TestReconciliationStatus_IsValid_InvalidValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status ReconciliationStatus
	}{
		{"lowercase matched", ReconciliationStatus("matched")},
		{"invalid string", ReconciliationStatus("INVALID")},
		{"partial match", ReconciliationStatus("MATCH")},
		{"numeric", ReconciliationStatus("123")},
		{"special chars", ReconciliationStatus("@#$")},
		{"with spaces", ReconciliationStatus("MATCHED ")},
		{"hyphenated", ReconciliationStatus("UN-RECONCILED")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.status.IsValid() {
				t.Errorf("Expected %v to be invalid", tt.status)
			}
		})
	}
}

func TestReconciliationStatus_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   ReconciliationStatus
		expected string
	}{
		{"unreconciled", ReconciliationStatusUnreconciled, "UNRECONCILED"},
		{"matched", ReconciliationStatusMatched, "MATCHED"},
		{"mismatched", ReconciliationStatusMismatched, "MISMATCHED"},
		{"resolved", ReconciliationStatusResolved, "RESOLVED"},
		{"empty", ReconciliationStatus(""), ""},
		{"invalid", ReconciliationStatus("INVALID"), "INVALID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.status.String()
			if result != tt.expected {
				t.Errorf("Expected String() to return %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestParseReconciliationStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected ReconciliationStatus
	}{
		{"valid unreconciled", "UNRECONCILED", ReconciliationStatusUnreconciled},
		{"valid matched", "MATCHED", ReconciliationStatusMatched},
		{"valid mismatched", "MISMATCHED", ReconciliationStatusMismatched},
		{"valid resolved", "RESOLVED", ReconciliationStatusResolved},
		{"invalid defaults to unreconciled", "INVALID", ReconciliationStatusUnreconciled},
		{"empty defaults to unreconciled", "", ReconciliationStatusUnreconciled},
		{"lowercase defaults to unreconciled", "matched", ReconciliationStatusUnreconciled},
		{"with spaces defaults to unreconciled", "MATCHED ", ReconciliationStatusUnreconciled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseReconciliationStatus(tt.input)
			if result != tt.expected {
				t.Errorf("Expected ParseReconciliationStatus(%q) to return %v, got %v", tt.input, tt.expected, result)
			}
		})
	}
}
