package domain

import (
	"testing"
)

func TestTransactionSource_IsValid_AllValidSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source TransactionSource
	}{
		{"MANUAL", TransactionSourceManual},
		{"AUTOMATED", TransactionSourceAutomated},
		{"IMPORTED", TransactionSourceImported},
		{"RECONCILIATION", TransactionSourceReconciliation},
		{"ADJUSTMENT", TransactionSourceAdjustment},
		{"CURRENT_ACCOUNT", TransactionSourceCurrentAccount},
		{"FINANCIAL_ACCOUNTING", TransactionSourceFinancialAccounting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.source.IsValid() {
				t.Errorf("Expected %v to be valid", tt.source)
			}
		})
	}
}

func TestTransactionSource_IsValid_EmptyString(t *testing.T) {
	t.Parallel()

	empty := TransactionSource("")
	if empty.IsValid() {
		t.Error("Expected empty string to be invalid")
	}
}

func TestTransactionSource_IsValid_InvalidValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source TransactionSource
	}{
		{"lowercase manual", TransactionSource("manual")},
		{"invalid string", TransactionSource("INVALID")},
		{"partial match", TransactionSource("MAN")},
		{"numeric", TransactionSource("123")},
		{"special chars", TransactionSource("@#$")},
		{"with spaces", TransactionSource("MANUAL ")},
		{"dash instead of underscore", TransactionSource("CURRENT-ACCOUNT")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.source.IsValid() {
				t.Errorf("Expected %v to be invalid", tt.source)
			}
		})
	}
}

func TestParseTransactionSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected TransactionSource
	}{
		{"valid manual", "MANUAL", TransactionSourceManual},
		{"valid automated", "AUTOMATED", TransactionSourceAutomated},
		{"valid imported", "IMPORTED", TransactionSourceImported},
		{"valid reconciliation", "RECONCILIATION", TransactionSourceReconciliation},
		{"valid adjustment", "ADJUSTMENT", TransactionSourceAdjustment},
		{"valid current account", "CURRENT_ACCOUNT", TransactionSourceCurrentAccount},
		{"valid financial accounting", "FINANCIAL_ACCOUNTING", TransactionSourceFinancialAccounting},
		{"invalid defaults to manual", "INVALID", TransactionSourceManual},
		{"empty defaults to manual", "", TransactionSourceManual},
		{"lowercase defaults to manual", "manual", TransactionSourceManual},
		{"with spaces defaults to manual", "MANUAL ", TransactionSourceManual},
		{"dash instead of underscore defaults to manual", "CURRENT-ACCOUNT", TransactionSourceManual},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseTransactionSource(tt.input)
			if result != tt.expected {
				t.Errorf("Expected ParseTransactionSource(%q) to return %v, got %v", tt.input, tt.expected, result)
			}
		})
	}
}
