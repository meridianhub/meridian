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
)

// IsValid checks if the transaction source is valid.
func (t TransactionSource) IsValid() bool {
	switch t {
	case TransactionSourceManual, TransactionSourceAutomated, TransactionSourceImported,
		TransactionSourceReconciliation, TransactionSourceAdjustment,
		TransactionSourceCurrentAccount, TransactionSourceFinancialAccounting:
		return true
	}
	return false
}

// String returns the string representation of the transaction source.
func (t TransactionSource) String() string {
	return string(t)
}
