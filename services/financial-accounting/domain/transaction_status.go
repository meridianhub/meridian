package domain

// TransactionStatus represents the current state of a financial transaction.
type TransactionStatus string

// Supported transaction statuses through the transaction lifecycle.
const (
	TransactionStatusPending    TransactionStatus = "PENDING"    // Transaction created but not yet posted
	TransactionStatusPosted     TransactionStatus = "POSTED"     // Transaction successfully posted to ledger
	TransactionStatusFailed     TransactionStatus = "FAILED"     // Transaction failed validation or posting
	TransactionStatusCancelled  TransactionStatus = "CANCELLED"  // Transaction manually cancelled
	TransactionStatusReversed   TransactionStatus = "REVERSED"   // Transaction reversed with offsetting entry
	TransactionStatusSuspended  TransactionStatus = "SUSPENDED"  // Transaction processing temporarily suspended
	TransactionStatusTerminated TransactionStatus = "TERMINATED" // Transaction permanently terminated
)

// IsValid checks if the transaction status is valid.
func (t TransactionStatus) IsValid() bool {
	switch t {
	case TransactionStatusPending, TransactionStatusPosted, TransactionStatusFailed,
		TransactionStatusCancelled, TransactionStatusReversed, TransactionStatusSuspended,
		TransactionStatusTerminated:
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
		t == TransactionStatusReversed ||
		t == TransactionStatusTerminated
}

// CanTransitionTo checks if a transition to the target status is valid.
func (t TransactionStatus) CanTransitionTo(target TransactionStatus) bool {
	// TERMINATED is absolutely terminal - no transitions allowed
	if t == TransactionStatusTerminated {
		return false
	}

	// Other final states cannot transition
	if t.IsFinal() {
		return false
	}

	// SUSPENDED can transition back to PENDING/POSTED or to TERMINATED
	if t == TransactionStatusSuspended {
		return target == TransactionStatusPending ||
			target == TransactionStatusPosted ||
			target == TransactionStatusTerminated
	}

	// Valid state transitions from PENDING
	if t == TransactionStatusPending {
		return target == TransactionStatusPosted ||
			target == TransactionStatusFailed ||
			target == TransactionStatusCancelled ||
			target == TransactionStatusSuspended
	}

	return false
}

// CanSuspend checks if the current status allows suspension.
// Only PENDING and POSTED states can be suspended.
// FAILED, CANCELLED, REVERSED, and TERMINATED are not suspendable.
func (t TransactionStatus) CanSuspend() bool {
	return t == TransactionStatusPending || t == TransactionStatusPosted
}

// CanResume checks if the current status allows resumption.
// Only SUSPENDED status can be resumed.
func (t TransactionStatus) CanResume() bool {
	return t == TransactionStatusSuspended
}

// CanTerminate checks if the current status allows termination.
// Only SUSPENDED status can be terminated.
func (t TransactionStatus) CanTerminate() bool {
	return t == TransactionStatusSuspended
}
