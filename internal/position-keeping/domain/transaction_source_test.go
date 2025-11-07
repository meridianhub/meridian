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
