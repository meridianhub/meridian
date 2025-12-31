package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Withdrawal domain errors
var (
	ErrWithdrawalNotFound          = errors.New("withdrawal not found")
	ErrWithdrawalNotPending        = errors.New("withdrawal is not in pending status")
	ErrInvalidWithdrawalTransition = errors.New("invalid withdrawal status transition")
	ErrInvalidWithdrawalAmount     = errors.New("withdrawal amount must be positive")
	ErrWithdrawalAlreadyExists     = errors.New("withdrawal already exists for this reference")
)

// WithdrawalStatus represents the lifecycle state of a withdrawal
type WithdrawalStatus string

// Withdrawal status constants
const (
	WithdrawalStatusPending   WithdrawalStatus = "PENDING"   // Withdrawal initiated
	WithdrawalStatusCompleted WithdrawalStatus = "COMPLETED" // Withdrawal executed successfully (terminal)
	WithdrawalStatusFailed    WithdrawalStatus = "FAILED"    // Withdrawal failed (terminal)
	WithdrawalStatusCancelled WithdrawalStatus = "CANCELLED" // Withdrawal cancelled (terminal)
)

// Withdrawal represents a fund withdrawal from an account.
// Withdrawals track the movement of funds out of an account.
//
// Note: Fields are exported for persistence layer access. State transitions should only be
// performed via Complete(), Fail(), and Cancel() methods which enforce the state machine invariants.
// The Version field is a persistence concern exposed here for optimistic locking support.
type Withdrawal struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	Amount    Money
	Status    WithdrawalStatus
	Reference string // Unique reference for idempotency
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewWithdrawal creates a new withdrawal in PENDING status
func NewWithdrawal(accountID uuid.UUID, amount Money, reference string) (*Withdrawal, error) {
	if !amount.IsPositive() {
		return nil, ErrInvalidWithdrawalAmount
	}

	now := time.Now()
	return &Withdrawal{
		ID:        uuid.New(),
		AccountID: accountID,
		Amount:    amount,
		Status:    WithdrawalStatusPending,
		Reference: reference,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Complete transitions the withdrawal to COMPLETED status (terminal state)
// This is called when the withdrawal has been successfully executed.
// Idempotent: Returns nil if already completed.
func (w *Withdrawal) Complete() error {
	if w.Status == WithdrawalStatusCompleted {
		return nil // Idempotent
	}

	if w.Status != WithdrawalStatusPending {
		return ErrWithdrawalNotPending
	}

	w.Status = WithdrawalStatusCompleted
	w.UpdatedAt = time.Now()
	return nil
}

// Fail transitions the withdrawal to FAILED status (terminal state)
// This is called when the withdrawal fails during processing.
// Idempotent: Returns nil if already failed.
func (w *Withdrawal) Fail() error {
	if w.Status == WithdrawalStatusFailed {
		return nil // Idempotent
	}

	if w.Status != WithdrawalStatusPending {
		return ErrWithdrawalNotPending
	}

	w.Status = WithdrawalStatusFailed
	w.UpdatedAt = time.Now()
	return nil
}

// Cancel transitions the withdrawal to CANCELLED status (terminal state)
// This is called when the withdrawal is cancelled before completion.
// Idempotent: Returns nil if already cancelled.
func (w *Withdrawal) Cancel() error {
	if w.Status == WithdrawalStatusCancelled {
		return nil // Idempotent
	}

	if w.Status != WithdrawalStatusPending {
		return ErrWithdrawalNotPending
	}

	w.Status = WithdrawalStatusCancelled
	w.UpdatedAt = time.Now()
	return nil
}

// IsPending returns true if the withdrawal is in PENDING status
func (w *Withdrawal) IsPending() bool {
	return w.Status == WithdrawalStatusPending
}

// IsTerminal returns true if the withdrawal is in a terminal state
func (w *Withdrawal) IsTerminal() bool {
	return w.Status == WithdrawalStatusCompleted ||
		w.Status == WithdrawalStatusFailed ||
		w.Status == WithdrawalStatusCancelled
}
