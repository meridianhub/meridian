// Package integration contains integration tests validating BIAN balance ownership
// between Current Account and Position Keeping services.
//
// These tests verify:
// - Account creation in Current Account creates position log in Position Keeping
// - Deposits and withdrawals in Current Account reflect in Position Keeping balances
// - Balance queries from Position Keeping match Current Account state
// - Liens are correctly computed in available/reserve balances
// - Multi-currency support (GBP, USD, EUR, JPY)
package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	cadomain "github.com/meridianhub/meridian/services/current-account/domain"
	pkdomain "github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/domain/money"
	"github.com/meridianhub/meridian/shared/platform/await"
)

// =============================================================================
// Test Infrastructure
// =============================================================================

// BalanceOwnershipTestInfra holds all test infrastructure for balance ownership tests.
type BalanceOwnershipTestInfra struct {
	pgContainer   *postgres.PostgresContainer
	pool          *pgxpool.Pool
	connStr       string
	mockCAService *MockCurrentAccountService
	mockPKService *MockPositionKeepingService
}

// setupBalanceOwnershipInfra creates the complete test infrastructure.
func setupBalanceOwnershipInfra(t *testing.T) *BalanceOwnershipTestInfra {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	infra := &BalanceOwnershipTestInfra{}

	// Setup PostgreSQL
	infra.setupPostgres(ctx, t)

	// Setup mock services
	infra.mockCAService = NewMockCurrentAccountService()
	infra.mockPKService = NewMockPositionKeepingService()

	// Register cleanup
	t.Cleanup(func() {
		infra.cleanup()
	})

	return infra
}

// setupPostgres creates PostgreSQL testcontainer with schemas for both services.
func (infra *BalanceOwnershipTestInfra) setupPostgres(ctx context.Context, t *testing.T) {
	t.Helper()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("balance_ownership_test"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
				wait.ForListeningPort("5432/tcp").
					WithStartupTimeout(30*time.Second),
			),
		),
	)
	require.NoError(t, err, "failed to start postgres container")
	infra.pgContainer = pgContainer

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string")
	infra.connStr = connStr

	// Create connection pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "failed to create connection pool")
	infra.pool = pool

	// Create schemas for both services
	infra.createSchemas(ctx, t)
}

// createSchemas creates database schemas for Current Account and Position Keeping.
func (infra *BalanceOwnershipTestInfra) createSchemas(ctx context.Context, t *testing.T) {
	t.Helper()

	// Ensure pgcrypto extension for gen_random_uuid() (available built-in in PG14+, but safe to create)
	_, err := infra.pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto`)
	require.NoError(t, err, "failed to create pgcrypto extension")

	// Create Current Account schema
	_, err = infra.pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS current_account`)
	require.NoError(t, err, "failed to create current_account schema")

	_, err = infra.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS current_account.accounts (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			account_id VARCHAR(34) NOT NULL UNIQUE,
			iban VARCHAR(34) NOT NULL,
			party_id VARCHAR(100) NOT NULL,
			balance_amount NUMERIC(38,18) NOT NULL DEFAULT 0,
			balance_currency VARCHAR(3) NOT NULL,
			available_balance_amount NUMERIC(38,18) NOT NULL DEFAULT 0,
			status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
			freeze_reason TEXT,
			overdraft_limit_amount NUMERIC(38,18) NOT NULL DEFAULT 0,
			overdraft_enabled BOOLEAN NOT NULL DEFAULT false,
			overdraft_rate NUMERIC(10,4) NOT NULL DEFAULT 0,
			balance_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			version BIGINT NOT NULL DEFAULT 1,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	require.NoError(t, err, "failed to create accounts table")

	_, err = infra.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS current_account.liens (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			lien_id VARCHAR(50) NOT NULL UNIQUE,
			account_id VARCHAR(34) NOT NULL,
			amount_value NUMERIC(38,18) NOT NULL,
			amount_currency VARCHAR(3) NOT NULL,
			lien_type VARCHAR(20) NOT NULL,
			purpose TEXT NOT NULL,
			reference VARCHAR(100),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ,
			released_at TIMESTAMPTZ,
			CONSTRAINT fk_lien_account FOREIGN KEY (account_id) REFERENCES current_account.accounts(account_id)
		)
	`)
	require.NoError(t, err, "failed to create liens table")

	// Create Position Keeping schema
	_, err = infra.pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS position_keeping`)
	require.NoError(t, err, "failed to create position_keeping schema")

	_, err = infra.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS position_keeping.financial_position_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			log_id UUID NOT NULL UNIQUE,
			account_id VARCHAR(34) NOT NULL,
			current_status VARCHAR(20) NOT NULL,
			reconciliation_status VARCHAR(20) NOT NULL,
			opening_balance_amount NUMERIC(38,18),
			opening_balance_currency VARCHAR(3),
			opening_balance_recorded_at TIMESTAMPTZ,
			version BIGINT NOT NULL DEFAULT 1,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_by VARCHAR(100) NOT NULL,
			updated_by VARCHAR(100) NOT NULL
		)
	`)
	require.NoError(t, err, "failed to create financial_position_log table")

	_, err = infra.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS position_keeping.transaction_log_entry (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			entry_id UUID NOT NULL,
			financial_position_log_id UUID NOT NULL,
			transaction_id UUID NOT NULL,
			account_id VARCHAR(34) NOT NULL,
			amount_cents BIGINT NOT NULL,
			currency VARCHAR(3) NOT NULL,
			direction VARCHAR(10) NOT NULL,
			timestamp TIMESTAMPTZ NOT NULL,
			description TEXT,
			reference VARCHAR(100),
			source VARCHAR(50) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_by VARCHAR(100) NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_by VARCHAR(100) NOT NULL,
			CONSTRAINT fk_entry_log FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id) ON DELETE CASCADE
		)
	`)
	require.NoError(t, err, "failed to create transaction_log_entry table")
}

