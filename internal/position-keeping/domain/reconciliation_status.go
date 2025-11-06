package domain

// ReconciliationStatus represents the reconciliation state of a transaction.
type ReconciliationStatus string

// Supported reconciliation statuses.
const (
	ReconciliationStatusUnreconciled ReconciliationStatus = "UNRECONCILED" // Not yet reconciled
	ReconciliationStatusMatched      ReconciliationStatus = "MATCHED"      // Matched with external source
	ReconciliationStatusMismatched   ReconciliationStatus = "MISMATCHED"   // Discrepancy found
	ReconciliationStatusResolved     ReconciliationStatus = "RESOLVED"     // Discrepancy resolved
)

// IsValid checks if the reconciliation status is valid.
func (r ReconciliationStatus) IsValid() bool {
	switch r {
	case ReconciliationStatusUnreconciled, ReconciliationStatusMatched,
		ReconciliationStatusMismatched, ReconciliationStatusResolved:
		return true
	}
	return false
}

// String returns the string representation of the reconciliation status.
func (r ReconciliationStatus) String() string {
	return string(r)
}
