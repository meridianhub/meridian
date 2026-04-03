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
	"github.com/meridianhub/meridian/shared/pkg/refdata"
	"github.com/shopspring/decimal"
)

// PostingService handles ledger posting operations
type PostingService struct {
	repo               *persistence.LedgerRepository
	bankCashAccountID  string
	accountResolver    *AccountResolver
	instrumentResolver refdata.InstrumentResolver
	logger             *slog.Logger
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

	// InstrumentResolver is optional. When provided, resolves instrument metadata
	// (dimension, precision) from Reference Data instead of relying on ParseCurrency.
	// When nil, falls back to legacy currency-based resolution.
	InstrumentResolver refdata.InstrumentResolver

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
		repo:               cfg.Repo,
		bankCashAccountID:  cfg.BankCashAccountID,
		accountResolver:    cfg.AccountResolver,
		instrumentResolver: cfg.InstrumentResolver,
		logger:             logger,
	}
}

// DepositEvent represents a deposit event from CurrentAccount service.
// Amount is a decimal string (e.g., "100.00") for full precision with any asset type.
// InstrumentCode identifies the instrument (e.g., "GBP", "KWH", "TONNE_CO2E").
type DepositEvent struct {
	AccountID      string
	Amount         string // Decimal as string for precision
	InstrumentCode string // e.g., "GBP", "KWH"
	CorrelationID  string
	ValueDate      time.Time
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
		observability.RecordDepositProcessed(event.InstrumentCode, observability.StatusError)
		return err
	}

	// Post both entries
	if err := debitPosting.Post("Deposit processed"); err != nil {
		timer.ObserveError(observability.ErrorCategoryInternal)
		observability.RecordDepositProcessed(event.InstrumentCode, observability.StatusError)
		return fmt.Errorf("failed to post debit: %w", err)
	}

	if err := creditPosting.Post("Deposit processed"); err != nil {
		timer.ObserveError(observability.ErrorCategoryInternal)
		observability.RecordDepositProcessed(event.InstrumentCode, observability.StatusError)
		return fmt.Errorf("failed to post credit: %w", err)
	}

	// Save both postings atomically in a transaction
	if err := s.repo.SavePostingsInTransaction(ctx, []*domain.LedgerPosting{debitPosting, creditPosting}); err != nil {
		timer.ObserveError(observability.ErrorCategoryDatabase)
		observability.RecordDepositProcessed(event.InstrumentCode, observability.StatusError)
		return fmt.Errorf("failed to save postings: %w", err)
	}

	// Record successful metrics
	timer.ObserveSuccess()
	recordDepositSuccessMetrics(event)

	return nil
}

// buildDepositPostings creates the debit and credit postings for a deposit event.
// Uses InstrumentResolver when available for proper instrument metadata; falls back
// to legacy ParseCurrency for known ISO 4217 currency codes.
func (s *PostingService) buildDepositPostings(
	ctx context.Context,
	bookingLogID uuid.UUID,
	event DepositEvent,
) (*domain.LedgerPosting, *domain.LedgerPosting, error) {
	amount, err := decimal.NewFromString(event.Amount)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid amount %q: %w", event.Amount, err)
	}

	instrument, err := s.resolveInstrument(ctx, event.InstrumentCode)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve instrument %q: %w", event.InstrumentCode, err)
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

// resolveInstrument resolves an instrument code to a domain Instrument.
// Tries InstrumentResolver first, then falls back to legacy ParseCurrency.
func (s *PostingService) resolveInstrument(ctx context.Context, code string) (domain.Instrument, error) {
	if s.instrumentResolver != nil {
		props, err := s.instrumentResolver.Resolve(ctx, code)
		if err == nil {
			return domain.NewInstrument(props.Code, 1, props.Dimension, props.Precision)
		}
		s.logger.Debug("instrument resolver failed, trying legacy currency lookup",
			"instrument_code", code,
			"error", err)
	}

	// Legacy fallback for known ISO 4217 currencies
	currency, err := domain.ParseCurrency(code)
	if err != nil {
		return domain.Instrument{}, fmt.Errorf("unknown instrument: %w", err)
	}
	return domain.CurrencyToInstrument(currency)
}

// recordDepositSuccessMetrics records all metrics for a successful deposit processing.
func recordDepositSuccessMetrics(event DepositEvent) {
	observability.RecordDepositProcessed(event.InstrumentCode, observability.StatusSuccess)
	observability.RecordPosting(observability.DirectionDebit, event.InstrumentCode)
	observability.RecordPosting(observability.DirectionCredit, event.InstrumentCode)

	// Convert amount string to float64 for metrics (precision loss acceptable for counters)
	if amountDec, err := decimal.NewFromString(event.Amount); err == nil {
		amountFloat, _ := amountDec.Float64()
		observability.RecordPostingAmountFloat(observability.DirectionDebit, event.InstrumentCode, amountFloat)
		observability.RecordPostingAmountFloat(observability.DirectionCredit, event.InstrumentCode, amountFloat)
	}
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