// cleanup releases all test infrastructure resources.
func (infra *BalanceOwnershipTestInfra) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if infra.pool != nil {
		infra.pool.Close()
	}
	if infra.pgContainer != nil {
		_ = infra.pgContainer.Terminate(ctx)
	}
}

// =============================================================================
// Mock Services
// =============================================================================

// MockCurrentAccountService simulates Current Account service behavior.
// Thread-safe for concurrent test execution.
type MockCurrentAccountService struct {
	mu       sync.RWMutex
	accounts map[string]cadomain.CurrentAccount
	liens    map[string][]pkdomain.AmountBlock
}

// NewMockCurrentAccountService creates a new mock Current Account service.
func NewMockCurrentAccountService() *MockCurrentAccountService {
	return &MockCurrentAccountService{
		accounts: make(map[string]cadomain.CurrentAccount),
		liens:    make(map[string][]pkdomain.AmountBlock),
	}
}

// CreateAccount creates a new account.
func (m *MockCurrentAccountService) CreateAccount(ctx context.Context, accountID, iban, partyID, currency string) (cadomain.CurrentAccount, error) {
	account, err := cadomain.NewCurrentAccount(accountID, iban, partyID, currency)
	if err != nil {
		return cadomain.CurrentAccount{}, err
	}
	m.mu.Lock()
	m.accounts[accountID] = account
	m.mu.Unlock()
	return account, nil
}

// Deposit adds funds to an account.
func (m *MockCurrentAccountService) Deposit(ctx context.Context, accountID string, amount money.Money) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	account, ok := m.accounts[accountID]
	if !ok {
		return fmt.Errorf("account not found: %s", accountID)
	}
	// Convert legacy money.Money to cadomain.Amount via minor units
	caAmount, err := cadomain.NewMoney(amount.Currency().String(), amount.AmountCents())
	if err != nil {
		return err
	}
	updated, err := account.Deposit(caAmount)
	if err != nil {
		return err
	}
	m.accounts[accountID] = updated
	return nil
}

// Withdraw removes funds from an account.
func (m *MockCurrentAccountService) Withdraw(ctx context.Context, accountID string, amount money.Money) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	account, ok := m.accounts[accountID]
	if !ok {
		return fmt.Errorf("account not found: %s", accountID)
	}
	// Convert legacy money.Money to cadomain.Amount via minor units
	caAmount, err := cadomain.NewMoney(amount.Currency().String(), amount.AmountCents())
	if err != nil {
		return err
	}
	updated, err := account.Withdraw(caAmount)
	if err != nil {
		return err
	}
	m.accounts[accountID] = updated
	return nil
}

