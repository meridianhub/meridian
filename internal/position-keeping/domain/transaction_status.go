package domain

// TransactionStatus represents the current state of a financial transaction.
type TransactionStatus string

// Supported transaction statuses through the transaction lifecycle.
const (
	TransactionStatusPending    TransactionStatus = "PENDING"    // Transaction created but not yet processed
	TransactionStatusReconciled TransactionStatus = "RECONCILED" // Transaction reconciled with external source
	TransactionStatusPosted     TransactionStatus = "POSTED"     // Transaction successfully posted to ledger
	TransactionStatusFailed     TransactionStatus = "FAILED"     // Transaction failed validation or posting
	TransactionStatusRejected   TransactionStatus = "REJECTED"   // Transaction rejected during review
	TransactionStatusAmended    TransactionStatus = "AMENDED"    // Transaction amended with new version
	TransactionStatusCancelled  TransactionStatus = "CANCELLED"  // Transaction manually cancelled
	TransactionStatusReversed   TransactionStatus = "REVERSED"   // Transaction reversed with offsetting entry
)

// IsValid checks if the transaction status is valid.
func (t TransactionStatus) IsValid() bool {
	switch t {
	case TransactionStatusPending, TransactionStatusReconciled, TransactionStatusPosted,
		TransactionStatusFailed, TransactionStatusRejected, TransactionStatusAmended,
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
		t == TransactionStatusRejected ||
		t == TransactionStatusCancelled ||
		t == TransactionStatusReversed
}

// CanTransitionTo checks if a transition to the target status is valid.
func (t TransactionStatus) CanTransitionTo(target TransactionStatus) bool {
	// Special case: POSTED can transition to REVERSED
	if t == TransactionStatusPosted {
		return target == TransactionStatusReversed
	}

	// Other final states cannot transition
	if t.IsFinal() {
		return false
	}

	// Valid state transitions
	validTransitions := map[TransactionStatus][]TransactionStatus{
		TransactionStatusPending: {
			TransactionStatusReconciled,
			TransactionStatusPosted,
			TransactionStatusFailed,
			TransactionStatusRejected,
			TransactionStatusCancelled,
		},
		TransactionStatusReconciled: {
			TransactionStatusPosted,
			TransactionStatusAmended,
			TransactionStatusRejected,
		},
		TransactionStatusAmended: {
			TransactionStatusReconciled,
			TransactionStatusPosted,
			TransactionStatusRejected,
		},
	}

	allowed, exists := validTransitions[t]
	if !exists {
		return false
	}

	for _, allowedStatus := range allowed {
		if target == allowedStatus {
			return true
		}
	}

	return false
}
