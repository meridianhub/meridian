package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/internal/financial-accounting/domain"
	"github.com/shopspring/decimal"
)

// decimalFromCents converts cents (int64) to decimal amount
func decimalFromCents(cents int64) decimal.Decimal {
	return decimal.NewFromInt(cents).Div(decimal.NewFromInt(100))
}

// PostingService handles ledger posting operations
type PostingService struct {
	repo *persistence.LedgerRepository
}

// NewPostingService creates a new posting service
func NewPostingService(repo *persistence.LedgerRepository) *PostingService {
	return &PostingService{repo: repo}
}

// DepositEvent represents a deposit event from CurrentAccount service
type DepositEvent struct {
	AccountID     string
	AmountCents   int64
	Currency      string
	CorrelationID string
	ValueDate     time.Time
}

// ProcessDeposit creates double-entry postings for a deposit
// Debit: Customer account (increases asset)
// Credit: Bank cash account (increases liability to customer)
func (s *PostingService) ProcessDeposit(ctx context.Context, event DepositEvent) error {
	bookingLogID := uuid.New()

	// Convert cents to decimal
	amount := decimalFromCents(event.AmountCents)
	money, err := domain.NewMoney(amount, domain.Currency(event.Currency))
	if err != nil {
		return fmt.Errorf("failed to create money: %w", err)
	}

	// Create debit posting (customer account)
	debitPosting, err := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionDebit,
		money,
		event.AccountID,
		event.ValueDate,
		event.CorrelationID,
	)
	if err != nil {
		return fmt.Errorf("failed to create debit posting: %w", err)
	}

	// Create credit posting (bank cash account)
	creditPosting, err := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionCredit,
		money,
		"BANK-CASH-001", // Bank's cash account
		event.ValueDate,
		event.CorrelationID,
	)
	if err != nil {
		return fmt.Errorf("failed to create credit posting: %w", err)
	}

	// Post both entries
	if err := debitPosting.Post("Deposit processed"); err != nil {
		return fmt.Errorf("failed to post debit: %w", err)
	}

	if err := creditPosting.Post("Deposit processed"); err != nil {
		return fmt.Errorf("failed to post credit: %w", err)
	}

	// Save to database
	if err := s.repo.SavePosting(ctx, debitPosting); err != nil {
		return fmt.Errorf("failed to save debit posting: %w", err)
	}

	if err := s.repo.SavePosting(ctx, creditPosting); err != nil {
		return fmt.Errorf("failed to save credit posting: %w", err)
	}

	return nil
}

// GetPostingsByBookingLog retrieves all postings for a booking log
func (s *PostingService) GetPostingsByBookingLog(ctx context.Context, bookingLogID uuid.UUID) ([]*domain.LedgerPosting, error) {
	postings, err := s.repo.GetPostingsByBookingLogID(ctx, bookingLogID)
	if err != nil {
		return nil, fmt.Errorf("failed to get postings: %w", err)
	}
	return postings, nil
}

// ValidateDoubleEntry checks that debits equal credits for a booking log
func (s *PostingService) ValidateDoubleEntry(ctx context.Context, bookingLogID uuid.UUID) (bool, error) {
	postings, err := s.repo.GetPostingsByBookingLogID(ctx, bookingLogID)
	if err != nil {
		return false, fmt.Errorf("failed to get postings: %w", err)
	}

	debitTotal := decimal.Zero
	creditTotal := decimal.Zero

	for _, posting := range postings {
		switch posting.Direction {
		case domain.PostingDirectionDebit:
			debitTotal = debitTotal.Add(posting.Amount.Amount)
		case domain.PostingDirectionCredit:
			creditTotal = creditTotal.Add(posting.Amount.Amount)
		}
	}

	return debitTotal.Equal(creditTotal), nil
}
