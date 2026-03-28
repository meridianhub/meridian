// Package testfixtures provides factory functions and fixtures for creating
// test data consistently across the position-keeping test suite.
//
// Usage:
//
//	// Create a default financial position log for testing
//	log := testfixtures.NewFinancialPositionLog(t)
//
//	// Create with custom options
//	log := testfixtures.NewFinancialPositionLog(t,
//	    testfixtures.WithAccountID("ACC-123"),
//	    testfixtures.WithAmount(decimal.NewFromInt(1000)),
//	)
package testfixtures

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
)

// FinancialPositionLogOption is a function that configures a FinancialPositionLog for testing.
type FinancialPositionLogOption func(*financialPositionLogConfig)

type financialPositionLogConfig struct {
	logID         *uuid.UUID
	accountID     string
	transactionID *uuid.UUID
	amount        decimal.Decimal
	currency      domain.Currency
	direction     domain.PostingDirection
	source        domain.TransactionSource
	description   string
	reference     string
}

// WithLogID sets a specific log ID.
func WithLogID(id uuid.UUID) FinancialPositionLogOption {
	return func(cfg *financialPositionLogConfig) {
		cfg.logID = &id
	}
}

// WithAccountID sets the account ID.
func WithAccountID(accountID string) FinancialPositionLogOption {
	return func(cfg *financialPositionLogConfig) {
		cfg.accountID = accountID
	}
}

// WithTransactionID sets a specific transaction ID.
func WithTransactionID(id uuid.UUID) FinancialPositionLogOption {
	return func(cfg *financialPositionLogConfig) {
		cfg.transactionID = &id
	}
}

// WithAmount sets the transaction amount.
func WithAmount(amount decimal.Decimal) FinancialPositionLogOption {
	return func(cfg *financialPositionLogConfig) {
		cfg.amount = amount
	}
}

// WithCurrency sets the currency.
func WithCurrency(currency domain.Currency) FinancialPositionLogOption {
	return func(cfg *financialPositionLogConfig) {
		cfg.currency = currency
	}
}

// WithDirection sets the posting direction.
func WithDirection(direction domain.PostingDirection) FinancialPositionLogOption {
	return func(cfg *financialPositionLogConfig) {
		cfg.direction = direction
	}
}

// WithSource sets the transaction source.
func WithSource(source domain.TransactionSource) FinancialPositionLogOption {
	return func(cfg *financialPositionLogConfig) {
		cfg.source = source
	}
}

// WithDescription sets the description.
func WithDescription(description string) FinancialPositionLogOption {
	return func(cfg *financialPositionLogConfig) {
		cfg.description = description
	}
}

// WithReference sets the reference.
func WithReference(reference string) FinancialPositionLogOption {
	return func(cfg *financialPositionLogConfig) {
		cfg.reference = reference
	}
}

// NewFinancialPositionLog creates a FinancialPositionLog with sensible defaults for testing.
// Use options to customize specific fields.
func NewFinancialPositionLog(t *testing.T, opts ...FinancialPositionLogOption) *domain.FinancialPositionLog {
	t.Helper()

	cfg := buildDefaultConfig(opts)

	transactionID := uuid.New()
	if cfg.transactionID != nil {
		transactionID = *cfg.transactionID
	}

	entry := buildTestEntry(t, cfg, transactionID)
	lineage := buildTestLineage(t, transactionID)

	log, err := domain.NewFinancialPositionLog(cfg.accountID, entry, lineage)
	if err != nil {
		t.Fatalf("Failed to create financial position log: %v", err)
	}

	return log
}

