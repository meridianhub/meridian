package domain

import (
	"testing"
)

func TestTransactionStatus_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		status TransactionStatus
		want   bool
	}{
		{"valid pending", TransactionStatusPending, true},
		{"valid reconciled", TransactionStatusReconciled, true},
		{"valid posted", TransactionStatusPosted, true},
		{"valid failed", TransactionStatusFailed, true},
		{"valid rejected", TransactionStatusRejected, true},
		{"valid amended", TransactionStatusAmended, true},
		{"valid cancelled", TransactionStatusCancelled, true},
		{"valid reversed", TransactionStatusReversed, true},
		{"invalid status", TransactionStatus("INVALID"), false},
		{"empty status", TransactionStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("TransactionStatus.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransactionStatus_IsFinal(t *testing.T) {
	tests := []struct {
		name   string
		status TransactionStatus
		want   bool
	}{
		{"pending not final", TransactionStatusPending, false},
		{"reconciled not final", TransactionStatusReconciled, false},
		{"amended not final", TransactionStatusAmended, false},
		{"posted is final", TransactionStatusPosted, true},
		{"failed is final", TransactionStatusFailed, true},
		{"rejected is final", TransactionStatusRejected, true},
		{"cancelled is final", TransactionStatusCancelled, true},
		{"reversed is final", TransactionStatusReversed, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsFinal(); got != tt.want {
				t.Errorf("TransactionStatus.IsFinal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransactionStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name    string
		current TransactionStatus
		target  TransactionStatus
		want    bool
	}{
		// Valid transitions from PENDING
		{"pending to reconciled", TransactionStatusPending, TransactionStatusReconciled, true},
		{"pending to posted", TransactionStatusPending, TransactionStatusPosted, true},
		{"pending to failed", TransactionStatusPending, TransactionStatusFailed, true},
		{"pending to rejected", TransactionStatusPending, TransactionStatusRejected, true},
		{"pending to cancelled", TransactionStatusPending, TransactionStatusCancelled, true},

		// Invalid transitions from PENDING
		{"pending to amended", TransactionStatusPending, TransactionStatusAmended, false},
		{"pending to reversed", TransactionStatusPending, TransactionStatusReversed, false},

		// Valid transitions from RECONCILED
		{"reconciled to posted", TransactionStatusReconciled, TransactionStatusPosted, true},
		{"reconciled to amended", TransactionStatusReconciled, TransactionStatusAmended, true},
		{"reconciled to rejected", TransactionStatusReconciled, TransactionStatusRejected, true},

		// Invalid transitions from RECONCILED
		{"reconciled to pending", TransactionStatusReconciled, TransactionStatusPending, false},
		{"reconciled to failed", TransactionStatusReconciled, TransactionStatusFailed, false},

		// Valid transitions from AMENDED
		{"amended to reconciled", TransactionStatusAmended, TransactionStatusReconciled, true},
		{"amended to posted", TransactionStatusAmended, TransactionStatusPosted, true},
		{"amended to rejected", TransactionStatusAmended, TransactionStatusRejected, true},

		// Invalid transitions from AMENDED
		{"amended to pending", TransactionStatusAmended, TransactionStatusPending, false},

		// POSTED can transition to REVERSED
		{"posted to reversed", TransactionStatusPosted, TransactionStatusReversed, true},
		{"posted to other states invalid", TransactionStatusPosted, TransactionStatusReconciled, false},

		// Other final states cannot transition
		{"failed cannot transition", TransactionStatusFailed, TransactionStatusPending, false},
		{"rejected cannot transition", TransactionStatusRejected, TransactionStatusPending, false},
		{"cancelled cannot transition", TransactionStatusCancelled, TransactionStatusPending, false},
		{"reversed cannot transition", TransactionStatusReversed, TransactionStatusPending, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.current.CanTransitionTo(tt.target); got != tt.want {
				t.Errorf("TransactionStatus.CanTransitionTo() = %v, want %v", got, tt.want)
			}
		})
	}
}
