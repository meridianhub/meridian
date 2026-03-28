package service

import (
	"context"
	"fmt"
	"log/slog"
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
	accountResolver   *AccountResolver
	logger            *slog.Logger
}

// PostingServiceConfig holds configuration for creating a PostingService.
type PostingServiceConfig struct {
	// Repo is the ledger repository for database operations.
	Repo *persistence.LedgerRepository

	// BankCashAccountID is the static fallback account ID for clearing operations.
	// Used when AccountResolver is nil or when dynamic lookup fails.
	BankCashAccountID string

	// AccountResolver is optional. When provided, enables dynamic clearing account
	// lookup by instrument. Falls back to BankCashAccountID on lookup failure.
	AccountResolver *AccountResolver

	// Logger is optional. If nil, a default logger is used.
	Logger *slog.Logger
}

// NewPostingService creates a new posting service.
//
// Deprecated: Use NewPostingServiceWithConfig for full configuration options.
func NewPostingService(repo *persistence.LedgerRepository, bankCashAccountID string) *PostingService {
	return &PostingService{
		repo:              repo,
		bankCashAccountID: bankCashAccountID,
		logger:            slog.Default(),
	}
}

// NewPostingServiceWithConfig creates a new posting service with full configuration.
// When AccountResolver is provided, the service will attempt dynamic clearing account
// lookup before falling back to the static BankCashAccountID.
func NewPostingServiceWithConfig(cfg PostingServiceConfig) *PostingService {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	mode := "static config"
	if cfg.AccountResolver != nil {
		mode = "dynamic clearing"
	}
	logger.Info("posting service initialized",
		"mode", mode,
		"static_account_id", cfg.BankCashAccountID)

	return &PostingService{
		repo:              cfg.Repo,
		bankCashAccountID: cfg.BankCashAccountID,
		accountResolver:   cfg.AccountResolver,
		logger:            logger,
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

	// Build debit and credit postings
	debitPosting, creditPosting, err := s.buildDepositPostings(ctx, bookingLogID, event)
	if err != nil {
		timer.ObserveError(observability.ErrorCategoryValidation)
		observability.RecordDepositProcessed(event.Currency, observability.StatusError)
		return err
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
	recordDepositSuccessMetrics(event)

	return nil
}

// buildDepositPostings creates the debit and credit postings for a deposit event.
func (s *PostingService) buildDepositPostings(
	ctx context.Context,
	bookingLogID uuid.UUID,
	event DepositEvent,
) (*domain.LedgerPosting, *domain.LedgerPosting, error) {
	amount := decimalFromCents(event.AmountCents)

	currency, err := domain.ParseCurrency(event.Currency)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid currency: %w", err)
	}

	instrument, err := domain.CurrencyToInstrument(currency)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create instrument: %w", err)
	}

	money := domain.NewMoney(amount, instrument)

	debitPosting, err := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionDebit,
		money,
		event.AccountID,
		event.ValueDate,
		event.CorrelationID,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create debit posting: %w", err)
	}

	clearingAccountID := s.resolveClearingAccountForDeposit(ctx, instrument.Code)

	creditPosting, err := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionCredit,
		money,
		clearingAccountID,
		event.ValueDate,
		event.CorrelationID,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create credit posting: %w", err)
	}

	return debitPosting, creditPosting, nil
}

// recordDepositSuccessMetrics records all metrics for a successful deposit processing.
func recordDepositSuccessMetrics(event DepositEvent) {
	observability.RecordDepositProcessed(event.Currency, observability.StatusSuccess)
	observability.RecordPosting(observability.DirectionDebit, event.Currency)
	observability.RecordPosting(observability.DirectionCredit, event.Currency)
	observability.RecordPostingAmount(observability.DirectionDebit, event.Currency, event.AmountCents)
	observability.RecordPostingAmount(observability.DirectionCredit, event.Currency, event.AmountCents)
}

