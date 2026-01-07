package domain

// TransactionSource represents the origin of a transaction entry.
type TransactionSource string

// Supported transaction sources.
const (
	TransactionSourceManual              TransactionSource = "MANUAL"               // Manually entered transaction
	TransactionSourceAutomated           TransactionSource = "AUTOMATED"            // System-generated transaction
	TransactionSourceImported            TransactionSource = "IMPORTED"             // Bulk imported transaction
	TransactionSourceReconciliation      TransactionSource = "RECONCILIATION"       // Created during reconciliation
	TransactionSourceAdjustment          TransactionSource = "ADJUSTMENT"           // Adjustment or correction entry
	TransactionSourceCurrentAccount      TransactionSource = "CURRENT_ACCOUNT"      // From Current Account service
	TransactionSourceFinancialAccounting TransactionSource = "FINANCIAL_ACCOUNTING" // From Financial Accounting service
	TransactionSourceOpeningBalance      TransactionSource = "OPENING_BALANCE"      // Opening balance entry for migration
	TransactionSourceMigration           TransactionSource = "MIGRATION"            // General migration entry from legacy systems
)

// IsValid checks if the transaction source is valid.
func (t TransactionSource) IsValid() bool {
	switch t {
	case TransactionSourceManual, TransactionSourceAutomated, TransactionSourceImported,
		TransactionSourceReconciliation, TransactionSourceAdjustment,
		TransactionSourceCurrentAccount, TransactionSourceFinancialAccounting,
		TransactionSourceOpeningBalance, TransactionSourceMigration:
		return true
	}
	return false
}

// String returns the string representation of the transaction source.
func (t TransactionSource) String() string {
	return string(t)
}

// ParseTransactionSource converts a string to TransactionSource.
// Returns TransactionSourceManual for unrecognized values.
func ParseTransactionSource(s string) TransactionSource {
	source := TransactionSource(s)
	if source.IsValid() {
		return source
	}
	return TransactionSourceManual
}
