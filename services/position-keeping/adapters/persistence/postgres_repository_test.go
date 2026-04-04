package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	testAccountID = "GB33BUKB20201555555555"
)

// testContainer holds the test database container and connection pool
type testContainer struct {
	container *postgres.PostgresContainer
	pool      *pgxpool.Pool
	repo      *persistence.PostgresRepository
}

// setupTestContainer creates a PostgreSQL testcontainer with the schema loaded
func setupTestContainer(t *testing.T) *testContainer {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_position_keeping"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second)),
	)
	require.NoError(t, err)

	// Get connection string with search_path configured
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable", "search_path=position_keeping")
	require.NoError(t, err)

	// Create connection pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err)

	// Load schema
	loadSchema(t, pool)

	// Create repository
	repo := persistence.NewPostgresRepository(pool)

	return &testContainer{
		container: pgContainer,
		pool:      pool,
		repo:      repo,
	}
}

// cleanup closes the pool and terminates the container
func (tc *testContainer) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if tc.pool != nil {
		tc.pool.Close()
	}

	if tc.container != nil {
		require.NoError(t, tc.container.Terminate(ctx))
	}
}

// loadSchema loads the position_keeping schema into the test database
func loadSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Create schemas
	_, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS position_keeping`)
	require.NoError(t, err)

	// Create financial_position_log table (singular to match production migration)
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.financial_position_log (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			log_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			version bigint NOT NULL DEFAULT 1,
			current_status character varying(20) NOT NULL,
			previous_status character varying(20) NULL,
			status_updated_at timestamptz NOT NULL,
			status_reason text NOT NULL,
			failure_reason text NULL,
			reconciliation_status character varying(20) NOT NULL,
			opening_balance_amount decimal(38, 18) NOT NULL DEFAULT 0,
			opening_balance_currency character(3) NOT NULL DEFAULT 'GBP',
			opening_balance_recorded_at timestamptz NULL,
			account_service_domain character varying(20) NOT NULL DEFAULT '',
			PRIMARY KEY (id)
		)
	`)
	require.NoError(t, err)

	// Create indexes
	_, err = pool.Exec(ctx, `
		CREATE UNIQUE INDEX idx_position_keeping_financial_position_log_log_id
		ON position_keeping.financial_position_log (log_id)
	`)
	require.NoError(t, err)

	// Create transaction_log_entry table (singular to match production migration)
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.transaction_log_entry (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			entry_id uuid NOT NULL,
			financial_position_log_id uuid NOT NULL,
			transaction_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			amount_cents bigint NOT NULL,
			currency character(3) NOT NULL DEFAULT 'GBP',
			direction character varying(10) NOT NULL,
			timestamp timestamptz NOT NULL,
			description text NULL,
			reference character varying(100) NULL,
			source character varying(50) NOT NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_transaction_log_entry_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err)

	// Create transaction_lineage table (singular to match production migration)
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.transaction_lineage (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			financial_position_log_id uuid NOT NULL,
			transaction_id uuid NOT NULL,
			parent_transaction_id uuid NULL,
			child_transaction_ids jsonb NOT NULL DEFAULT '[]',
			related_transaction_ids jsonb NOT NULL DEFAULT '[]',
			transaction_type character varying(50) NOT NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_transaction_lineage_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err)

	// Create audit_trail_entry table (singular to match production migration)
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.audit_trail_entry (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			audit_id uuid NOT NULL,
			financial_position_log_id uuid NOT NULL,
			timestamp timestamptz NOT NULL,
			user_id character varying(100) NOT NULL,
			action character varying(100) NOT NULL,
			details text NULL,
			ip_address character varying(45) NULL,
			system_context jsonb NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_audit_trail_entry_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err)
}

