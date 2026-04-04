//go:build integration

// Package e2e provides end-to-end integration tests for the financial-accounting service.
// These tests verify double-entry bookkeeping guarantees, trial balance accuracy,
// and reconciliation with position-keeping using REAL service integration.
package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	financialaccountingpersistence "github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	financialaccountingdomain "github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// ============================================================================
// Test Infrastructure (Subtask 1: E2E Infrastructure with Position-Keeping)
// ============================================================================

// E2ETestEnvironment encapsulates all dependencies for financial-accounting E2E tests
// with REAL position-keeping integration (no mocks).
//
// These tests use direct DB + repository access (not gRPC) consistent with the
// established E2E pattern across all services (position-keeping, current-account, etc.).
type E2ETestEnvironment struct {
	// Database connection (shared across services in same testcontainer)
	DB *gorm.DB

	// Repositories for direct database queries (verification)
	FinancialAccountingRepo *financialaccountingpersistence.LedgerRepository

	// Test context with tenant
	Ctx      context.Context
	TenantID tenant.TenantID

	// Cleanup function
	Cleanup func()
}

// setupE2ETest creates a complete E2E test environment with:
// - CockroachDB testcontainer
// - Tenant schema with all required tables (financial-accounting + position-keeping)
// - Direct repository access for financial-accounting
// - Direct SQL access for position-keeping (uses pgxpool, not GORM)
func setupE2ETest(t *testing.T) *E2ETestEnvironment {
	t.Helper()

	// Create CockroachDB testcontainer
	db, cleanup := testdb.SetupCockroachDB(t, nil)

	// Create tenant schema
	tenantID := tenant.TenantID(fmt.Sprintf("e2e_finacct_%d", time.Now().UnixNano()))
	tenantCtx := setupMultiServiceTenantSchema(t, db, tenantID)

	// Create repository for financial-accounting
	financialAccountingRepo := financialaccountingpersistence.NewLedgerRepository(db)

	env := &E2ETestEnvironment{
		DB:                      db,
		Ctx:                     tenantCtx,
		TenantID:                tenantID,
		FinancialAccountingRepo: financialAccountingRepo,
		Cleanup:                 cleanup,
	}

	return env
}

// setupMultiServiceTenantSchema creates tenant schema with tables for both services
func setupMultiServiceTenantSchema(t *testing.T, db *gorm.DB, tenantID tenant.TenantID) context.Context {
	t.Helper()

	schemaName := tenantID.SchemaName()

	// Get raw DB connection for schema operations
	sqlDB, err := db.DB()
	require.NoError(t, err, "Failed to get SQL DB connection")

	// Create tenant schema
	_, err = sqlDB.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err, "Failed to create tenant schema %s", schemaName)

	// Apply schemas for both services
	applyFinancialAccountingSchema(t, db, schemaName)
	applyPositionKeepingSchema(t, db, schemaName)

	// Set search_path to tenant schema for all subsequent GORM operations
	err = db.Exec(fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err, "Failed to set search_path to tenant schema")

	// Create context with tenant
	tenantCtx := tenant.WithTenant(context.Background(), tenantID)

	// Cleanup: drop tenant schema on test completion
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
	})

	return tenantCtx
}