// GetBalance returns the current balance.
func (m *MockCurrentAccountService) GetBalance(ctx context.Context, accountID string) (money.Money, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	account, ok := m.accounts[accountID]
	if !ok {
		return money.Money{}, fmt.Errorf("account not found: %s", accountID)
	}
	// Convert cadomain.Amount to the legacy money.Money type via minor units
	caBalance := account.Balance()
	minorUnits, err := caBalance.ToMinorUnits()
	if err != nil {
		return money.Money{}, fmt.Errorf("failed to convert balance to minor units: %w", err)
	}
	result, err := money.NewFromMinorUnits(minorUnits, money.Currency(caBalance.InstrumentCode()))
	if err != nil {
		return money.Money{}, fmt.Errorf("failed to convert balance to money: %w", err)
	}
	return result, nil
}

// AddLien adds a lien to an account.
func (m *MockCurrentAccountService) AddLien(ctx context.Context, accountID string, lien pkdomain.AmountBlock) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.liens[accountID] = append(m.liens[accountID], lien)
	return nil
}

// GetActiveAmountBlocks returns all active liens/amount blocks for an account.
// Implements pkdomain.CurrentAccountClient interface used by Position Keeping.
func (m *MockCurrentAccountService) GetActiveAmountBlocks(ctx context.Context, accountID string) ([]pkdomain.AmountBlock, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.liens[accountID], nil
}

// MockPositionKeepingService simulates Position Keeping service behavior.
// Thread-safe for concurrent test execution.
type MockPositionKeepingService struct {
	mu       sync.RWMutex
	logs     map[string]*pkdomain.FinancialPositionLog
	infra    *BalanceOwnershipTestInfra
	caClient pkdomain.CurrentAccountClient
}

// NewMockPositionKeepingService creates a new mock Position Keeping service.
func NewMockPositionKeepingService() *MockPositionKeepingService {
	return &MockPositionKeepingService{
		logs: make(map[string]*pkdomain.FinancialPositionLog),
	}
}

// SetInfra sets the test infrastructure for database access.
func (m *MockPositionKeepingService) SetInfra(infra *BalanceOwnershipTestInfra) {
	m.infra = infra
}

// SetCurrentAccountClient sets the Current Account client for lien queries.
func (m *MockPositionKeepingService) SetCurrentAccountClient(client pkdomain.CurrentAccountClient) {
	m.caClient = client
}

// CreatePositionLog creates a position log with opening balance.
func (m *MockPositionKeepingService) CreatePositionLog(ctx context.Context, accountID string, openingBalance money.Money) (*pkdomain.FinancialPositionLog, error) {
	// Convert money.Money to pkdomain.Money
	pkOpeningBalance := pkdomain.MustNewMoney(openingBalance.Amount(), pkdomain.Currency(openingBalance.Currency().String()))

	// Create initial entry for opening balance if non-zero
	// Use DEBIT direction for positive balance (increases the account balance)
	var initialEntry *pkdomain.TransactionLogEntry
	if !openingBalance.IsZero() {
		var err error
		initialEntry, err = pkdomain.NewTransactionLogEntry(
			uuid.New(),
			accountID,
			pkOpeningBalance,
			pkdomain.PostingDirectionDebit, // DEBIT adds to balance for positive opening balance
			time.Now().UTC(),
			"Opening balance",
			"INTEGRATION-TEST",
			pkdomain.TransactionSourceOpeningBalance,
		)
		if err != nil {
			return nil, err
		}
	}

	log, err := pkdomain.NewFinancialPositionLog(accountID, initialEntry, nil)
	if err != nil {
		return nil, err
	}

	// Store the opening balance for reference (even though it's tracked via entry)
	// Note: We can't set OpeningBalance directly since it's a public field but semantically
	// the log uses entries for balance computation. We leave OpeningBalance at zero value.

	m.mu.Lock()
	m.logs[accountID] = log
	m.mu.Unlock()

	// Persist to database
	if m.infra != nil {
		err = m.persistLog(ctx, log)
		if err != nil {
			return nil, err
		}
	}

	return log, nil
}