// createTestLog creates a valid FinancialPositionLog for testing
func createTestLog(t *testing.T, accountID string) *domain.FinancialPositionLog {
	t.Helper()

	// Create money amount
	amount, err := domain.NewMoney(decimal.NewFromFloat(100.50), domain.CurrencyGBP)
	require.NoError(t, err)

	// Create transaction log entry
	entry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		amount,
		domain.PostingDirectionDebit,
		time.Now().UTC(),
		"Test transaction",
		"REF-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	// Create lineage
	lineage, err := domain.NewTransactionLineage(
		uuid.New(),
		"payment",
		nil,
		[]uuid.UUID{},
		[]uuid.UUID{},
	)
	require.NoError(t, err)

	// Create financial position log
	log, err := domain.NewFinancialPositionLog(accountID, entry, lineage)
	require.NoError(t, err)

	// Add audit entry
	auditEntry, err := domain.NewAuditTrailEntry(
		"test-user",
		"create",
		"Test log created",
		"127.0.0.1",
		map[string]string{"system": "test"},
	)
	require.NoError(t, err)

	err = log.AddAuditEntry(auditEntry)
	require.NoError(t, err)

	return log
}

func TestPostgresRepository_Create(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	log := createTestLog(t, testAccountID)

	// Test successful create
	err := tc.repo.Create(ctx, log)
	require.NoError(t, err)

	// Verify log was created
	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	assert.Equal(t, log.LogID, retrieved.LogID)
	assert.Equal(t, log.AccountID, retrieved.AccountID)
	assert.Equal(t, log.Version, retrieved.Version)
	assert.Equal(t, 1, len(retrieved.TransactionLogEntries))
	assert.NotNil(t, retrieved.TransactionLineage)
	assert.Equal(t, 1, len(retrieved.AuditTrail))

	// Test duplicate create (should fail with conflict)
	err = tc.repo.Create(ctx, log)
	assert.ErrorIs(t, err, domain.ErrConflict)

	// Test nil log
	err = tc.repo.Create(ctx, nil)
	assert.ErrorIs(t, err, persistence.ErrNilLog)
}

func TestPostgresRepository_CreateBatch(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create multiple logs
	logs := []*domain.FinancialPositionLog{
		createTestLog(t, "GB33BUKB20201555555555"),
		createTestLog(t, "GB33BUKB20201555555556"),
		createTestLog(t, "GB33BUKB20201555555557"),
	}

	// Test successful batch create
	err := tc.repo.CreateBatch(ctx, logs)
	require.NoError(t, err)

	// Verify all logs were created
	for _, log := range logs {
		retrieved, err := tc.repo.FindByID(ctx, log.LogID)
		require.NoError(t, err)
		assert.Equal(t, log.LogID, retrieved.LogID)
	}

	// Test empty batch (should succeed without error)
	err = tc.repo.CreateBatch(ctx, []*domain.FinancialPositionLog{})
	require.NoError(t, err)
}

