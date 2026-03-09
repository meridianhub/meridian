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
//
// The Amount field uses the generic Qty[Monetary] type (aliased as Money) from
// the Universal Asset System, enabling type-safe monetary operations with
// compile-time dimension checking.
type LedgerPosting struct {
	ID                    uuid.UUID
	FinancialBookingLogID uuid.UUID
	Direction             PostingDirection
	Amount                Money
	AccountID             string
	AccountServiceDomain  string // BIAN Service Domain: "CURRENT_ACCOUNT", "INTERNAL_ACCOUNT", or ""
	ValueDate             time.Time
	PostingResult         string
	Status                TransactionStatus
	CorrelationID         string
	CreatedAt             time.Time

	// Attributes stores contextual metadata for the posting.
	// This can include information like source system, transaction type,
	// or other domain-specific context needed for audit or processing.
	Attributes map[string]string
}

// NewLedgerPosting creates a new ledger posting with validation.
//
// Follows BIAN Financial Accounting specification for double-entry bookkeeping:
//   - Amount must be positive (BIAN CurrencyAndAmount: "A zero amount is considered a positive amount")
//   - Direction (DEBIT/CREDIT) indicates the accounting meaning, not mathematical sign
//   - Reversals/corrections use opposite direction with positive amounts, not negative amounts
//
// Reference: BIAN v14.0.0 FinancialAccounting.yaml CurrencyAndAmount schema
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

	// BIAN requirement: amounts must be positive (zero is considered positive)
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
		Attributes:            make(map[string]string),
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