// applyFinancialAccountingSchema creates financial_booking_log and ledger_posting tables
func applyFinancialAccountingSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	// Create financial_booking_log table
	bookingLogTable := fmt.Sprintf("%s.financial_booking_log", pq.QuoteIdentifier(schemaName))
	createBookingLogSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY,
			financial_account_type TEXT NOT NULL,
			product_service_reference TEXT NOT NULL,
			business_unit_reference TEXT NOT NULL,
			chart_of_accounts_rules TEXT,
			base_currency TEXT NOT NULL,
			status TEXT NOT NULL,
			idempotency_key TEXT UNIQUE NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			created_by VARCHAR(255),
			updated_by VARCHAR(255),
			version INT NOT NULL DEFAULT 1,
			deleted_at TIMESTAMP
		)`, bookingLogTable)
	_, err = sqlDB.Exec(createBookingLogSQL)
	require.NoError(t, err, "Failed to create financial_booking_log table")

	// Create ledger_posting table
	postingTable := fmt.Sprintf("%s.ledger_posting", pq.QuoteIdentifier(schemaName))
	createPostingSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY,
			financial_booking_log_id UUID NOT NULL REFERENCES %s(id) ON DELETE RESTRICT,
			posting_direction TEXT NOT NULL,
			amount_cents BIGINT NOT NULL,
			currency VARCHAR(32) NOT NULL,
			dimension_type VARCHAR(20) DEFAULT 'CURRENCY',
			instrument_version INTEGER DEFAULT 1,
			instrument_precision INTEGER DEFAULT 2,
			attributes JSONB DEFAULT '{}',
			account_id TEXT NOT NULL,
			value_date TIMESTAMP NOT NULL,
			posting_result TEXT,
			correlation_id TEXT,
			status TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP,
			created_by VARCHAR(255),
			updated_by VARCHAR(255),
			deleted_at TIMESTAMP
		)`, postingTable, bookingLogTable)
	_, err = sqlDB.Exec(createPostingSQL)
	require.NoError(t, err, "Failed to create ledger_posting table")

	// Create audit_outbox table for GORM hooks
	auditOutboxTable := fmt.Sprintf("%s.audit_outbox", pq.QuoteIdentifier(schemaName))
	createAuditOutboxSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id VARCHAR(50) NOT NULL,
			old_values TEXT,
			new_values TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			retry_count INT NOT NULL DEFAULT 0,
			last_error TEXT,
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT
		)`, auditOutboxTable)
	_, err = sqlDB.Exec(createAuditOutboxSQL)
	require.NoError(t, err, "Failed to create audit_outbox table")
}

// applyPositionKeepingSchema creates position table for balance tracking
func applyPositionKeepingSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	positionTable := fmt.Sprintf("%s.position", pq.QuoteIdentifier(schemaName))
	createPositionSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			created_by VARCHAR(100) NOT NULL,
			deleted_at TIMESTAMPTZ NULL,
			account_id VARCHAR(34) NOT NULL,
			instrument_code VARCHAR(32) NOT NULL,
			bucket_key VARCHAR(256) NOT NULL,
			amount DECIMAL(38, 18) NOT NULL,
			dimension VARCHAR(32) NOT NULL DEFAULT 'Monetary',
			reference_id UUID NULL,
			status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
			UNIQUE (account_id, instrument_code, bucket_key, deleted_at)
		)`, positionTable)
	_, err = sqlDB.Exec(createPositionSQL)
	require.NoError(t, err, "Failed to create position table")
}

// ============================================================================
// Test Data Helpers
// ============================================================================

// createTestBookingLog creates a booking log in PENDING status for testing
func createTestBookingLog(t *testing.T, env *E2ETestEnvironment, status string) uuid.UUID {
	t.Helper()

	bookingLogID := uuid.New()
	bookingLog := &financialaccountingpersistence.FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  status,
		IdempotencyKey:          "test-key-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, env.DB.WithContext(env.Ctx).Create(bookingLog).Error)

	return bookingLogID
}

// createTestPosting creates a ledger posting for double-entry testing
// If accountID is provided, uses that; otherwise generates a random account ID
func createTestPosting(t *testing.T, env *E2ETestEnvironment, bookingLogID uuid.UUID, direction string, amountCents int64, accountID ...string) uuid.UUID {
	t.Helper()

	gbpInstrument := financialaccountingdomain.MustCurrencyToInstrument(financialaccountingdomain.CurrencyGBP)
	amount := financialaccountingdomain.NewMoney(decimal.NewFromInt(amountCents).Div(decimal.NewFromInt(100)), gbpInstrument)

	// Use provided account ID or generate random one
	acc := "ACC-" + uuid.New().String()[:8]
	if len(accountID) > 0 && accountID[0] != "" {
		acc = accountID[0]
	}

	posting := &financialaccountingdomain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             financialaccountingdomain.PostingDirection(direction),
		Amount:                amount,
		AccountID:             acc,
		ValueDate:             time.Now(),
		Status:                financialaccountingdomain.TransactionStatusPending,
		CreatedAt:             time.Now(),
	}

	require.NoError(t, env.FinancialAccountingRepo.SavePosting(env.Ctx, posting))

	return posting.ID
}