// buildDefaultConfig creates a default config and applies options.
func buildDefaultConfig(opts []FinancialPositionLogOption) *financialPositionLogConfig {
	cfg := &financialPositionLogConfig{
		accountID:   "TEST-ACC-001",
		amount:      decimal.NewFromInt(100),
		currency:    domain.CurrencyGBP,
		direction:   domain.PostingDirectionDebit,
		source:      domain.TransactionSourceManual,
		description: "Test transaction",
		reference:   "TEST-REF-001",
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// buildTestEntry creates a transaction log entry from test config.
func buildTestEntry(t *testing.T, cfg *financialPositionLogConfig, transactionID uuid.UUID) *domain.TransactionLogEntry {
	t.Helper()

	money, err := domain.NewMoney(cfg.amount, cfg.currency)
	if err != nil {
		t.Fatalf("Failed to create money: %v", err)
	}

	entry, err := domain.NewTransactionLogEntry(
		transactionID, cfg.accountID, money, cfg.direction,
		time.Now().UTC(), cfg.description, cfg.reference, cfg.source,
	)
	if err != nil {
		t.Fatalf("Failed to create transaction log entry: %v", err)
	}
	return entry
}

// buildTestLineage creates a transaction lineage from a transaction ID.
func buildTestLineage(t *testing.T, transactionID uuid.UUID) *domain.TransactionLineage {
	t.Helper()

	lineage, err := domain.NewTransactionLineage(transactionID, "test-transaction", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create transaction lineage: %v", err)
	}
	return lineage
}

// NewMoney creates a Money instance with sensible defaults for testing.
func NewMoney(t *testing.T, amount decimal.Decimal, currency domain.Currency) domain.Money {
	t.Helper()
	money, err := domain.NewMoney(amount, currency)
	if err != nil {
		t.Fatalf("Failed to create money: %v", err)
	}
	return money
}

// NewTransactionCapturedEvent creates a TransactionCaptured event with sensible defaults.
func NewTransactionCapturedEvent(t *testing.T, opts ...FinancialPositionLogOption) *domain.TransactionCaptured {
	t.Helper()

	// Defaults
	cfg := &financialPositionLogConfig{
		accountID:   "TEST-ACC-001",
		amount:      decimal.NewFromInt(100),
		currency:    domain.CurrencyGBP,
		direction:   domain.PostingDirectionDebit,
		source:      domain.TransactionSourceManual,
		description: "Test transaction",
		reference:   "TEST-REF-001",
	}

	// Apply options
	for _, opt := range opts {
		opt(cfg)
	}

	// Generate IDs if not provided
	transactionID := uuid.New()
	if cfg.transactionID != nil {
		transactionID = *cfg.transactionID
	}

	// Create money
	money, err := domain.NewMoney(cfg.amount, cfg.currency)
	if err != nil {
		t.Fatalf("Failed to create money: %v", err)
	}

	// Generate log ID if not provided
	logID := uuid.New()
	if cfg.logID != nil {
		logID = *cfg.logID
	}

	return &domain.TransactionCaptured{
		LogID:         logID,
		AccountID:     cfg.accountID,
		TransactionID: transactionID,
		Amount:        money,
		Direction:     cfg.direction,
		Source:        cfg.source,
		Description:   cfg.description,
		Reference:     cfg.reference,
		CorrelationID: "TEST-CORR-001",
		Timestamp:     time.Now().UTC(),
		Version:       1,
	}
}

// NewBulkTransactionCapturedEvent creates a BulkTransactionCaptured event with sensible defaults.
func NewBulkTransactionCapturedEvent(t *testing.T, count int) *domain.BulkTransactionCaptured {
	t.Helper()

	if count < 1 {
		count = 10 // Default to 10 transactions
	}
	if count > 10000 {
		t.Fatalf("Bulk transaction count exceeds maximum of 10000: %d", count)
	}

	logIDs := make([]uuid.UUID, count)
	for i := 0; i < count; i++ {
		logIDs[i] = uuid.New()
	}

	// Safe conversion: count is validated to be in range [1, 10000]
	transactionCount := int32(count) // #nosec G115

	return &domain.BulkTransactionCaptured{
		BatchID:          uuid.New(),
		TransactionCount: transactionCount,
		LogIDs:           logIDs,
		Source:           domain.TransactionSourceImported,
		CorrelationID:    "TEST-BULK-CORR-001",
		Timestamp:        time.Now().UTC(),
		Version:          1,
	}
}

// DefaultGBPMoney returns 100 GBP for testing.
func DefaultGBPMoney(t *testing.T) domain.Money {
	t.Helper()
	return NewMoney(t, decimal.NewFromInt(100), domain.CurrencyGBP)
}

// DefaultUSDMoney returns 100 USD for testing.
func DefaultUSDMoney(t *testing.T) domain.Money {
	t.Helper()
	return NewMoney(t, decimal.NewFromInt(100), domain.CurrencyUSD)
}

// DefaultJPYMoney returns 10000 JPY for testing.
func DefaultJPYMoney(t *testing.T) domain.Money {
	t.Helper()
	return NewMoney(t, decimal.NewFromInt(10000), domain.CurrencyJPY)
}