// GetPostingsByBookingLog retrieves all postings for a booking log
func (s *PostingService) GetPostingsByBookingLog(ctx context.Context, bookingLogID uuid.UUID) ([]*domain.LedgerPosting, error) {
	timer := observability.NewOperationTimer(observability.OperationRetrieveLedgerPosting)

	postings, err := s.repo.GetPostingsByBookingLogID(ctx, bookingLogID)
	if err != nil {
		timer.ObserveError(observability.ErrorCategoryDatabase)
		return nil, fmt.Errorf("failed to get postings: %w", err)
	}

	timer.ObserveSuccess()
	return postings, nil
}

// ValidateDoubleEntry checks that debits equal credits for a booking log
func (s *PostingService) ValidateDoubleEntry(ctx context.Context, bookingLogID uuid.UUID) (bool, error) {
	timer := observability.NewOperationTimer(observability.OperationValidateDoubleEntry)
	start := time.Now()

	postings, err := s.repo.GetPostingsByBookingLogID(ctx, bookingLogID)
	if err != nil {
		timer.ObserveError(observability.ErrorCategoryDatabase)
		return false, fmt.Errorf("failed to get postings: %w", err)
	}

	debitTotal := decimal.Zero
	creditTotal := decimal.Zero
	var currency string

	for _, posting := range postings {
		// Capture currency from first posting (all postings in a booking log have same currency)
		if currency == "" {
			currency = posting.Amount.Instrument.Code
		}
		switch posting.Direction {
		case domain.PostingDirectionDebit:
			debitTotal = debitTotal.Add(posting.Amount.Amount)
		case domain.PostingDirectionCredit:
			creditTotal = creditTotal.Add(posting.Amount.Amount)
		}
	}

	// Default currency if no postings
	if currency == "" {
		currency = observability.CurrencyUnknown
	}

	balanced := debitTotal.Equal(creditTotal)

	// Record validation duration and result
	observability.RecordBalanceValidationDuration(time.Since(start))
	timer.ObserveSuccess()

	if balanced {
		observability.RecordDoubleEntryValidation(observability.ValidationResultBalanced, currency)
	} else {
		imbalance := debitTotal.Sub(creditTotal)
		observability.RecordDoubleEntryValidation(observability.ValidationResultUnbalanced, currency)
		observability.LogBalanceValidationFailure(
			bookingLogID.String(),
			currency,
			debitTotal.String(),
			creditTotal.String(),
			imbalance.String(),
		)
	}

	return balanced, nil
}

// resolveClearingAccountForDeposit attempts dynamic clearing account lookup,
// falling back to static config on any error or empty result.
func (s *PostingService) resolveClearingAccountForDeposit(ctx context.Context, instrumentCode string) string {
	// If no resolver configured, use static fallback
	if s.accountResolver == nil {
		s.logger.Debug("using static clearing account (no resolver configured)",
			"instrument_code", instrumentCode,
			"account_id", s.bankCashAccountID)
		return s.bankCashAccountID
	}

	// Attempt dynamic lookup
	accountID, err := s.accountResolver.GetDepositClearingAccount(ctx, instrumentCode)
	if err != nil {
		// Log fallback event for observability
		s.logger.Warn("dynamic clearing account lookup failed, using static fallback",
			"instrument_code", instrumentCode,
			"fallback_account_id", s.bankCashAccountID,
			"error", err)
		observability.RecordResolverFallback(instrumentCode, observability.OperationProcessDeposit)
		return s.bankCashAccountID
	}

	// Guard against empty account ID - treat as lookup failure
	if accountID == "" {
		s.logger.Warn("dynamic clearing account lookup returned empty result, using static fallback",
			"instrument_code", instrumentCode,
			"fallback_account_id", s.bankCashAccountID)
		observability.RecordResolverFallback(instrumentCode, observability.OperationProcessDeposit)
		return s.bankCashAccountID
	}

	s.logger.Debug("using dynamic clearing account",
		"instrument_code", instrumentCode,
		"account_id", accountID)
	return accountID
}