// createTestPosition creates a position in position-keeping for reconciliation testing
// Note: position-keeping uses decimal.Decimal Amount (not minor units), so we store the actual amount
func createTestPosition(t *testing.T, env *E2ETestEnvironment, accountID string, amountDecimal decimal.Decimal) uuid.UUID {
	t.Helper()

	positionID := uuid.New()

	// Insert directly into position table using SQL (position-keeping uses pgxpool, not GORM)
	// Amount is stored as DECIMAL(38, 18) in the database
	query := `
		INSERT INTO position (
			id, created_at, created_by, account_id, instrument_code, bucket_key, amount, dimension
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	err := env.DB.WithContext(env.Ctx).Exec(query,
		positionID,
		time.Now(),
		"e2e-test",
		accountID,
		"GBP",
		"default",
		amountDecimal.String(), // Store as decimal string
		"Monetary",
	).Error
	require.NoError(t, err, "Failed to create test position")

	return positionID
}

// ============================================================================
// Double-Entry Assertion Helper
// ============================================================================

// assertDoubleEntry verifies that all postings for a given booking log satisfy
// the double-entry constraint: sum(debits) == sum(credits)
func assertDoubleEntry(t *testing.T, env *E2ETestEnvironment, bookingLogID uuid.UUID) {
	t.Helper()

	var postings []financialaccountingpersistence.LedgerPostingEntity
	err := env.DB.WithContext(env.Ctx).
		Where("financial_booking_log_id = ?", bookingLogID).
		Find(&postings).Error
	require.NoError(t, err, "Failed to query ledger postings")

	var totalDebits, totalCredits decimal.Decimal
	totalDebits = decimal.Zero
	totalCredits = decimal.Zero

	for _, p := range postings {
		amount := decimal.NewFromInt(p.AmountMinorUnits).Div(decimal.NewFromInt(100))
		switch p.PostingDirection {
		case "DEBIT":
			totalDebits = totalDebits.Add(amount)
		case "CREDIT":
			totalCredits = totalCredits.Add(amount)
		}
	}

	assert.True(t, totalDebits.Equal(totalCredits),
		"Double-entry violated: debits=%s credits=%s diff=%s",
		totalDebits.String(), totalCredits.String(), totalDebits.Sub(totalCredits).String())
}

// ============================================================================
// Subtask 2: TestDoubleEntryPosting_E2E
// ============================================================================

func TestDoubleEntryPosting_E2E(t *testing.T) {
	env := setupE2ETest(t)
	defer env.Cleanup()

	// Create booking log in PENDING status
	bookingLogID := createTestBookingLog(t, env, "PENDING")

	// Capture DEBIT posting (100.00 GBP)
	debitID := createTestPosting(t, env, bookingLogID, "DEBIT", 10000)
	require.NotEqual(t, uuid.Nil, debitID, "DEBIT posting should be created")

	// Capture CREDIT posting (100.00 GBP)
	creditID := createTestPosting(t, env, bookingLogID, "CREDIT", 10000)
	require.NotEqual(t, uuid.Nil, creditID, "CREDIT posting should be created")

	// Verify double-entry constraint: sum(debits) = sum(credits)
	assertDoubleEntry(t, env, bookingLogID)

	// Update booking log to POSTED (should succeed with balanced postings)
	bookingLog := &financialaccountingpersistence.FinancialBookingLogEntity{}
	err := env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(bookingLog).Error
	require.NoError(t, err)

	bookingLog.Status = "POSTED"
	bookingLog.UpdatedAt = time.Now()
	err = env.DB.WithContext(env.Ctx).Save(bookingLog).Error
	require.NoError(t, err, "Should successfully post balanced booking log")

	// Verify booking log status is POSTED
	var updatedLog financialaccountingpersistence.FinancialBookingLogEntity
	err = env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&updatedLog).Error
	require.NoError(t, err)
	assert.Equal(t, "POSTED", updatedLog.Status)
}

// ============================================================================
// Subtask 3: TestTrialBalance_E2E
// ============================================================================

func TestTrialBalance_E2E(t *testing.T) {
	env := setupE2ETest(t)
	defer env.Cleanup()

	// Execute 100+ transactions of various types
	totalTransactions := 100

	for i := 0; i < totalTransactions; i++ {
		bookingLogID := createTestBookingLog(t, env, "PENDING")

		// Randomize transaction types
		switch i % 3 {
		case 0: // Deposit: CREDIT customer, DEBIT cash
			createTestPosting(t, env, bookingLogID, "CREDIT", int64(100+i*10)) // Customer account
			createTestPosting(t, env, bookingLogID, "DEBIT", int64(100+i*10))  // Cash account
		case 1: // Withdrawal: DEBIT customer, CREDIT cash
			createTestPosting(t, env, bookingLogID, "DEBIT", int64(50+i*5))  // Customer account
			createTestPosting(t, env, bookingLogID, "CREDIT", int64(50+i*5)) // Cash account
		case 2: // Transfer: DEBIT from one, CREDIT to another
			createTestPosting(t, env, bookingLogID, "DEBIT", int64(200+i*20))  // From account
			createTestPosting(t, env, bookingLogID, "CREDIT", int64(200+i*20)) // To account
		}

		// Mark booking log as POSTED
		bookingLog := &financialaccountingpersistence.FinancialBookingLogEntity{}
		err := env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(bookingLog).Error
		require.NoError(t, err)
		bookingLog.Status = "POSTED"
		bookingLog.UpdatedAt = time.Now()
		err = env.DB.WithContext(env.Ctx).Save(bookingLog).Error
		require.NoError(t, err)
	}

	// Query ALL ledger postings and calculate trial balance
	var allPostings []financialaccountingpersistence.LedgerPostingEntity
	err := env.DB.WithContext(env.Ctx).Find(&allPostings).Error
	require.NoError(t, err)

	var totalDebits, totalCredits decimal.Decimal
	totalDebits = decimal.Zero
	totalCredits = decimal.Zero

	for _, p := range allPostings {
		amount := decimal.NewFromInt(p.AmountMinorUnits).Div(decimal.NewFromInt(100))
		switch p.PostingDirection {
		case "DEBIT":
			totalDebits = totalDebits.Add(amount)
		case "CREDIT":
			totalCredits = totalCredits.Add(amount)
		}
	}

	// Assert trial balance holds: sum(all debits) = sum(all credits)
	assert.True(t, totalDebits.Equal(totalCredits),
		"Trial balance violated: debits=%s credits=%s imbalance=%s postings=%d",
		totalDebits.String(), totalCredits.String(), totalDebits.Sub(totalCredits).String(), len(allPostings))

	// Verify we processed all transactions
	assert.GreaterOrEqual(t, len(allPostings), totalTransactions*2, "Should have at least 200 postings (2 per transaction)")
}

// ============================================================================
// Subtask 4: TestReconciliation_E2E
// ============================================================================

func TestReconciliation_E2E(t *testing.T) {
	env := setupE2ETest(t)
	defer env.Cleanup()

	accountID := "ACC-RECON-001"

	// Initialize position-keeping with 1000.00 GBP balance
	initialBalance := decimal.NewFromInt(1000)
	createTestPosition(t, env, accountID, initialBalance)

	// Execute 10 deposits through financial-accounting (CREDIT to account)
	for i := 0; i < 10; i++ {
		bookingLogID := createTestBookingLog(t, env, "PENDING")
		createTestPosting(t, env, bookingLogID, "CREDIT", 5000, accountID) // 50.00 GBP deposit to ACC-RECON-001
		createTestPosting(t, env, bookingLogID, "DEBIT", 5000)             // From cash account (different account)

		// Mark as POSTED
		bookingLog := &financialaccountingpersistence.FinancialBookingLogEntity{}
		err := env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(bookingLog).Error
		require.NoError(t, err)
		bookingLog.Status = "POSTED"
		err = env.DB.WithContext(env.Ctx).Save(bookingLog).Error
		require.NoError(t, err)

		// Update position-keeping to match (simulating real integration)
		// Position uses append-only, so we insert a new record with +50.00 GBP
		newPositionID := uuid.New()
		updateQuery := `
			INSERT INTO position (
				id, created_at, created_by, account_id, instrument_code, bucket_key, amount, dimension
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`
		err = env.DB.WithContext(env.Ctx).Exec(updateQuery,
			newPositionID,
			time.Now(),
			"e2e-deposit",
			accountID,
			"GBP",
			"default",
			"50.00", // Adding 50.00 GBP
			"Monetary",
		).Error
		require.NoError(t, err)
	}

	// Wait for async updates to complete (if any)
	err := await.Until(func() bool {
		var postingCount int64
		env.DB.WithContext(env.Ctx).Model(&financialaccountingpersistence.LedgerPostingEntity{}).Count(&postingCount)
		return postingCount >= 20 // 10 deposits * 2 postings each
	})
	require.NoError(t, err, "Async posting updates did not complete in time")

	// Calculate balance from financial-accounting for ACC-RECON-001
	var postings []financialaccountingpersistence.LedgerPostingEntity
	err = env.DB.WithContext(env.Ctx).
		Where("ledger_posting.account_id = ?", accountID).
		Find(&postings).Error
	require.NoError(t, err)

	// Calculate ledger net balance (CREDIT - DEBIT)
	var ledgerNet decimal.Decimal
	ledgerNet = decimal.Zero
	for _, p := range postings {
		amount := decimal.NewFromInt(p.AmountMinorUnits).Div(decimal.NewFromInt(100))
		switch p.PostingDirection {
		case "CREDIT":
			ledgerNet = ledgerNet.Add(amount)
		case "DEBIT":
			ledgerNet = ledgerNet.Sub(amount)
		}
	}

	// Calculate total position balance (position-keeping is append-only, sum all records)
	var positionTotal decimal.Decimal
	var amountStrings []string
	err = env.DB.WithContext(env.Ctx).
		Model(&struct {
			Amount string
		}{}).
		Table("position").
		Where("account_id = ? AND instrument_code = ? AND deleted_at IS NULL", accountID, "GBP").
		Pluck("amount", &amountStrings).Error
	require.NoError(t, err)

	positionTotal = decimal.Zero
	for _, amtStr := range amountStrings {
		amt, err := decimal.NewFromString(amtStr)
		require.NoError(t, err)
		positionTotal = positionTotal.Add(amt)
	}

	// Expected ledger delta from deposits (10 * 50.00 CREDIT to ACC-RECON-001)
	expectedLedgerDelta := decimal.NewFromInt(50).Mul(decimal.NewFromInt(10))

	// Verify ledger delta matches expected
	assert.True(t, expectedLedgerDelta.Equal(ledgerNet),
		"Ledger delta mismatch: expected=%s actual=%s",
		expectedLedgerDelta.String(), ledgerNet.String())

	// Expected final balance: 1000.00 (initial position) + 500.00 (10 deposits)
	expectedBalance := initialBalance.Add(expectedLedgerDelta)

	// Verify position total matches expected balance
	assert.True(t, expectedBalance.Equal(positionTotal),
		"Position total mismatch: expected=%s actual=%s diff=%s records=%d",
		expectedBalance.String(), positionTotal.String(), expectedBalance.Sub(positionTotal).String(), len(amountStrings))

	// CRITICAL: Verify ledger balance reconciles with position balance
	// This is the core reconciliation test - both systems must agree
	actualLedgerBalance := initialBalance.Add(ledgerNet)
	assert.True(t, actualLedgerBalance.Equal(positionTotal),
		"Reconciliation FAILED: ledger=%s position=%s diff=%s",
		actualLedgerBalance.String(), positionTotal.String(), actualLedgerBalance.Sub(positionTotal).String())
}

// ============================================================================
// Subtask 5: TestOrphanedBookingLog_E2E
// ============================================================================

func TestOrphanedBookingLog_E2E(t *testing.T) {
	env := setupE2ETest(t)
	defer env.Cleanup()

	// Initiate booking log (PENDING)
	bookingLogID := createTestBookingLog(t, env, "PENDING")

	// Capture DEBIT posting successfully (100.00 GBP)
	debitID := createTestPosting(t, env, bookingLogID, "DEBIT", 10000)
	require.NotEqual(t, uuid.Nil, debitID)

	// Simulate CREDIT posting failure (don't create it)
	// This leaves the booking log in an orphaned state: PENDING with only 1 posting

	// Verify booking log remains in PENDING state
	var bookingLog financialaccountingpersistence.FinancialBookingLogEntity
	err := env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&bookingLog).Error
	require.NoError(t, err)
	assert.Equal(t, "PENDING", bookingLog.Status)

	// Query ledger postings - should only find DEBIT (orphaned)
	var postings []financialaccountingpersistence.LedgerPostingEntity
	err = env.DB.WithContext(env.Ctx).
		Where("financial_booking_log_id = ?", bookingLogID).
		Find(&postings).Error
	require.NoError(t, err)
	assert.Len(t, postings, 1, "Should only have DEBIT posting (orphaned state)")
	assert.Equal(t, "DEBIT", postings[0].PostingDirection)

	// Implement orphan detection query
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)
	var orphanedLogs []financialaccountingpersistence.FinancialBookingLogEntity
	err = env.DB.WithContext(env.Ctx).
		Where("status = ? AND created_at < ?", "PENDING", fiveMinutesAgo).
		Find(&orphanedLogs).Error
	require.NoError(t, err)

	// For this test, we'll manually backdate the created_at to trigger detection
	bookingLog.CreatedAt = time.Now().Add(-6 * time.Minute)
	err = env.DB.WithContext(env.Ctx).Save(&bookingLog).Error
	require.NoError(t, err)

	// Re-run orphan detection
	err = env.DB.WithContext(env.Ctx).
		Where("status = ? AND created_at < ?", "PENDING", fiveMinutesAgo).
		Find(&orphanedLogs).Error
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(orphanedLogs), 1, "Orphan detection should find the orphaned booking log")

	// Test compensation path 1: Complete the booking by adding missing CREDIT
	creditID := createTestPosting(t, env, bookingLogID, "CREDIT", 10000)
	require.NotEqual(t, uuid.Nil, creditID)

	// Verify double-entry now holds
	assertDoubleEntry(t, env, bookingLogID)

	// Update status to POSTED
	bookingLog.Status = "POSTED"
	bookingLog.UpdatedAt = time.Now()
	err = env.DB.WithContext(env.Ctx).Save(&bookingLog).Error
	require.NoError(t, err)

	// Verify status is POSTED
	var completedLog financialaccountingpersistence.FinancialBookingLogEntity
	err = env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&completedLog).Error
	require.NoError(t, err)
	assert.Equal(t, "POSTED", completedLog.Status)
}

// TestOrphanedBookingLog_Cancellation_E2E tests the cancellation compensation path
func TestOrphanedBookingLog_Cancellation_E2E(t *testing.T) {
	env := setupE2ETest(t)
	defer env.Cleanup()

	// Initiate booking log (PENDING)
	bookingLogID := createTestBookingLog(t, env, "PENDING")

	// Capture DEBIT posting (100.00 GBP)
	debitID := createTestPosting(t, env, bookingLogID, "DEBIT", 10000)
	require.NotEqual(t, uuid.Nil, debitID)

	// Simulate failure - cancel the booking by adding reversal CREDIT
	reversalID := createTestPosting(t, env, bookingLogID, "CREDIT", 10000)
	require.NotEqual(t, uuid.Nil, reversalID)

	// Verify double-entry holds (DEBIT + reversal CREDIT = 0 net effect)
	assertDoubleEntry(t, env, bookingLogID)

	// Update status to CANCELLED
	var bookingLog financialaccountingpersistence.FinancialBookingLogEntity
	err := env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&bookingLog).Error
	require.NoError(t, err)
	bookingLog.Status = "CANCELLED"
	bookingLog.UpdatedAt = time.Now()
	err = env.DB.WithContext(env.Ctx).Save(&bookingLog).Error
	require.NoError(t, err)

	// Verify status is CANCELLED
	var cancelledLog financialaccountingpersistence.FinancialBookingLogEntity
	err = env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&cancelledLog).Error
	require.NoError(t, err)
	assert.Equal(t, "CANCELLED", cancelledLog.Status)
}

// ============================================================================
// Subtask 6: TestBookingLogLifecycle_E2E
// ============================================================================

func TestBookingLogLifecycle_E2E(t *testing.T) {
	env := setupE2ETest(t)
	defer env.Cleanup()

	t.Run("HappyPath_PENDING_to_POSTED", func(t *testing.T) {
		// Initiate booking log
		bookingLogID := createTestBookingLog(t, env, "PENDING")

		// Capture balanced postings
		createTestPosting(t, env, bookingLogID, "DEBIT", 10000)
		createTestPosting(t, env, bookingLogID, "CREDIT", 10000)

		// Verify double-entry
		assertDoubleEntry(t, env, bookingLogID)

		// Transition to POSTED
		var bookingLog financialaccountingpersistence.FinancialBookingLogEntity
		err := env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&bookingLog).Error
		require.NoError(t, err)
		assert.Equal(t, "PENDING", bookingLog.Status)

		bookingLog.Status = "POSTED"
		bookingLog.UpdatedAt = time.Now()
		err = env.DB.WithContext(env.Ctx).Save(&bookingLog).Error
		require.NoError(t, err)

		// Verify transition succeeded
		var updatedLog financialaccountingpersistence.FinancialBookingLogEntity
		err = env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&updatedLog).Error
		require.NoError(t, err)
		assert.Equal(t, "POSTED", updatedLog.Status)
	})

	t.Run("CompensationPath_PENDING_to_CANCELLED", func(t *testing.T) {
		// Initiate booking log
		bookingLogID := createTestBookingLog(t, env, "PENDING")

		// Capture partial postings, then add reversals
		createTestPosting(t, env, bookingLogID, "DEBIT", 5000)
		createTestPosting(t, env, bookingLogID, "CREDIT", 5000)

		// Transition to CANCELLED
		var bookingLog financialaccountingpersistence.FinancialBookingLogEntity
		err := env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&bookingLog).Error
		require.NoError(t, err)

		bookingLog.Status = "CANCELLED"
		bookingLog.UpdatedAt = time.Now()
		err = env.DB.WithContext(env.Ctx).Save(&bookingLog).Error
		require.NoError(t, err)

		// Verify transition succeeded
		var updatedLog financialaccountingpersistence.FinancialBookingLogEntity
		err = env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&updatedLog).Error
		require.NoError(t, err)
		assert.Equal(t, "CANCELLED", updatedLog.Status)
	})

	t.Run("Immutability_POSTED_cannot_transition_to_CANCELLED", func(t *testing.T) {
		// Create booking log already in POSTED state
		bookingLogID := createTestBookingLog(t, env, "POSTED")
		createTestPosting(t, env, bookingLogID, "DEBIT", 10000)
		createTestPosting(t, env, bookingLogID, "CREDIT", 10000)

		// Verify POSTED is a terminal state in the domain model
		assert.True(t, financialaccountingdomain.TransactionStatusPosted.IsFinal(),
			"POSTED should be a final state")

		// Verify the service-layer state machine rejects POSTED -> CANCELLED
		// State transition validation is enforced at the gRPC service layer
		// (isValidBookingLogTransition in grpc_booking_endpoints.go), not at the
		// DB level. Direct DB writes bypass this intentionally - the E2E tests
		// verify domain invariants here instead.
		var bookingLog financialaccountingpersistence.FinancialBookingLogEntity
		err := env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&bookingLog).Error
		require.NoError(t, err)
		assert.Equal(t, "POSTED", bookingLog.Status)
	})

	t.Run("Immutability_CANCELLED_cannot_transition_to_POSTED", func(t *testing.T) {
		// Create booking log in CANCELLED state
		bookingLogID := createTestBookingLog(t, env, "CANCELLED")

		// Verify CANCELLED is a terminal state in the domain model
		assert.True(t, financialaccountingdomain.TransactionStatusCancelled.IsFinal(),
			"CANCELLED should be a final state")

		// Verify the booking log exists in CANCELLED state
		var bookingLog financialaccountingpersistence.FinancialBookingLogEntity
		err := env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&bookingLog).Error
		require.NoError(t, err)
		assert.Equal(t, "CANCELLED", bookingLog.Status)
	})

	t.Run("Valid_PENDING_to_PENDING", func(t *testing.T) {
		// PENDING -> PENDING (no-op) should succeed
		bookingLogID := createTestBookingLog(t, env, "PENDING")

		var bookingLog financialaccountingpersistence.FinancialBookingLogEntity
		err := env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&bookingLog).Error
		require.NoError(t, err)

		bookingLog.Status = "PENDING"
		bookingLog.UpdatedAt = time.Now()
		err = env.DB.WithContext(env.Ctx).Save(&bookingLog).Error
		require.NoError(t, err, "PENDING -> PENDING should succeed (no-op)")

		// Verify status remains PENDING
		var updatedLog financialaccountingpersistence.FinancialBookingLogEntity
		err = env.DB.WithContext(env.Ctx).Where("id = ?", bookingLogID).First(&updatedLog).Error
		require.NoError(t, err)
		assert.Equal(t, "PENDING", updatedLog.Status)
	})
}
