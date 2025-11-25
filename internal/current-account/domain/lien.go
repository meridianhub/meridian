package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Lien domain errors
var (
	ErrLienNotActive         = errors.New("lien is not in active status")
	ErrInvalidLienTransition = errors.New("invalid lien status transition")
	ErrLienExpired           = errors.New("lien has expired")
	ErrInvalidLienAmount     = errors.New("lien amount must be positive")
	ErrLienAlreadyExists     = errors.New("lien already exists for this idempotency key")
)

// LienStatus represents the lifecycle state of a lien
type LienStatus string

// Lien status constants following ADR-012
const (
	LienStatusActive     LienStatus = "ACTIVE"     // Funds reserved
	LienStatusExecuted   LienStatus = "EXECUTED"   // Funds debited (terminal)
	LienStatusTerminated LienStatus = "TERMINATED" // Reservation released (terminal)
)

// Lien represents a fund reservation on an account
// Liens are used by the Payment Order saga to reserve funds before executing external payments.
// Invariant: Available Balance = Current Balance - Sum(Active Liens)
type Lien struct {
	ID                    uuid.UUID
	AccountID             uuid.UUID
	Amount                Money
	Status                LienStatus
	PaymentOrderReference string
	TerminationReason     string
	ExpiresAt             *time.Time
	Version               int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// NewLien creates a new lien in ACTIVE status
func NewLien(accountID uuid.UUID, amount Money, paymentOrderReference string, expiresAt *time.Time) (*Lien, error) {
	if !amount.IsPositive() {
		return nil, ErrInvalidLienAmount
	}

	now := time.Now()
	return &Lien{
		ID:                    uuid.New(),
		AccountID:             accountID,
		Amount:                amount,
		Status:                LienStatusActive,
		PaymentOrderReference: paymentOrderReference,
		ExpiresAt:             expiresAt,
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}, nil
}

// Execute transitions the lien to EXECUTED status (terminal state)
// This is called when the payment has been successfully submitted and the funds should be debited.
// Idempotent: Returns nil if already executed.
func (l *Lien) Execute() error {
	if l.Status == LienStatusExecuted {
		return nil // Idempotent
	}

	if l.Status != LienStatusActive {
		return ErrLienNotActive
	}

	l.Status = LienStatusExecuted
	l.UpdatedAt = time.Now()
	return nil
}

// Terminate transitions the lien to TERMINATED status (terminal state)
// This is called when the payment fails or is cancelled, releasing the reserved funds.
// Idempotent: Returns nil if already terminated.
func (l *Lien) Terminate(reason string) error {
	if l.Status == LienStatusTerminated {
		return nil // Idempotent
	}

	if l.Status != LienStatusActive {
		return ErrLienNotActive
	}

	l.Status = LienStatusTerminated
	l.TerminationReason = reason
	l.UpdatedAt = time.Now()
	return nil
}

// IsActive returns true if the lien is in ACTIVE status
func (l *Lien) IsActive() bool {
	return l.Status == LienStatusActive
}

// IsTerminal returns true if the lien is in a terminal state (EXECUTED or TERMINATED)
func (l *Lien) IsTerminal() bool {
	return l.Status == LienStatusExecuted || l.Status == LienStatusTerminated
}

// IsExpired returns true if the lien has an expiration time that has passed
func (l *Lien) IsExpired() bool {
	if l.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*l.ExpiresAt)
}

// CanExecute returns true if the lien can be executed
func (l *Lien) CanExecute() bool {
	return l.Status == LienStatusActive && !l.IsExpired()
}

// CanTerminate returns true if the lien can be terminated
func (l *Lien) CanTerminate() bool {
	return l.Status == LienStatusActive
}
