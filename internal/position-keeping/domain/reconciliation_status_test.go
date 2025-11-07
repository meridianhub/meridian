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
