package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/services/financial-accounting/observability"
	"github.com/shopspring/decimal"
)

// PostingService handles ledger posting operations
type PostingService struct {
	repo              *persistence.LedgerRepository
	bankCashAccountID string
}

// NewPostingService creates a new posting service
func NewPostingService(repo *persistence.LedgerRepository, bankCashAccountID string) *PostingService {
	return &PostingService{
		repo:              repo,
		bankCashAccountID: bankCashAccountID,
	}
}

// decimalFromCents converts cents (int64) to decimal amount
func decimalFromCents(cents int64) decimal.Decimal {
	return decimal.NewFromInt(cents).Div(decimal.NewFromInt(100))
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
	timer := observability.NewOperationTimer(observability.OperationProcessDeposit)

	bookingLogID := uuid.New()

	// Convert cents to decimal
	amount := decimalFromCents(event.AmountCents)
	money, err := domain.NewMoney(amount, domain.Currency(event.Currency))
	if err != nil {
		timer.ObserveError(observability.ErrorCategoryValidation)
		observability.RecordDepositProcessed(event.Currency, observability.StatusError)
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
		timer.ObserveError(observability.ErrorCategoryValidation)
		observability.RecordDepositProcessed(event.Currency, observability.StatusError)
		return fmt.Errorf("failed to create debit posting: %w", err)
	}

	// Create credit posting (bank cash account)
	creditPosting, err := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionCredit,
		money,
		s.bankCashAccountID,
		event.ValueDate,
		event.CorrelationID,
	)
	if err != nil {
		timer.ObserveError(observability.ErrorCategoryValidation)
		observability.RecordDepositProcessed(event.Currency, observability.StatusError)
		return fmt.Errorf("failed to create credit posting: %w", err)
	}

	// Post both entries
	if err := debitPosting.Post("Deposit processed"); err != nil {
		timer.ObserveError(observability.ErrorCategoryInternal)
		observability.RecordDepositProcessed(event.Currency, observability.StatusError)
		return fmt.Errorf("failed to post debit: %w", err)
	}

	if err := creditPosting.Post("Deposit processed"); err != nil {
		timer.ObserveError(observability.ErrorCategoryInternal)
		observability.RecordDepositProcessed(event.Currency, observability.StatusError)
		return fmt.Errorf("failed to post credit: %w", err)
	}

	// Save both postings atomically in a transaction
	if err := s.repo.SavePostingsInTransaction(ctx, []*domain.LedgerPosting{debitPosting, creditPosting}); err != nil {
		timer.ObserveError(observability.ErrorCategoryDatabase)
		observability.RecordDepositProcessed(event.Currency, observability.StatusError)
		return fmt.Errorf("failed to save postings: %w", err)
	}

	// Record successful metrics
	timer.ObserveSuccess()
	observability.RecordDepositProcessed(event.Currency, observability.StatusSuccess)
	observability.RecordPosting(observability.DirectionDebit, event.Currency)
	observability.RecordPosting(observability.DirectionCredit, event.Currency)
	observability.RecordPostingAmount(observability.DirectionDebit, event.Currency, event.AmountCents)
	observability.RecordPostingAmount(observability.DirectionCredit, event.Currency, event.AmountCents)

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
	timer := observability.NewOperationTimer(observability.OperationValidateDoubleEntry)

	postings, err := s.repo.GetPostingsByBookingLogID(ctx, bookingLogID)
	if err != nil {
		timer.ObserveError(observability.ErrorCategoryDatabase)
		return false, fmt.Errorf("failed to get postings: %w", err)
	}

	debitTotal := decimal.Zero
	creditTotal := decimal.Zero

	for _, posting := range postings {
		switch posting.Direction {
		case domain.PostingDirectionDebit:
			debitTotal = debitTotal.Add(posting.Amount.Amount())
		case domain.PostingDirectionCredit:
			creditTotal = creditTotal.Add(posting.Amount.Amount())
		}
	}

	balanced := debitTotal.Equal(creditTotal)

	// Record validation result
	timer.ObserveSuccess()
	if balanced {
		observability.RecordDoubleEntryValidation("balanced")
	} else {
		observability.RecordDoubleEntryValidation("unbalanced")
	}

	return balanced, nil
}