// RecordTransaction records a transaction entry in the position log.
func (m *MockPositionKeepingService) RecordTransaction(ctx context.Context, accountID string, txnID uuid.UUID, amount money.Money, direction pkdomain.PostingDirection, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	log, ok := m.logs[accountID]
	if !ok {
		return fmt.Errorf("position log not found for account: %s", accountID)
	}

	// Convert money.Money to pkdomain.Money
	pkAmount := pkdomain.MustNewMoney(amount.Amount(), pkdomain.Currency(amount.Currency().String()))

	entry, err := pkdomain.NewTransactionLogEntry(
		txnID,
		accountID,
		pkAmount,
		direction,
		time.Now().UTC(),
		reference,
		reference,
		pkdomain.TransactionSourceCurrentAccount,
	)
	if err != nil {
		return err
	}

	err = log.AddEntry(entry)
	if err != nil {
		return err
	}

	// Persist to database
	if m.infra != nil {
		err = m.persistEntry(ctx, log.LogID, entry)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetAccountBalance returns a specific balance type for an account.
func (m *MockPositionKeepingService) GetAccountBalance(ctx context.Context, accountID string, balanceType positionkeepingv1.BalanceType, currency string) (*money.Money, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	log, ok := m.logs[accountID]
	if !ok {
		return nil, fmt.Errorf("position log not found for account: %s", accountID)
	}

	// Determine currency from entries or from filter
	var currencyCode string
	if len(log.TransactionLogEntries) > 0 {
		currencyCode = log.TransactionLogEntries[0].Amount.Instrument.Code
	} else if currency != "" {
		currencyCode = currency
	} else {
		return nil, fmt.Errorf("cannot determine currency for account: %s", accountID)
	}

	// Create a LogBalanceComputer to calculate balances
	// Pass zero as openingBalance since the log already contains the opening balance as a transaction entry
	zeroBalance := pkdomain.MustNewMoney(decimal.Zero, pkdomain.Currency(currencyCode))
	lbc, err := pkdomain.NewLogBalanceComputer(log, zeroBalance, m.caClient)
	if err != nil {
		return nil, err
	}

	var pkBalance pkdomain.Balance
	var balErr error

	switch balanceType {
	case positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT:
		pkBalance, balErr = lbc.CurrentBalance()
	case positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER:
		pkBalance, balErr = lbc.CurrentBalance()
	case positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE:
		if m.caClient == nil {
			return nil, fmt.Errorf("current account client not configured")
		}
		pkBalance, balErr = lbc.ReserveBalance(ctx)
	case positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE:
		if m.caClient == nil {
			return nil, fmt.Errorf("current account client not configured")
		}
		// Available = Current - Reserve
		currentBal, currentErr := lbc.CurrentBalance()
		if currentErr != nil {
			return nil, currentErr
		}
		reserveBal, reserveErr := lbc.ReserveBalance(ctx)
		if reserveErr != nil {
			return nil, reserveErr
		}
		availableAmount, subErr := currentBal.Amount.Subtract(reserveBal.Amount)
		if subErr != nil {
			return nil, subErr
		}
		pkBalance = pkdomain.Balance{
			Amount: availableAmount,
			Type:   currentBal.Type,
			AsOf:   currentBal.AsOf,
		}
		balErr = nil
	default:
		return nil, fmt.Errorf("unsupported balance type: %v", balanceType)
	}

	if balErr != nil {
		return nil, balErr
	}

	// Convert pkdomain.Money to money.Money for return value
	balance := money.MustNew(pkBalance.Amount.Amount, money.Currency(pkBalance.Amount.Instrument.Code))

	// Check currency filter
	if currency != "" && balance.Currency().String() != currency {
		return nil, fmt.Errorf("currency mismatch: expected %s, got %s", currency, balance.Currency().String())
	}

	return &balance, nil
}

// persistLog persists a financial position log to the database.
func (m *MockPositionKeepingService) persistLog(ctx context.Context, log *pkdomain.FinancialPositionLog) error {
	var openingAmount *decimal.Decimal
	var openingCurrency *string
	var openingRecordedAt *time.Time

	if !log.OpeningBalance.IsZero() {
		amount := log.OpeningBalance.Amount
		currency := log.OpeningBalance.Instrument.Code
		recordedAt := log.OpeningBalanceRecordedAt
		openingAmount = &amount
		openingCurrency = &currency
		openingRecordedAt = &recordedAt
	}

	_, err := m.infra.pool.Exec(ctx, `
		INSERT INTO position_keeping.financial_position_log
		(log_id, account_id, current_status, reconciliation_status,
		 opening_balance_amount, opening_balance_currency, opening_balance_recorded_at,
		 version, created_at, updated_at, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, log.LogID, log.AccountID, log.StatusTracking.CurrentStatus, log.StatusTracking.ReconciliationStatus,
		openingAmount, openingCurrency, openingRecordedAt,
		log.Version, log.CreatedAt, log.UpdatedAt, "TEST", "TEST")

	return err
}

// persistEntry persists a transaction log entry to the database.
func (m *MockPositionKeepingService) persistEntry(ctx context.Context, logID uuid.UUID, entry *pkdomain.TransactionLogEntry) error {
	// Get the financial_position_log database ID
	var dbID uuid.UUID
	err := m.infra.pool.QueryRow(ctx, `
		SELECT id FROM position_keeping.financial_position_log WHERE log_id = $1
	`, logID).Scan(&dbID)
	if err != nil {
		return err
	}

	// Convert amount to minor units (cents) based on currency precision
	precision := int32(entry.Amount.Instrument.Precision)
	amountCents := entry.Amount.Amount.Shift(precision).IntPart()

	_, err = m.infra.pool.Exec(ctx, `
		INSERT INTO position_keeping.transaction_log_entry
		(entry_id, financial_position_log_id, transaction_id, account_id,
		 amount_cents, currency, direction, timestamp, description, reference, source,
		 created_at, created_by, updated_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`, entry.EntryID, dbID, entry.TransactionID, entry.AccountID,
		amountCents, entry.Amount.Instrument.Code, entry.Direction, entry.Timestamp,
		entry.Description, entry.Reference, entry.Source,
		time.Now().UTC(), "TEST", time.Now().UTC(), "TEST")

	return err
}

// calculateReserveBalance sums all active liens.
// Returns zero money in the given currency if no liens exist.
func calculateReserveBalance(liens []pkdomain.AmountBlock, defaultCurrency money.Currency) (*money.Money, error) {
	if len(liens) == 0 {
		zeroMoney := money.MustNew(decimal.Zero, defaultCurrency)
		return &zeroMoney, nil
	}

	// Use the currency of the first lien
	// pkdomain.Money is quantity.Money which has Amount and Instrument fields
	currency := money.Currency(liens[0].Amount.Instrument.Code)
	total := money.MustNew(decimal.Zero, currency)

	for _, lien := range liens {
		// Convert pkdomain.Money to money.Money for addition
		lienMoney := money.MustNew(lien.Amount.Amount, currency)
		sum, err := total.Add(lienMoney)
		if err != nil {
			return nil, err
		}
		total = sum
	}

	return &total, nil
}

// =============================================================================
// Helper Functions
// =============================================================================

// createTestAccount creates an account in Current Account and initializes position log.
// Both services are initialized with the same opening balance to maintain consistency.
func createTestAccount(t *testing.T, infra *BalanceOwnershipTestInfra, accountID, currency string, openingBalance money.Money) {
	t.Helper()
	require.GreaterOrEqual(t, len(accountID), 6, "accountID must be at least 6 characters for IBAN generation")
	ctx := context.Background()

	// Create account in Current Account
	iban := fmt.Sprintf("GB29NWBK%s%s", accountID[:6], accountID[6:])
	partyID := fmt.Sprintf("PARTY-%s", accountID)

	_, err := infra.mockCAService.CreateAccount(ctx, accountID, iban, partyID, currency)
	require.NoError(t, err, "failed to create account in Current Account")

	// Initialize CA balance with opening balance (simulates migration scenario)
	if !openingBalance.IsZero() {
		err = infra.mockCAService.Deposit(ctx, accountID, openingBalance)
		require.NoError(t, err, "failed to set opening balance in Current Account")
	}

	// Create position log in Position Keeping
	infra.mockPKService.SetInfra(infra)
	_, err = infra.mockPKService.CreatePositionLog(ctx, accountID, openingBalance)
	require.NoError(t, err, "failed to create position log in Position Keeping")
}

// recordDeposit records a deposit in both services.
// Note: In Position Keeping domain, PostingDirectionDebit ADDS to balance (see balance_computer.go).
// This follows the implementation where Debit increases the position, not standard accounting convention.
func recordDeposit(t *testing.T, infra *BalanceOwnershipTestInfra, accountID string, amount money.Money, reference string) {
	t.Helper()
	ctx := context.Background()

	// Record in Current Account
	err := infra.mockCAService.Deposit(ctx, accountID, amount)
	require.NoError(t, err, "failed to record deposit in Current Account")

	// Record in Position Keeping - Debit direction adds to balance
	txnID := uuid.New()
	err = infra.mockPKService.RecordTransaction(ctx, accountID, txnID, amount, pkdomain.PostingDirectionDebit, reference)
	require.NoError(t, err, "failed to record deposit in Position Keeping")
}

// recordWithdrawal records a withdrawal in both services.
// Note: In Position Keeping domain, PostingDirectionCredit SUBTRACTS from balance (see balance_computer.go).
// This follows the implementation where Credit decreases the position, not standard accounting convention.
func recordWithdrawal(t *testing.T, infra *BalanceOwnershipTestInfra, accountID string, amount money.Money, reference string) {
	t.Helper()
	ctx := context.Background()

	// Record in Current Account
	err := infra.mockCAService.Withdraw(ctx, accountID, amount)
	require.NoError(t, err, "failed to record withdrawal in Current Account")

	// Record in Position Keeping - Credit direction subtracts from balance
	txnID := uuid.New()
	err = infra.mockPKService.RecordTransaction(ctx, accountID, txnID, amount, pkdomain.PostingDirectionCredit, reference)
	require.NoError(t, err, "failed to record withdrawal in Position Keeping")
}

// =============================================================================
// Test Scenarios
// =============================================================================

func TestBalanceOwnership_CreateAccountAndVerifyBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupBalanceOwnershipInfra(t)
	ctx := context.Background()

	t.Run("create_GBP_account_with_opening_balance", func(t *testing.T) {
		accountID := "ACC-GBP-001"
		openingBalance := money.MustNew(decimal.NewFromInt(1000), money.CurrencyGBP)

		createTestAccount(t, infra, accountID, "GBP", openingBalance)

		// Verify balance in Current Account
		balance, err := infra.mockCAService.GetBalance(ctx, accountID)
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(1000).Equal(balance.Amount()), "CA balance should be 1000, got %s", balance.Amount())

		// Verify balance in Position Keeping
		pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(1000).Equal(pkBalance.Amount()), "PK balance should be 1000, got %s", pkBalance.Amount())
		assert.Equal(t, "GBP", pkBalance.Currency().String())
	})

	t.Run("create_USD_account_with_zero_opening_balance", func(t *testing.T) {
		accountID := "ACC-USD-001"
		openingBalance := money.MustNew(decimal.Zero, money.CurrencyUSD)

		createTestAccount(t, infra, accountID, "USD", openingBalance)

		// Verify balance in Position Keeping
		pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "USD")
		require.NoError(t, err)
		assert.True(t, pkBalance.IsZero())
		assert.Equal(t, "USD", pkBalance.Currency().String())
	})
}

func TestBalanceOwnership_DepositAndWithdrawalFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupBalanceOwnershipInfra(t)
	ctx := context.Background()

	accountID := "ACC-TXN-001"
	openingBalance := money.MustNew(decimal.NewFromInt(5000), money.CurrencyGBP)

	t.Run("setup_account", func(t *testing.T) {
		createTestAccount(t, infra, accountID, "GBP", openingBalance)
	})

	t.Run("deposit_increases_balance", func(t *testing.T) {
		depositAmount := money.MustNew(decimal.NewFromInt(1000), money.CurrencyGBP)
		recordDeposit(t, infra, accountID, depositAmount, "DEPOSIT-001")

		// Wait for balance to update using await.Until
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(100 * time.Millisecond).
			Until(func() bool {
				pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
				if err != nil {
					return false
				}
				return pkBalance.Amount().Equal(decimal.NewFromInt(6000))
			})
		require.NoError(t, err, "balance did not update to expected value")

		// Final verification
		pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(6000).Equal(pkBalance.Amount()), "balance should be 6000, got %s", pkBalance.Amount())
	})

	t.Run("withdrawal_decreases_balance", func(t *testing.T) {
		withdrawalAmount := money.MustNew(decimal.NewFromInt(2000), money.CurrencyGBP)
		recordWithdrawal(t, infra, accountID, withdrawalAmount, "WITHDRAWAL-001")

		// Wait for balance to update
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(100 * time.Millisecond).
			Until(func() bool {
				pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
				if err != nil {
					return false
				}
				return pkBalance.Amount().Equal(decimal.NewFromInt(4000))
			})
		require.NoError(t, err, "balance did not update to expected value")

		// Final verification
		pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(4000).Equal(pkBalance.Amount()), "balance should be 4000, got %s", pkBalance.Amount())
	})

	t.Run("multiple_transactions_compute_correctly", func(t *testing.T) {
		// Execute multiple deposits and withdrawals
		recordDeposit(t, infra, accountID, money.MustNew(decimal.NewFromInt(500), money.CurrencyGBP), "DEPOSIT-002")
		recordWithdrawal(t, infra, accountID, money.MustNew(decimal.NewFromInt(300), money.CurrencyGBP), "WITHDRAWAL-002")
		recordDeposit(t, infra, accountID, money.MustNew(decimal.NewFromInt(800), money.CurrencyGBP), "DEPOSIT-003")

		// Expected: 4000 + 500 - 300 + 800 = 5000
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(100 * time.Millisecond).
			Until(func() bool {
				pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
				if err != nil {
					return false
				}
				return pkBalance.Amount().Equal(decimal.NewFromInt(5000))
			})
		require.NoError(t, err, "balance did not compute correctly after multiple transactions")

		// Final verification
		pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(5000).Equal(pkBalance.Amount()), "balance should be 5000, got %s", pkBalance.Amount())
	})
}

func TestBalanceOwnership_LiensAndAvailableBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupBalanceOwnershipInfra(t)
	ctx := context.Background()

	accountID := "ACC-LIEN-001"
	openingBalance := money.MustNew(decimal.NewFromInt(10000), money.CurrencyGBP)

	// Configure Position Keeping to use Current Account for liens
	infra.mockPKService.SetCurrentAccountClient(infra.mockCAService)

	t.Run("setup_account", func(t *testing.T) {
		createTestAccount(t, infra, accountID, "GBP", openingBalance)
	})

	t.Run("reserve_balance_without_liens_is_zero", func(t *testing.T) {
		pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE, "GBP")
		require.NoError(t, err)
		assert.True(t, pkBalance.IsZero(), "reserve balance should be zero without liens")
	})

	t.Run("available_balance_equals_current_without_liens", func(t *testing.T) {
		pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE, "GBP")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(10000).Equal(pkBalance.Amount()), "balance should be 10000, got %s", pkBalance.Amount())
	})

	t.Run("add_liens_and_verify_reserve_balance", func(t *testing.T) {
		// Add first lien
		lien1 := pkdomain.AmountBlock{
			BlockID:   "LIEN-001",
			Amount:    pkdomain.MustNewMoney(decimal.NewFromInt(2000), pkdomain.CurrencyGBP),
			BlockType: pkdomain.AmountBlockTypePending,
			Purpose:   "Payment authorization hold",
		}
		err := infra.mockCAService.AddLien(ctx, accountID, lien1)
		require.NoError(t, err)

		// Add second lien
		lien2 := pkdomain.AmountBlock{
			BlockID:   "LIEN-002",
			Amount:    pkdomain.MustNewMoney(decimal.NewFromInt(1500), pkdomain.CurrencyGBP),
			BlockType: pkdomain.AmountBlockTypeTemporary,
			Purpose:   "Preauthorization hold",
		}
		err = infra.mockCAService.AddLien(ctx, accountID, lien2)
		require.NoError(t, err)

		// Wait for reserve balance to update
		err = await.New().
			AtMost(5 * time.Second).
			PollInterval(100 * time.Millisecond).
			Until(func() bool {
				reserve, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE, "GBP")
				if err != nil {
					return false
				}
				// Reserve should be sum of liens: 2000 + 1500 = 3500
				return reserve.Amount().Equal(decimal.NewFromInt(3500))
			})
		require.NoError(t, err, "reserve balance did not update correctly")

		// Verify reserve balance
		reserve, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE, "GBP")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(3500).Equal(reserve.Amount()), "reserve should be 3500, got %s", reserve.Amount())
	})

	t.Run("available_balance_is_current_minus_reserve", func(t *testing.T) {
		// Current balance: 10000
		// Reserve balance: 3500
		// Available: 10000 - 3500 = 6500
		available, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE, "GBP")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(6500).Equal(available.Amount()), "available should be 6500, got %s", available.Amount())
	})
}

func TestBalanceOwnership_MultiCurrencySupport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupBalanceOwnershipInfra(t)
	ctx := context.Background()

	currencies := []struct {
		code           money.Currency
		accountID      string
		openingBalance decimal.Decimal
	}{
		{money.CurrencyGBP, "ACC-MC-GBP", decimal.NewFromInt(1000)},
		{money.CurrencyUSD, "ACC-MC-USD", decimal.NewFromInt(2000)},
		{money.CurrencyEUR, "ACC-MC-EUR", decimal.NewFromInt(1500)},
		{money.CurrencyJPY, "ACC-MC-JPY", decimal.NewFromInt(100000)}, // JPY has 0 decimal places
	}

	for _, tc := range currencies {
		t.Run(fmt.Sprintf("create_%s_account", tc.code), func(t *testing.T) {
			openingBalance := money.MustNew(tc.openingBalance, tc.code)
			createTestAccount(t, infra, tc.accountID, tc.code.String(), openingBalance)

			// Verify balance in Position Keeping
			pkBalance, err := infra.mockPKService.GetAccountBalance(ctx, tc.accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, tc.code.String())
			require.NoError(t, err)
			assert.Equal(t, tc.openingBalance, pkBalance.Amount())
			assert.Equal(t, tc.code.String(), pkBalance.Currency().String())
		})
	}

	t.Run("currency_filter_rejects_mismatched_currency", func(t *testing.T) {
		// Try to query GBP account with USD filter
		_, err := infra.mockPKService.GetAccountBalance(ctx, "ACC-MC-GBP", positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "USD")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "currency mismatch")
	})
}

func TestBalanceOwnership_BalanceConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupBalanceOwnershipInfra(t)
	ctx := context.Background()

	accountID := "ACC-CONSISTENCY-001"
	openingBalance := money.MustNew(decimal.NewFromInt(10000), money.CurrencyGBP)

	t.Run("setup_account", func(t *testing.T) {
		createTestAccount(t, infra, accountID, "GBP", openingBalance)
	})

	t.Run("ledger_balance_equals_current_balance", func(t *testing.T) {
		current, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
		require.NoError(t, err)

		ledger, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER, "GBP")
		require.NoError(t, err)

		assert.Equal(t, current.Amount(), ledger.Amount(), "ledger balance should equal current balance")
	})

	t.Run("consistency_after_transactions", func(t *testing.T) {
		// Execute several transactions
		recordDeposit(t, infra, accountID, money.MustNew(decimal.NewFromInt(3000), money.CurrencyGBP), "DEP-001")
		recordWithdrawal(t, infra, accountID, money.MustNew(decimal.NewFromInt(1500), money.CurrencyGBP), "WD-001")
		recordDeposit(t, infra, accountID, money.MustNew(decimal.NewFromInt(500), money.CurrencyGBP), "DEP-002")

		// Expected: 10000 + 3000 - 1500 + 500 = 12000
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(100 * time.Millisecond).
			Until(func() bool {
				current, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
				if err != nil {
					return false
				}
				caBalance, err := infra.mockCAService.GetBalance(ctx, accountID)
				if err != nil {
					return false
				}
				// Both should be 12000 and equal
				return current.Amount().Equal(decimal.NewFromInt(12000)) &&
					current.Amount().Equal(caBalance.Amount())
			})
		require.NoError(t, err, "balances did not reconcile correctly")

		// Final verification - balances should be consistent across services
		pkCurrent, err := infra.mockPKService.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
		require.NoError(t, err)

		caBalance, err := infra.mockCAService.GetBalance(ctx, accountID)
		require.NoError(t, err)

		assert.True(t, decimal.NewFromInt(12000).Equal(pkCurrent.Amount()), "PK balance should be 12000, got %s", pkCurrent.Amount())
		assert.True(t, pkCurrent.Amount().Equal(caBalance.Amount()), "Position Keeping and Current Account balances must match")
	})
}

func TestBalanceOwnership_ErrorCases(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupBalanceOwnershipInfra(t)
	ctx := context.Background()

	t.Run("query_nonexistent_account_returns_error", func(t *testing.T) {
		_, err := infra.mockPKService.GetAccountBalance(ctx, "NONEXISTENT", positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, "GBP")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("reserve_balance_without_client_returns_error", func(t *testing.T) {
		accountID := "ACC-NO-CLIENT-001"
		openingBalance := money.MustNew(decimal.NewFromInt(1000), money.CurrencyGBP)
		createTestAccount(t, infra, accountID, "GBP", openingBalance)

		// Don't configure Current Account client
		mockPKNoClient := NewMockPositionKeepingService()
		mockPKNoClient.SetInfra(infra)
		mockPKNoClient.logs = infra.mockPKService.logs

		_, err := mockPKNoClient.GetAccountBalance(ctx, accountID, positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE, "GBP")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "current account client")
	})
}