func TestPostgresRepository_FindByID(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	log := createTestLog(t, testAccountID)

	// Create log
	err := tc.repo.Create(ctx, log)
	require.NoError(t, err)

	// Test successful find
	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	assert.Equal(t, log.LogID, retrieved.LogID)
	assert.Equal(t, log.AccountID, retrieved.AccountID)

	// Test not found
	_, err = tc.repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestPostgresRepository_FindByAccountID(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create multiple logs for same account
	log1 := createTestLog(t, testAccountID)
	log2 := createTestLog(t, testAccountID)
	log3 := createTestLog(t, "GB33BUKB20201555555556")

	err := tc.repo.Create(ctx, log1)
	require.NoError(t, err)
	err = tc.repo.Create(ctx, log2)
	require.NoError(t, err)
	err = tc.repo.Create(ctx, log3)
	require.NoError(t, err)

	// Test find by account ID
	logs, err := tc.repo.FindByAccountID(ctx, testAccountID)
	require.NoError(t, err)
	assert.Equal(t, 2, len(logs))

	// Test find for account with no logs
	logs, err = tc.repo.FindByAccountID(ctx, "GB33BUKB20201555555999")
	require.NoError(t, err)
	assert.Equal(t, 0, len(logs))
}

func TestPostgresRepository_Update(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	log := createTestLog(t, testAccountID)

	// Create log
	err := tc.repo.Create(ctx, log)
	require.NoError(t, err)

	// Update status
	err = log.MarkPosted("Posted to ledger", nil)
	require.NoError(t, err)

	// Test successful update
	err = tc.repo.Update(ctx, log)
	require.NoError(t, err)

	// Verify update
	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	assert.Equal(t, domain.TransactionStatusPosted, retrieved.StatusTracking.CurrentStatus)
	assert.Equal(t, int64(2), retrieved.Version)

	// Test optimistic lock failure (modify version)
	log.Version = 1 // Set to old version
	err = tc.repo.Update(ctx, log)
	assert.ErrorIs(t, err, domain.ErrOptimisticLock)

	// Test nil log
	err = tc.repo.Update(ctx, nil)
	assert.ErrorIs(t, err, persistence.ErrNilLog)
}

func TestPostgresRepository_List(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create multiple logs with different statuses
	log1 := createTestLog(t, testAccountID)
	log2 := createTestLog(t, testAccountID)
	err := log2.MarkPosted("Posted", nil)
	require.NoError(t, err)

	err = tc.repo.Create(ctx, log1)
	require.NoError(t, err)
	err = tc.repo.Create(ctx, log2)
	require.NoError(t, err)

	// Test list with no filter (all logs)
	filter := domain.PositionLogFilter{
		Limit:  10,
		Offset: 0,
	}
	logs, err := tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 2, len(logs))

	// Test filter by status
	statusPending := domain.TransactionStatusPending
	filter = domain.PositionLogFilter{
		Status: &statusPending,
		Limit:  10,
		Offset: 0,
	}
	logs, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 1, len(logs))
	assert.Equal(t, domain.TransactionStatusPending, logs[0].StatusTracking.CurrentStatus)

	// Test filter by account ID
	accountIDFilter := testAccountID
	filter = domain.PositionLogFilter{
		AccountID: &accountIDFilter,
		Limit:     10,
		Offset:    0,
	}
	logs, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 2, len(logs))

	// Test pagination
	filter = domain.PositionLogFilter{
		Limit:  1,
		Offset: 0,
	}
	logs, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 1, len(logs))

	// Test filter by multiple account IDs (account_ids)
	anotherAccountID := "GB33BUKB20201555555556"
	excludedAccountID := "GB33BUKB20201555555557"
	log3 := createTestLog(t, anotherAccountID)
	log4 := createTestLog(t, excludedAccountID)
	err = tc.repo.Create(ctx, log3)
	require.NoError(t, err)
	err = tc.repo.Create(ctx, log4)
	require.NoError(t, err)

	// 4 logs exist: 2 for testAccountID, 1 for anotherAccountID, 1 for excludedAccountID.
	// Filter to testAccountID + anotherAccountID should return 3, excluding the 4th.
	filter = domain.PositionLogFilter{
		AccountIDs: []string{testAccountID, anotherAccountID},
		Limit:      10,
		Offset:     0,
	}
	logs, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 3, len(logs))
	for _, l := range logs {
		assert.NotEqual(t, excludedAccountID, l.AccountID)
	}

	// AccountIDs with only one account
	filter = domain.PositionLogFilter{
		AccountIDs: []string{anotherAccountID},
		Limit:      10,
		Offset:     0,
	}
	logs, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 1, len(logs))
	assert.Equal(t, anotherAccountID, logs[0].AccountID)

	// Test invalid limit
	filter = domain.PositionLogFilter{
		Limit: 0,
	}
	_, err = tc.repo.List(ctx, filter)
	assert.ErrorIs(t, err, persistence.ErrInvalidLimit)
}

func TestPostgresRepository_FindPendingForReconciliation(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create logs with different statuses
	log1 := createTestLog(t, "GB33BUKB20201555555555")
	log2 := createTestLog(t, "GB33BUKB20201555555556")
	log3 := createTestLog(t, "GB33BUKB20201555555557")

	// Mark one as posted
	err := log3.MarkPosted("Posted", nil)
	require.NoError(t, err)

	err = tc.repo.Create(ctx, log1)
	require.NoError(t, err)
	err = tc.repo.Create(ctx, log2)
	require.NoError(t, err)
	err = tc.repo.Create(ctx, log3)
	require.NoError(t, err)

	// Test find pending (no limit)
	logs, err := tc.repo.FindPendingForReconciliation(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, len(logs))
	for _, log := range logs {
		assert.Equal(t, domain.TransactionStatusPending, log.StatusTracking.CurrentStatus)
		assert.Equal(t, domain.ReconciliationStatusUnreconciled, log.StatusTracking.ReconciliationStatus)
	}

	// Test find pending with limit
	logs, err = tc.repo.FindPendingForReconciliation(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, len(logs))
}

