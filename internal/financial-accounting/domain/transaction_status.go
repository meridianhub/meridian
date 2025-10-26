package domain

// TransactionStatus represents the current state of a financial transaction.
type TransactionStatus string

// Supported transaction statuses through the transaction lifecycle.
const (
	TransactionStatusPending   TransactionStatus = "PENDING"   // Transaction created but not yet posted
	TransactionStatusPosted    TransactionStatus = "POSTED"    // Transaction successfully posted to ledger
	TransactionStatusFailed    TransactionStatus = "FAILED"    // Transaction failed validation or posting
	TransactionStatusCancelled TransactionStatus = "CANCELLED" // Transaction manually cancelled
	TransactionStatusReversed  TransactionStatus = "REVERSED"  // Transaction reversed with offsetting entry
)

// IsValid checks if the transaction status is valid.
func (t TransactionStatus) IsValid() bool {
	switch t {
	case TransactionStatusPending, TransactionStatusPosted, TransactionStatusFailed,
	     TransactionStatusCancelled, TransactionStatusReversed:
		return true
	}
	return false
}

// String returns the string representation of the transaction status.
func (t TransactionStatus) String() string {
	return string(t)
}

// IsFinal checks if the status is a final state (no further transitions possible).
func (t TransactionStatus) IsFinal() bool {
	return t == TransactionStatusPosted || 
	       t == TransactionStatusFailed || 
	       t == TransactionStatusCancelled || 
	       t == TransactionStatusReversed
}
