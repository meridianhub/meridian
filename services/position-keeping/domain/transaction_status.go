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
	TransactionStatusSuspended  TransactionStatus = "SUSPENDED"  // Transaction processing temporarily suspended
	TransactionStatusTerminated TransactionStatus = "TERMINATED" // Transaction permanently terminated
)

// IsValid checks if the transaction status is valid.
func (t TransactionStatus) IsValid() bool {
	switch t {
	case TransactionStatusPending, TransactionStatusReconciled, TransactionStatusPosted,
		TransactionStatusFailed, TransactionStatusRejected, TransactionStatusAmended,
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
		t == TransactionStatusRejected ||
		t == TransactionStatusCancelled ||
		t == TransactionStatusReversed ||
		t == TransactionStatusTerminated
}

// CanTransitionTo checks if a transition to the target status is valid.
func (t TransactionStatus) CanTransitionTo(target TransactionStatus) bool {
	// Special case: POSTED can transition to REVERSED
	if t == TransactionStatusPosted {
		return target == TransactionStatusReversed
	}

	// TERMINATED is absolutely terminal - no transitions allowed
	if t == TransactionStatusTerminated {
		return false
	}

	// Other final states cannot transition (except POSTED handled above)
	if t.IsFinal() {
		return false
	}

	// SUSPENDED can transition back to original states or to TERMINATED
	if t == TransactionStatusSuspended {
		return target == TransactionStatusPending ||
			target == TransactionStatusReconciled ||
			target == TransactionStatusPosted ||
			target == TransactionStatusTerminated
	}

	// Valid state transitions
	validTransitions := map[TransactionStatus][]TransactionStatus{
		TransactionStatusPending: {
			TransactionStatusReconciled,
			TransactionStatusPosted,
			TransactionStatusFailed,
			TransactionStatusRejected,
			TransactionStatusCancelled,
			TransactionStatusSuspended,
		},
		TransactionStatusReconciled: {
			TransactionStatusPosted,
			TransactionStatusAmended,
			TransactionStatusRejected,
			TransactionStatusSuspended,
		},
		TransactionStatusAmended: {
			TransactionStatusReconciled,
			TransactionStatusPosted,
			TransactionStatusRejected,
			TransactionStatusSuspended,
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

// CanSuspend checks if the current status allows suspension.
// Only PENDING, RECONCILED, AMENDED, and POSTED states can be suspended.
// FAILED, REJECTED, CANCELLED, REVERSED, and TERMINATED are not suspendable.
func (t TransactionStatus) CanSuspend() bool {
	return t == TransactionStatusPending ||
		t == TransactionStatusReconciled ||
		t == TransactionStatusAmended ||
		t == TransactionStatusPosted
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

// ParseTransactionStatus converts a string to TransactionStatus.
// Returns TransactionStatusPending for unrecognized values.
func ParseTransactionStatus(s string) TransactionStatus {
	status := TransactionStatus(s)
	if status.IsValid() {
		return status
	}
	return TransactionStatusPending
}
