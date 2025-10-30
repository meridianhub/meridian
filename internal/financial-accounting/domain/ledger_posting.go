package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidPostingAmount is returned when amount is not positive
	ErrInvalidPostingAmount = errors.New("posting amount must be positive")
	// ErrInvalidAccountID is returned when account ID is empty
	ErrInvalidAccountID = errors.New("account ID cannot be empty")
	// ErrInvalidBookingLogID is returned when booking log ID is nil
	ErrInvalidBookingLogID = errors.New("booking log ID cannot be nil")
	// ErrAlreadyPosted is returned when posting is already in posted status
	ErrAlreadyPosted = errors.New("posting already posted")
	// ErrCannotFailPosted is returned when attempting to fail a posted transaction
	ErrCannotFailPosted = errors.New("cannot fail a posted transaction")
)

// LedgerPosting represents a single posting in double-entry bookkeeping
// Pure domain model with business logic, no persistence concerns
type LedgerPosting struct {
	ID                    uuid.UUID
	FinancialBookingLogID uuid.UUID
	Direction             PostingDirection
	Amount                Money
	AccountID             string
	ValueDate             time.Time
	PostingResult         string
	Status                TransactionStatus
	CorrelationID         string
	CreatedAt             time.Time
}

// NewLedgerPosting creates a new ledger posting with validation
func NewLedgerPosting(
	bookingLogID uuid.UUID,
	direction PostingDirection,
	amount Money,
	accountID string,
	valueDate time.Time,
	correlationID string,
) (*LedgerPosting, error) {
	if bookingLogID == uuid.Nil {
		return nil, ErrInvalidBookingLogID
	}

	if !amount.IsPositive() {
		return nil, ErrInvalidPostingAmount
	}

	if accountID == "" {
		return nil, ErrInvalidAccountID
	}

	return &LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             direction,
		Amount:                amount,
		AccountID:             accountID,
		ValueDate:             valueDate,
		Status:                TransactionStatusPending,
		CorrelationID:         correlationID,
		CreatedAt:             time.Now(),
	}, nil
}

// Post marks the posting as successfully posted
func (p *LedgerPosting) Post(result string) error {
	if p.Status == TransactionStatusPosted {
		return ErrAlreadyPosted
	}

	p.Status = TransactionStatusPosted
	p.PostingResult = result
	return nil
}

// Fail marks the posting as failed
func (p *LedgerPosting) Fail(result string) error {
	if p.Status == TransactionStatusPosted {
		return ErrCannotFailPosted
	}

	p.Status = TransactionStatusFailed
	p.PostingResult = result
	return nil
}

// IsPosted returns true if the posting has been posted
func (p *LedgerPosting) IsPosted() bool {
	return p.Status == TransactionStatusPosted
}
