package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidEntryAmount is returned when amount is not positive
	ErrInvalidEntryAmount = errors.New("entry amount must be positive")
	// ErrInvalidAccountID is returned when account ID is empty
	ErrInvalidAccountID = errors.New("account ID cannot be empty")
	// ErrInvalidTransactionID is returned when transaction ID is nil
	ErrInvalidTransactionID = errors.New("transaction ID cannot be nil")
	// ErrInvalidPostingDirection is returned when posting direction is invalid
	ErrInvalidPostingDirection = errors.New("posting direction is invalid")
)

// TransactionLogEntry represents a single entry in the financial position log.
// It captures the details of a transaction affecting an account's position.
type TransactionLogEntry struct {
	EntryID       uuid.UUID
	TransactionID uuid.UUID
	AccountID     string
	Amount        Money
	Direction     PostingDirection
	Timestamp     time.Time
	Description   string
	Reference     string
	Source        TransactionSource
	CreatedAt     time.Time
}

// NewTransactionLogEntry creates a new transaction log entry with validation.
func NewTransactionLogEntry(
	transactionID uuid.UUID,
	accountID string,
	amount Money,
	direction PostingDirection,
	timestamp time.Time,
	description string,
	reference string,
	source TransactionSource,
) (*TransactionLogEntry, error) {
	if transactionID == uuid.Nil {
		return nil, ErrInvalidTransactionID
	}

	if accountID == "" {
		return nil, ErrInvalidAccountID
	}

	if !amount.IsPositive() {
		return nil, ErrInvalidEntryAmount
	}

	if !direction.IsValid() {
		return nil, ErrInvalidPostingDirection
	}

	if !source.IsValid() {
		source = TransactionSourceManual // Default to manual if invalid
	}

	return &TransactionLogEntry{
		EntryID:       uuid.New(),
		TransactionID: transactionID,
		AccountID:     accountID,
		Amount:        amount,
		Direction:     direction,
		Timestamp:     timestamp,
		Description:   description,
		Reference:     reference,
		Source:        source,
		CreatedAt:     time.Now().UTC(),
	}, nil
}