func TestPostgresRepository_ComplexAggregateHandling(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create a log with multiple entries
	log := createTestLog(t, testAccountID)

	// Add more entries
	amount2, err := domain.NewMoney(decimal.NewFromFloat(50.25), domain.CurrencyGBP)
	require.NoError(t, err)

	entry2, err := domain.NewTransactionLogEntry(
		uuid.New(),
		testAccountID,
		amount2,
		domain.PostingDirectionCredit,
		time.Now().UTC(),
		"Second transaction",
		"REF-002",
		domain.TransactionSourceAutomated,
	)
	require.NoError(t, err)

	err = log.AddEntry(entry2)
	require.NoError(t, err)

	// Create log
	err = tc.repo.Create(ctx, log)
	require.NoError(t, err)

	// Verify all entries persisted
	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	assert.Equal(t, 2, len(retrieved.TransactionLogEntries))
	assert.Equal(t, "Test transaction", retrieved.TransactionLogEntries[0].Description)
	assert.Equal(t, "Second transaction", retrieved.TransactionLogEntries[1].Description)
}

func TestPostgresRepository_OpeningBalancePersistence(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create a log with a non-zero opening balance using the migration constructor
	openingBalance, err := domain.NewMoney(decimal.NewFromFloat(1234.56), domain.CurrencyGBP)
	require.NoError(t, err)

	effectiveDate := time.Now().UTC().Add(-24 * time.Hour) // Yesterday
	log, err := domain.NewFinancialPositionLogWithOpeningBalance(
		testAccountID,
		openingBalance,
		effectiveDate,
		"MIGRATION-REF-001",
	)
	require.NoError(t, err)

	// Add an audit entry (required for create)
	auditEntry, err := domain.NewAuditTrailEntry(
		"test-user",
		"create",
		"Opening balance log created",
		"127.0.0.1",
		map[string]string{"system": "test"},
	)
	require.NoError(t, err)
	err = log.AddAuditEntry(auditEntry)
	require.NoError(t, err)

	// Persist the log
	err = tc.repo.Create(ctx, log)
	require.NoError(t, err)

	// Retrieve and verify opening balance fields round-trip correctly
	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)

	// Verify opening balance amount and currency
	assert.True(t, retrieved.OpeningBalance.Amount.Equal(decimal.NewFromFloat(1234.56)),
		"Expected opening balance amount 1234.56, got %v", retrieved.OpeningBalance.Amount)
	assert.Equal(t, string(domain.CurrencyGBP), retrieved.OpeningBalance.Instrument.Code,
		"Expected opening balance currency GBP")

	// Verify opening balance recorded at timestamp
	assert.True(t, retrieved.HasOpeningBalance(),
		"Expected HasOpeningBalance to be true")
	assert.False(t, retrieved.OpeningBalanceRecordedAt.IsZero(),
		"Expected OpeningBalanceRecordedAt to be set")

	// Verify the transaction entry is also persisted
	assert.Equal(t, 1, len(retrieved.TransactionLogEntries),
		"Expected 1 transaction entry for opening balance")
	entry := retrieved.TransactionLogEntries[0]
	assert.Equal(t, domain.TransactionSourceOpeningBalance, entry.Source,
		"Expected source OPENING_BALANCE")
	assert.Equal(t, "MIGRATION-REF-001", entry.Reference,
		"Expected migration reference in entry")
}

func TestPostgresRepository_ZeroOpeningBalanceRoundTrip(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create a log using the standard constructor (zero opening balance)
	log := createTestLog(t, "GB33BUKB20201555555557")

	// Persist the log
	err := tc.repo.Create(ctx, log)
	require.NoError(t, err)

	// Retrieve and verify zero opening balance round-trips correctly
	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)

	// Default constructor should result in zero opening balance
	assert.True(t, retrieved.OpeningBalance.IsZero(),
		"Expected opening balance to be zero for default constructor")
	assert.False(t, retrieved.HasOpeningBalance(),
		"Expected HasOpeningBalance to be false for default constructor")

	// Opening balance recorded at should be zero time
	assert.True(t, retrieved.OpeningBalanceRecordedAt.IsZero(),
		"Expected OpeningBalanceRecordedAt to be zero for default constructor")
}
