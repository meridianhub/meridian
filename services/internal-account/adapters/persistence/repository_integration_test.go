//go:build integration

// Package persistence provides integration tests for the internal account repository.
// These tests use testcontainers to run against a real PostgreSQL database.
package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testContainer holds the test database container, connection pool, and repository.
type testContainer struct {
	container *postgres.PostgresContainer
	pool      *pgxpool.Pool
	db        *gorm.DB
	repo      *Repository
}

// setupIntegrationTestContainer creates a PostgreSQL testcontainer with the schema loaded.
// This function:
//   - Creates an isolated PostgreSQL 16 container
//   - Waits for the database to be ready (up to 30s)
//   - Creates a GORM connection
//   - Loads the internal_account schema
//   - Creates a Repository instance
func setupIntegrationTestContainer(t *testing.T) *testContainer {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container with explicit wait strategy
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_internal_account"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(30*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Create pgx pool for direct queries
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to create connection pool")

	// Create GORM connection
	db, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "Failed to connect to database with GORM")

	// Load schema
	loadInternalAccountSchema(t, pool)

	// Create repository
	repo := NewRepository(db)

	return &testContainer{
		container: pgContainer,
		pool:      pool,
		db:        db,
		repo:      repo,
	}
}

// cleanup closes the pool and terminates the container.
func (tc *testContainer) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if tc.pool != nil {
		tc.pool.Close()
	}

	sqlDB, _ := tc.db.DB()
	if sqlDB != nil {
		_ = sqlDB.Close()
	}

	if tc.container != nil {
		require.NoError(t, tc.container.Terminate(ctx), "Failed to terminate container")
	}
}

const defaultTestTenantID = tenant.TenantID("test_tenant")

// loadInternalAccountSchema loads the complete internal_account schema in the tenant schema.
func loadInternalAccountSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Create tenant schema
	schemaName := defaultTestTenantID.SchemaName()
	_, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err, "Failed to create tenant schema")

	// Create internal_account table in tenant schema (matches migration 20260112000001_initial.sql + later migrations)
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.internal_account (`, pq.QuoteIdentifier(schemaName))+`
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			account_id character varying(100) NOT NULL,
			account_code character varying(50) NOT NULL,
			name character varying(255) NOT NULL,
			account_type character varying(20) NOT NULL,
			clearing_purpose character varying(32) NULL,
			org_party_id uuid NULL,
			product_type_code character varying(100) NULL,
			product_type_version integer NULL,
			instrument_code character varying(32) NOT NULL,
			dimension character varying(20) NOT NULL,
			status character varying(20) NOT NULL DEFAULT 'ACTIVE',
			counterparty_id character varying(50) NULL,
			counterparty_name character varying(255) NULL,
			counterparty_external_ref character varying(100) NULL,
			attributes jsonb NOT NULL DEFAULT '{}',
			version bigint NOT NULL DEFAULT 1,
			PRIMARY KEY (id),
			CONSTRAINT chk_account_type CHECK (account_type IN (
				'CLEARING', 'NOSTRO', 'VOSTRO', 'HOLDING',
				'SUSPENSE', 'REVENUE', 'EXPENSE', 'INVENTORY'
			)),
			CONSTRAINT chk_dimension CHECK (dimension IN (
				'CURRENCY', 'ENERGY', 'MASS', 'VOLUME', 'TIME',
				'COMPUTE', 'CARBON', 'DATA', 'COUNT'
			)),
			CONSTRAINT chk_status CHECK (status IN (
				'ACTIVE', 'SUSPENDED', 'CLOSED'
			))
		)
	`)
	require.NoError(t, err, "Failed to create internal_account table")

	// Create unique constraint on account_id
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_internal_account_account_id ON %s.internal_account (account_id)
	`, pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err, "Failed to create account_id unique index")

	// Create indexes for query optimization
	quotedSchema := pq.QuoteIdentifier(schemaName)
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx_internal_account_type ON %s.internal_account (account_type);
		CREATE INDEX IF NOT EXISTS idx_internal_account_instrument ON %s.internal_account (instrument_code);
		CREATE INDEX IF NOT EXISTS idx_internal_account_status ON %s.internal_account (status);
		CREATE INDEX IF NOT EXISTS idx_internal_account_code ON %s.internal_account (account_code);
		CREATE INDEX IF NOT EXISTS idx_internal_account_deleted_at ON %s.internal_account (deleted_at)
	`, quotedSchema, quotedSchema, quotedSchema, quotedSchema, quotedSchema))
	require.NoError(t, err, "Failed to create indexes")

	// Create status history table in tenant schema
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.internal_account_status_history (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			account_id character varying(100) NOT NULL,
			from_status character varying(20) NOT NULL,
			to_status character varying(20) NOT NULL,
			reason text NULL,
			changed_by character varying(100) NOT NULL,
			changed_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (id),
			CONSTRAINT chk_from_status CHECK (from_status IN ('ACTIVE', 'SUSPENDED', 'CLOSED')),
			CONSTRAINT chk_to_status CHECK (to_status IN ('ACTIVE', 'SUSPENDED', 'CLOSED')),
			CONSTRAINT fk_status_history_account FOREIGN KEY (account_id)
				REFERENCES %s.internal_account (account_id)
				ON UPDATE NO ACTION ON DELETE RESTRICT
		)
	`, quotedSchema, quotedSchema))
	require.NoError(t, err, "Failed to create status_history table")

	// Create status history indexes
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx_status_history_account_changed
			ON %s.internal_account_status_history (account_id, changed_at DESC);
		CREATE INDEX IF NOT EXISTS idx_status_history_changed_at
			ON %s.internal_account_status_history (changed_at)
	`, quotedSchema, quotedSchema))
	require.NoError(t, err, "Failed to create status history indexes")
}

// createTestContext creates a context with tenant and audit user information for tests.
func createTestContext() context.Context {
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, defaultTestTenantID)
	return context.WithValue(ctx, auth.UserIDContextKey, "test-user")
}

// createTestAccountIntegration creates a test account for integration tests.
func createTestAccountIntegration(t *testing.T, accountID, accountCode, name string, accountType domain.AccountType) domain.InternalAccount {
	t.Helper()
	account, err := domain.NewInternalAccount(
		accountID,
		accountCode,
		name,
		accountType,
		domain.ClearingPurposeUnspecified,
		"GBP",
		"CURRENCY",
	)
	require.NoError(t, err)
	return account
}

// TestIntegration_SaveNewAccount tests creating a new account and verifying persistence.
func TestIntegration_SaveNewAccount(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-001", "GBP_CLEARING", "GBP Clearing Account", domain.AccountTypeClearing)

	// Save new account
	err := tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Verify account was persisted
	retrieved, err := tc.repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	assert.Equal(t, account.AccountID(), retrieved.AccountID())
	assert.Equal(t, account.AccountCode(), retrieved.AccountCode())
	assert.Equal(t, account.Name(), retrieved.Name())
	assert.Equal(t, domain.AccountStatusActive, retrieved.Status())
	assert.Equal(t, int64(1), retrieved.Version())
}

// TestIntegration_FindByID tests retrieving an account by its UUID.
func TestIntegration_FindByID(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-002", "EUR_CLEARING", "EUR Clearing Account", domain.AccountTypeClearing)

	err := tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Find by UUID
	found, err := tc.repo.FindByID(ctx, account.ID())
	require.NoError(t, err)
	assert.Equal(t, account.AccountID(), found.AccountID())

	// Test not found
	_, err = tc.repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// TestIntegration_FindByCode tests retrieving an account by its unique code.
func TestIntegration_FindByCode(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-003", "USD_CLEARING", "USD Clearing Account", domain.AccountTypeClearing)

	err := tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Find by code
	found, err := tc.repo.FindByCode(ctx, "USD_CLEARING")
	require.NoError(t, err)
	assert.Equal(t, account.AccountID(), found.AccountID())

	// Test not found
	_, err = tc.repo.FindByCode(ctx, "NONEXISTENT")
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// TestIntegration_ListWithFilters tests querying accounts with type/instrument/status filters.
func TestIntegration_ListWithFilters(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()

	// Create accounts of different types
	clearing := createTestAccountIntegration(t, "IBA-INT-010", "GBP_CLR", "GBP Clearing", domain.AccountTypeClearing)
	holding := createTestAccountIntegration(t, "IBA-INT-011", "GBP_HOLD", "GBP Holding", domain.AccountTypeHolding)
	suspense := createTestAccountIntegration(t, "IBA-INT-012", "GBP_SUSP", "GBP Suspense", domain.AccountTypeSuspense)

	require.NoError(t, tc.repo.Save(ctx, clearing))
	require.NoError(t, tc.repo.Save(ctx, holding))
	require.NoError(t, tc.repo.Save(ctx, suspense))

	// Filter by account type
	clearingType := domain.AccountTypeClearing
	filter := domain.ListFilter{AccountType: &clearingType}
	results, err := tc.repo.List(ctx, filter)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "GBP_CLR", results[0].AccountCode())

	// Filter by instrument code
	instrumentCode := "GBP"
	filter = domain.ListFilter{InstrumentCode: &instrumentCode}
	results, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Filter by status
	activeStatus := domain.AccountStatusActive
	filter = domain.ListFilter{Status: &activeStatus}
	results, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Test pagination
	filter = domain.ListFilter{Limit: 2, Offset: 0}
	results, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	filter = domain.ListFilter{Limit: 2, Offset: 2}
	results, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

// TestIntegration_OptimisticLocking tests that concurrent updates trigger version conflicts.
func TestIntegration_OptimisticLocking(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-020", "LOCK_TEST", "Lock Test Account", domain.AccountTypeClearing)

	// Save initial
	err := tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Retrieve two copies
	retrieved1, err := tc.repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	retrieved2, err := tc.repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	// Modify and save first copy (increments version to 2)
	suspended1, err := retrieved1.Suspend("First update")
	require.NoError(t, err)
	err = tc.repo.Save(ctx, suspended1)
	require.NoError(t, err)

	// Try to modify and save second copy (still has version 1)
	// This should fail due to optimistic locking
	suspended2, err := retrieved2.Suspend("Concurrent update")
	require.NoError(t, err)
	err = tc.repo.Save(ctx, suspended2)
	assert.ErrorIs(t, err, ErrVersionConflict)
}

// TestIntegration_OptimisticLocking_Concurrent tests concurrent access with goroutines.
func TestIntegration_OptimisticLocking_Concurrent(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-021", "CONCURRENT_TEST", "Concurrent Test", domain.AccountTypeClearing)

	err := tc.repo.Save(ctx, account)
	require.NoError(t, err)

	const numGoroutines = 5
	var wg sync.WaitGroup
	var successCount, conflictCount int
	var mu sync.Mutex

	// Launch concurrent updates
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Read account
			acc, err := tc.repo.FindByID(ctx, account.ID())
			if err != nil {
				return
			}

			// Small delay to increase contention
			time.Sleep(10 * time.Millisecond)

			// Try to update
			renamed, err := acc.Rename(fmt.Sprintf("Updated by goroutine %d", idx))
			if err != nil {
				return
			}

			err = tc.repo.Save(ctx, renamed)

			mu.Lock()
			switch {
			case err == nil:
				successCount++
			case errors.Is(err, ErrVersionConflict):
				conflictCount++
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Exactly one should succeed, rest should fail with version conflict
	assert.Equal(t, 1, successCount, "Exactly one goroutine should succeed")
	assert.Equal(t, numGoroutines-1, conflictCount, "Rest should fail with version conflict")
}

// TestIntegration_UpdateAccount tests modifying account metadata and settings.
func TestIntegration_UpdateAccount(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-030", "UPDATE_TEST", "Update Test Account", domain.AccountTypeClearing)

	err := tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Retrieve and update
	retrieved, err := tc.repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	// Rename
	renamed, err := retrieved.Rename("Updated Account Name")
	require.NoError(t, err)
	err = tc.repo.Save(ctx, renamed)
	require.NoError(t, err)

	// Verify update
	updated, err := tc.repo.FindByID(ctx, account.ID())
	require.NoError(t, err)
	assert.Equal(t, "Updated Account Name", updated.Name())
	assert.Equal(t, int64(2), updated.Version())
}

// TestIntegration_StatusHistory tests that status_history audit table records transitions.
func TestIntegration_StatusHistory(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-040", "STATUS_HIST", "Status History Test", domain.AccountTypeClearing)

	// Save account
	err := tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Record status change
	err = tc.repo.RecordStatusChange(ctx, account.AccountID(), "ACTIVE", "SUSPENDED", "Test suspension")
	require.NoError(t, err)

	// Verify using direct query with schema-qualified table name
	schemaName := defaultTestTenantID.SchemaName()
	var count int64
	err = tc.db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM %s.internal_account_status_history WHERE account_id = ?`, pq.QuoteIdentifier(schemaName)), account.AccountID()).Scan(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Record another status change
	err = tc.repo.RecordStatusChange(ctx, account.AccountID(), "SUSPENDED", "ACTIVE", "Reactivation")
	require.NoError(t, err)

	err = tc.db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM %s.internal_account_status_history WHERE account_id = ?`, pq.QuoteIdentifier(schemaName)), account.AccountID()).Scan(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	// Verify order (most recent first) using raw query
	type historyRow struct {
		ToStatus string
	}
	var history []historyRow
	err = tc.db.Raw(fmt.Sprintf(`SELECT to_status FROM %s.internal_account_status_history WHERE account_id = ? ORDER BY changed_at DESC`, pq.QuoteIdentifier(schemaName)), account.AccountID()).Scan(&history).Error
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, "ACTIVE", history[0].ToStatus)
	assert.Equal(t, "SUSPENDED", history[1].ToStatus)
}

// TestIntegration_CounterpartyDetails tests persistence of counterparty details.
func TestIntegration_CounterpartyDetails(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()

	// Create a NOSTRO account
	nostro, err := domain.NewInternalAccount(
		"IBA-INT-050",
		"USD_NOSTRO_CITI",
		"USD NOSTRO at Citibank",
		domain.AccountTypeNostro,
		domain.ClearingPurposeUnspecified,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)

	// Add counterparty details
	counterparty, err := domain.NewCounterpartyDetails("CITI001", "Citibank NA", "12345678")
	require.NoError(t, err)
	nostro, err = nostro.UpdateCounterparty(counterparty)
	require.NoError(t, err)

	// Save
	err = tc.repo.Save(ctx, nostro)
	require.NoError(t, err)

	// Retrieve and verify counterparty details
	retrieved, err := tc.repo.FindByID(ctx, nostro.ID())
	require.NoError(t, err)

	require.NotNil(t, retrieved.Counterparty())
	assert.Equal(t, "CITI001", retrieved.Counterparty().CounterpartyID())
	assert.Equal(t, "Citibank NA", retrieved.Counterparty().CounterpartyName())
	assert.Equal(t, "12345678", retrieved.Counterparty().ExternalRef())
}

// TestIntegration_JSONBAttributes tests persistence and retrieval of JSONB attributes.
func TestIntegration_JSONBAttributes(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()

	// Create account with custom attributes using the builder
	account := domain.NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-INT-060").
		WithAccountCode("GBP_SPECIAL").
		WithName("GBP Special Account").
		WithAccountType(domain.AccountTypeClearing).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithStatus(domain.AccountStatusActive).
		WithAttributes(map[string]string{
			"cost_center": "CC001",
			"department":  "Treasury",
			"region":      "EMEA",
		}).
		WithVersion(1).
		Build()

	err := tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Retrieve and verify attributes
	retrieved, err := tc.repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	attrs := retrieved.Attributes()
	require.NotNil(t, attrs)
	assert.Equal(t, "CC001", attrs["cost_center"])
	assert.Equal(t, "Treasury", attrs["department"])
	assert.Equal(t, "EMEA", attrs["region"])
}

// TestIntegration_SoftDelete tests soft deletion of accounts.
func TestIntegration_SoftDelete(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-070", "DELETE_TEST", "Delete Test Account", domain.AccountTypeClearing)

	err := tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Delete
	err = tc.repo.Delete(ctx, account.ID())
	require.NoError(t, err)

	// Should not be found after delete
	_, err = tc.repo.FindByID(ctx, account.ID())
	assert.ErrorIs(t, err, ErrAccountNotFound)

	// Should not appear in list
	results, err := tc.repo.List(ctx, domain.ListFilter{})
	require.NoError(t, err)
	assert.Len(t, results, 0)

	// Verify deleted_at is set in database using raw query with schema-qualified table
	schemaName := defaultTestTenantID.SchemaName()
	var deletedAt *time.Time
	err = tc.db.Raw(fmt.Sprintf(`SELECT deleted_at FROM %s.internal_account WHERE id = ?`, pq.QuoteIdentifier(schemaName)), account.ID()).Scan(&deletedAt).Error
	require.NoError(t, err)
	assert.NotNil(t, deletedAt)
}

// TestIntegration_Delete_NotFound tests deleting a non-existent account.
func TestIntegration_Delete_NotFound(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()

	err := tc.repo.Delete(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// TestIntegration_ExistsByCode tests checking account existence by code.
func TestIntegration_ExistsByCode(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-080", "EXISTS_TEST", "Exists Test Account", domain.AccountTypeClearing)

	// Should not exist before save
	exists, err := tc.repo.ExistsByCode(ctx, "EXISTS_TEST")
	require.NoError(t, err)
	assert.False(t, exists)

	// Save
	err = tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Should exist after save
	exists, err = tc.repo.ExistsByCode(ctx, "EXISTS_TEST")
	require.NoError(t, err)
	assert.True(t, exists)
}

// TestIntegration_DuplicateCode tests that duplicate account codes are rejected.
func TestIntegration_DuplicateCode(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account1 := createTestAccountIntegration(t, "IBA-INT-090", "DUP_CODE", "First Account", domain.AccountTypeClearing)

	err := tc.repo.Save(ctx, account1)
	require.NoError(t, err)

	// Try to create another account with duplicate account_id
	account2 := createTestAccountIntegration(t, "IBA-INT-090", "DUP_CODE_2", "Second Account", domain.AccountTypeClearing)

	err = tc.repo.Save(ctx, account2)
	assert.Error(t, err) // Should fail due to unique constraint on account_id
}

// TestIntegration_Ping tests database connectivity check.
func TestIntegration_Ping(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	err := tc.repo.Ping()
	require.NoError(t, err)
}

// TestIntegration_FindByIDForUpdate tests pessimistic locking.
func TestIntegration_FindByIDForUpdate(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-100", "LOCK_FOR_UPDATE", "Lock For Update Test", domain.AccountTypeClearing)

	err := tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Test FindByIDForUpdate works
	found, err := tc.repo.FindByIDForUpdate(ctx, account.ID())
	require.NoError(t, err)
	assert.Equal(t, account.AccountID(), found.AccountID())

	// Test not found
	_, err = tc.repo.FindByIDForUpdate(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// TestIntegration_AwaitPattern demonstrates using await package instead of time.Sleep.
func TestIntegration_AwaitPattern(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	account := createTestAccountIntegration(t, "IBA-INT-110", "AWAIT_TEST", "Await Test Account", domain.AccountTypeClearing)

	// Save account in goroutine with small delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = tc.repo.Save(ctx, account)
	}()

	// Use await to poll for account existence instead of time.Sleep
	err := await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		UntilNoError(func() error {
			_, findErr := tc.repo.FindByID(ctx, account.ID())
			return findErr
		})

	require.NoError(t, err, "Account should be found after async save")

	// Verify account exists
	found, err := tc.repo.FindByID(ctx, account.ID())
	require.NoError(t, err)
	assert.Equal(t, account.AccountID(), found.AccountID())
}

// ============================================================================
// Multi-Tenant Isolation Tests
// ============================================================================

// setupMultiTenantContainer creates a PostgreSQL testcontainer with multiple tenant schemas.
func setupMultiTenantContainer(t *testing.T, tenants ...tenant.TenantID) *testContainer {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_multitenant"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(30*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Create pgx pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to create connection pool")

	// Create GORM connection
	db, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "Failed to connect to database with GORM")

	// Create schema for each tenant
	for _, tenantID := range tenants {
		schemaName := tenantID.SchemaName()

		_, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err, "Failed to create schema for tenant %s", tenantID)

		// Create internal_account table in tenant schema
		_, err = pool.Exec(ctx, fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.internal_account (
				id uuid NOT NULL DEFAULT gen_random_uuid(),
				created_at timestamptz NOT NULL DEFAULT now(),
				created_by character varying(100) NOT NULL,
				updated_at timestamptz NOT NULL DEFAULT now(),
				updated_by character varying(100) NOT NULL,
				deleted_at timestamptz NULL,
				account_id character varying(100) NOT NULL,
				account_code character varying(50) NOT NULL,
				name character varying(255) NOT NULL,
				account_type character varying(20) NOT NULL,
				product_type_code character varying(100) NULL,
				product_type_version integer NULL,
				instrument_code character varying(32) NOT NULL,
				dimension character varying(20) NOT NULL,
				status character varying(20) NOT NULL DEFAULT 'ACTIVE',
				counterparty_id character varying(50) NULL,
				counterparty_name character varying(255) NULL,
				counterparty_external_ref character varying(100) NULL,
				attributes jsonb NOT NULL DEFAULT '{}',
				version bigint NOT NULL DEFAULT 1,
				PRIMARY KEY (id)
			)
		`, pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err, "Failed to create table for tenant %s", tenantID)

		// Create unique index on account_id
		qs := pq.QuoteIdentifier(schemaName)
		_, err = pool.Exec(ctx, fmt.Sprintf(
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_%s_account_id ON %s.internal_account (account_id)`,
			string(tenantID), qs))
		require.NoError(t, err, "Failed to create account_id index for tenant %s", tenantID)

		// Create status_history table
		_, err = pool.Exec(ctx, fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.internal_account_status_history (
				id uuid NOT NULL DEFAULT gen_random_uuid(),
				account_id character varying(100) NOT NULL,
				from_status character varying(20) NOT NULL,
				to_status character varying(20) NOT NULL,
				reason text NULL,
				changed_by character varying(100) NOT NULL,
				changed_at timestamptz NOT NULL DEFAULT now(),
				PRIMARY KEY (id),
				CONSTRAINT fk_status_history_account_%s FOREIGN KEY (account_id)
					REFERENCES %s.internal_account (account_id)
					ON UPDATE NO ACTION ON DELETE RESTRICT
			)
		`, qs, string(tenantID), qs))
		require.NoError(t, err, "Failed to create status_history table for tenant %s", tenantID)
	}

	repo := NewRepository(db)

	return &testContainer{
		container: pgContainer,
		pool:      pool,
		db:        db,
		repo:      repo,
	}
}

// createTenantContext creates a context with tenant and audit user information.
func createTenantContext(tenantID tenant.TenantID) context.Context {
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, "test-user")
	return ctx
}

// TestIntegration_MultiTenant_Isolation tests that tenant A cannot see tenant B's data.
func TestIntegration_MultiTenant_Isolation(t *testing.T) {
	tenantA := tenant.TenantID("tenant_a")
	tenantB := tenant.TenantID("tenant_b")

	tc := setupMultiTenantContainer(t, tenantA, tenantB)
	defer tc.cleanup(t)

	// Create context for each tenant
	ctxTenantA := createTenantContext(tenantA)
	ctxTenantB := createTenantContext(tenantB)

	// Create account for tenant A
	accountA := createTestAccountIntegration(t, "IBA-TENANT-A", "TENANT_A_ACC", "Tenant A Account", domain.AccountTypeClearing)
	err := tc.repo.Save(ctxTenantA, accountA)
	require.NoError(t, err)

	// Create account for tenant B
	accountB := createTestAccountIntegration(t, "IBA-TENANT-B", "TENANT_B_ACC", "Tenant B Account", domain.AccountTypeClearing)
	err = tc.repo.Save(ctxTenantB, accountB)
	require.NoError(t, err)

	// Tenant A should not see tenant B's account
	_, err = tc.repo.FindByID(ctxTenantA, accountB.ID())
	assert.ErrorIs(t, err, ErrAccountNotFound, "Tenant A should not see tenant B's account by ID")

	_, err = tc.repo.FindByCode(ctxTenantA, "TENANT_B_ACC")
	assert.ErrorIs(t, err, ErrAccountNotFound, "Tenant A should not see tenant B's account by code")

	// Tenant B should not see tenant A's account
	_, err = tc.repo.FindByID(ctxTenantB, accountA.ID())
	assert.ErrorIs(t, err, ErrAccountNotFound, "Tenant B should not see tenant A's account by ID")

	_, err = tc.repo.FindByCode(ctxTenantB, "TENANT_A_ACC")
	assert.ErrorIs(t, err, ErrAccountNotFound, "Tenant B should not see tenant A's account by code")
}

// TestIntegration_MultiTenant_ListIsolation tests that List() only returns current tenant's data.
func TestIntegration_MultiTenant_ListIsolation(t *testing.T) {
	tenantA := tenant.TenantID("tenant_a")
	tenantB := tenant.TenantID("tenant_b")

	tc := setupMultiTenantContainer(t, tenantA, tenantB)
	defer tc.cleanup(t)

	ctxTenantA := createTenantContext(tenantA)
	ctxTenantB := createTenantContext(tenantB)

	// Create 3 accounts for tenant A
	for i := 1; i <= 3; i++ {
		account := createTestAccountIntegration(t,
			fmt.Sprintf("IBA-A-%d", i),
			fmt.Sprintf("A_ACC_%d", i),
			fmt.Sprintf("Tenant A Account %d", i),
			domain.AccountTypeClearing)
		require.NoError(t, tc.repo.Save(ctxTenantA, account))
	}

	// Create 2 accounts for tenant B
	for i := 1; i <= 2; i++ {
		account := createTestAccountIntegration(t,
			fmt.Sprintf("IBA-B-%d", i),
			fmt.Sprintf("B_ACC_%d", i),
			fmt.Sprintf("Tenant B Account %d", i),
			domain.AccountTypeClearing)
		require.NoError(t, tc.repo.Save(ctxTenantB, account))
	}

	// Tenant A should only see their 3 accounts
	accountsA, err := tc.repo.List(ctxTenantA, domain.ListFilter{})
	require.NoError(t, err)
	assert.Len(t, accountsA, 3)
	for _, acc := range accountsA {
		assert.Contains(t, acc.AccountCode(), "A_ACC_")
	}

	// Tenant B should only see their 2 accounts
	accountsB, err := tc.repo.List(ctxTenantB, domain.ListFilter{})
	require.NoError(t, err)
	assert.Len(t, accountsB, 2)
	for _, acc := range accountsB {
		assert.Contains(t, acc.AccountCode(), "B_ACC_")
	}
}

// TestIntegration_MultiTenant_UpdateIsolation tests that tenants cannot update each other's accounts.
func TestIntegration_MultiTenant_UpdateIsolation(t *testing.T) {
	tenantA := tenant.TenantID("tenant_a")
	tenantB := tenant.TenantID("tenant_b")

	tc := setupMultiTenantContainer(t, tenantA, tenantB)
	defer tc.cleanup(t)

	ctxTenantA := createTenantContext(tenantA)
	ctxTenantB := createTenantContext(tenantB)

	// Create account for tenant A
	accountA := createTestAccountIntegration(t, "IBA-UPDATE-A", "UPDATE_A_ACC", "Tenant A Account", domain.AccountTypeClearing)
	err := tc.repo.Save(ctxTenantA, accountA)
	require.NoError(t, err)

	// Try to retrieve from tenant B context (simulating attempt to modify)
	// First verify we can't even find it
	_, err = tc.repo.FindByID(ctxTenantB, accountA.ID())
	assert.ErrorIs(t, err, ErrAccountNotFound)

	// Even if tenant B has the account object, saving with their context should create a new account
	// in tenant B's schema, not modify tenant A's account
	err = tc.repo.Save(ctxTenantB, accountA)
	require.NoError(t, err)

	// Verify tenant A's account is unchanged
	retrievedA, err := tc.repo.FindByID(ctxTenantA, accountA.ID())
	require.NoError(t, err)
	assert.Equal(t, "Tenant A Account", retrievedA.Name())

	// Tenant B now has a copy in their schema
	retrievedB, err := tc.repo.FindByID(ctxTenantB, accountA.ID())
	require.NoError(t, err)
	assert.Equal(t, "Tenant A Account", retrievedB.Name())

	// Both accounts exist independently
	listA, _ := tc.repo.List(ctxTenantA, domain.ListFilter{})
	listB, _ := tc.repo.List(ctxTenantB, domain.ListFilter{})
	assert.Len(t, listA, 1)
	assert.Len(t, listB, 1)
}

// TestIntegration_MultiTenant_DeleteIsolation tests that tenants cannot delete each other's accounts.
func TestIntegration_MultiTenant_DeleteIsolation(t *testing.T) {
	tenantA := tenant.TenantID("tenant_a")
	tenantB := tenant.TenantID("tenant_b")

	tc := setupMultiTenantContainer(t, tenantA, tenantB)
	defer tc.cleanup(t)

	ctxTenantA := createTenantContext(tenantA)
	ctxTenantB := createTenantContext(tenantB)

	// Create account for tenant A
	accountA := createTestAccountIntegration(t, "IBA-DELETE-A", "DELETE_A_ACC", "Tenant A Account", domain.AccountTypeClearing)
	err := tc.repo.Save(ctxTenantA, accountA)
	require.NoError(t, err)

	// Tenant B tries to delete tenant A's account - should fail (not found in their schema)
	err = tc.repo.Delete(ctxTenantB, accountA.ID())
	assert.ErrorIs(t, err, ErrAccountNotFound)

	// Verify tenant A's account still exists
	retrievedA, err := tc.repo.FindByID(ctxTenantA, accountA.ID())
	require.NoError(t, err)
	assert.Equal(t, "Tenant A Account", retrievedA.Name())
}

// TestIntegration_MultiTenant_ExistsByCodeIsolation tests ExistsByCode tenant isolation.
func TestIntegration_MultiTenant_ExistsByCodeIsolation(t *testing.T) {
	tenantA := tenant.TenantID("tenant_a")
	tenantB := tenant.TenantID("tenant_b")

	tc := setupMultiTenantContainer(t, tenantA, tenantB)
	defer tc.cleanup(t)

	ctxTenantA := createTenantContext(tenantA)
	ctxTenantB := createTenantContext(tenantB)

	// Create account for tenant A with specific code
	accountA := createTestAccountIntegration(t, "IBA-EXISTS-A", "SHARED_CODE", "Tenant A Account", domain.AccountTypeClearing)
	err := tc.repo.Save(ctxTenantA, accountA)
	require.NoError(t, err)

	// Tenant A should see the code exists
	exists, err := tc.repo.ExistsByCode(ctxTenantA, "SHARED_CODE")
	require.NoError(t, err)
	assert.True(t, exists)

	// Tenant B should not see the code exists
	exists, err = tc.repo.ExistsByCode(ctxTenantB, "SHARED_CODE")
	require.NoError(t, err)
	assert.False(t, exists)

	// Tenant B can use the same code
	accountB := createTestAccountIntegration(t, "IBA-EXISTS-B", "SHARED_CODE", "Tenant B Account", domain.AccountTypeHolding)
	err = tc.repo.Save(ctxTenantB, accountB)
	require.NoError(t, err)

	// Both tenants now have accounts with the same code but different schemas
	codeExistsA, _ := tc.repo.ExistsByCode(ctxTenantA, "SHARED_CODE")
	codeExistsB, _ := tc.repo.ExistsByCode(ctxTenantB, "SHARED_CODE")
	assert.True(t, codeExistsA)
	assert.True(t, codeExistsB)
}

// TestIntegration_MultiTenant_StatusHistoryIsolation tests status history tenant isolation.
func TestIntegration_MultiTenant_StatusHistoryIsolation(t *testing.T) {
	tenantA := tenant.TenantID("tenant_a")
	tenantB := tenant.TenantID("tenant_b")

	tc := setupMultiTenantContainer(t, tenantA, tenantB)
	defer tc.cleanup(t)

	ctxTenantA := createTenantContext(tenantA)
	ctxTenantB := createTenantContext(tenantB)

	// Create account for tenant A
	accountA := createTestAccountIntegration(t, "IBA-HIST-A", "HIST_A_ACC", "Tenant A Account", domain.AccountTypeClearing)
	err := tc.repo.Save(ctxTenantA, accountA)
	require.NoError(t, err)

	// Record status change for tenant A
	err = tc.repo.RecordStatusChange(ctxTenantA, "IBA-HIST-A", "ACTIVE", "SUSPENDED", "Test suspension")
	require.NoError(t, err)

	// Tenant B should not see tenant A's status history
	// We can verify this by querying the status_history table with tenant B's context
	// The history record should only exist in tenant A's schema

	// Create account for tenant B with same account_id (different schema)
	accountB := createTestAccountIntegration(t, "IBA-HIST-A", "HIST_B_ACC", "Tenant B Account", domain.AccountTypeClearing)
	err = tc.repo.Save(ctxTenantB, accountB)
	require.NoError(t, err)

	// Record status change for tenant B
	err = tc.repo.RecordStatusChange(ctxTenantB, "IBA-HIST-A", "ACTIVE", "CLOSED", "Closing account")
	require.NoError(t, err)

	// Query status history directly to verify isolation
	// Each tenant's status_history should only contain their own records
	var countA int64
	err = tc.db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM %s.internal_account_status_history WHERE account_id = ?`, pq.QuoteIdentifier(tenantA.SchemaName())), "IBA-HIST-A").Scan(&countA).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), countA, "Tenant A should have 1 status history record")

	var countB int64
	err = tc.db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM %s.internal_account_status_history WHERE account_id = ?`, pq.QuoteIdentifier(tenantB.SchemaName())), "IBA-HIST-A").Scan(&countB).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), countB, "Tenant B should have 1 status history record")
}

// TestIntegration_MultiTenant_MissingContext tests that operations fail without tenant context.
func TestIntegration_MultiTenant_MissingContext(t *testing.T) {
	tenantA := tenant.TenantID("tenant_a")

	tc := setupMultiTenantContainer(t, tenantA)
	defer tc.cleanup(t)

	// Create context without tenant (only audit user, no tenant)
	ctxNoTenant := context.WithValue(context.Background(), auth.UserIDContextKey, "test-user")

	// Create an account
	account := createTestAccountIntegration(t, "IBA-NO-TENANT", "NO_TENANT_ACC", "No Tenant Account", domain.AccountTypeClearing)

	// All operations should fail without tenant context
	err := tc.repo.Save(ctxNoTenant, account)
	assert.Error(t, err, "Save should fail without tenant context")
	assert.Contains(t, err.Error(), "tenant")

	_, err = tc.repo.FindByID(ctxNoTenant, account.ID())
	assert.Error(t, err, "FindByID should fail without tenant context")

	_, err = tc.repo.FindByCode(ctxNoTenant, "NO_TENANT_ACC")
	assert.Error(t, err, "FindByCode should fail without tenant context")

	_, err = tc.repo.List(ctxNoTenant, domain.ListFilter{})
	assert.Error(t, err, "List should fail without tenant context")

	_, err = tc.repo.ExistsByCode(ctxNoTenant, "NO_TENANT_ACC")
	assert.Error(t, err, "ExistsByCode should fail without tenant context")

	err = tc.repo.Delete(ctxNoTenant, account.ID())
	assert.Error(t, err, "Delete should fail without tenant context")
}

// ============================================================================
// Org-Scoped Account Tests
// ============================================================================

// TestIntegration_SaveOrgScopedAccount tests persisting an org-scoped account.
func TestIntegration_SaveOrgScopedAccount(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	orgID := uuid.New()

	account, err := domain.NewInternalAccount(
		"IBA-ORG-INT-001",
		"ORG_HOLDING_001",
		"Org Scoped Holding",
		domain.AccountTypeHolding,
		domain.ClearingPurposeUnspecified,
		"GBP",
		"CURRENCY",
		domain.WithOrgPartyID(orgID),
	)
	require.NoError(t, err)

	// Save
	err = tc.repo.Save(ctx, account)
	require.NoError(t, err)

	// Retrieve and verify org_party_id persisted
	retrieved, err := tc.repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	require.NotNil(t, retrieved.OrgPartyID())
	assert.Equal(t, orgID, *retrieved.OrgPartyID())
	assert.True(t, retrieved.IsScopedToOrganization())
}

// TestIntegration_SaveGlobalAccount_NilOrgPartyID tests that global accounts persist with null org_party_id.
func TestIntegration_SaveGlobalAccount_NilOrgPartyID(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()

	account, err := domain.NewInternalAccount(
		"IBA-GLOBAL-INT-001",
		"GLOBAL_HOLDING_001",
		"Global Holding",
		domain.AccountTypeHolding,
		domain.ClearingPurposeUnspecified,
		"GBP",
		"CURRENCY",
	)
	require.NoError(t, err)

	err = tc.repo.Save(ctx, account)
	require.NoError(t, err)

	retrieved, err := tc.repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	assert.Nil(t, retrieved.OrgPartyID())
	assert.False(t, retrieved.IsScopedToOrganization())
}

// TestIntegration_FindByOrganization tests finding accounts by org party ID.
func TestIntegration_FindByOrganization(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	orgA := uuid.New()
	orgB := uuid.New()

	// Create org-scoped accounts for org A
	accountA1, err := domain.NewInternalAccount(
		"IBA-ORGA-001",
		"ORGA_HOLDING_001",
		"Org A Holding 1",
		domain.AccountTypeHolding,
		domain.ClearingPurposeUnspecified,
		"GBP",
		"CURRENCY",
		domain.WithOrgPartyID(orgA),
	)
	require.NoError(t, err)
	require.NoError(t, tc.repo.Save(ctx, accountA1))

	accountA2, err := domain.NewInternalAccount(
		"IBA-ORGA-002",
		"ORGA_SUSPENSE_001",
		"Org A Suspense",
		domain.AccountTypeSuspense,
		domain.ClearingPurposeUnspecified,
		"GBP",
		"CURRENCY",
		domain.WithOrgPartyID(orgA),
	)
	require.NoError(t, err)
	require.NoError(t, tc.repo.Save(ctx, accountA2))

	// Create org-scoped account for org B
	accountB1, err := domain.NewInternalAccount(
		"IBA-ORGB-001",
		"ORGB_HOLDING_001",
		"Org B Holding",
		domain.AccountTypeHolding,
		domain.ClearingPurposeUnspecified,
		"USD",
		"CURRENCY",
		domain.WithOrgPartyID(orgB),
	)
	require.NoError(t, err)
	require.NoError(t, tc.repo.Save(ctx, accountB1))

	// Create global account (no org) - use HOLDING since createTestAccountIntegration
	// uses ClearingPurposeUnspecified which is invalid for CLEARING accounts
	globalAccount := createTestAccountIntegration(t, "IBA-GLOBAL-002", "GLOBAL_HOLD_002", "Global Holding", domain.AccountTypeHolding)
	require.NoError(t, tc.repo.Save(ctx, globalAccount))

	// FindByOrganization for org A
	orgAAccounts, err := tc.repo.FindByOrganization(ctx, orgA)
	require.NoError(t, err)
	assert.Len(t, orgAAccounts, 2)

	// FindByOrganization for org B
	orgBAccounts, err := tc.repo.FindByOrganization(ctx, orgB)
	require.NoError(t, err)
	assert.Len(t, orgBAccounts, 1)
	assert.Equal(t, "ORGB_HOLDING_001", orgBAccounts[0].AccountCode())

	// FindByOrganization for non-existent org
	nonExistentOrg := uuid.New()
	noAccounts, err := tc.repo.FindByOrganization(ctx, nonExistentOrg)
	require.NoError(t, err)
	assert.Empty(t, noAccounts)
}

// TestIntegration_ListWithOrgPartyIDFilter tests the List method with OrgPartyID filter.
func TestIntegration_ListWithOrgPartyIDFilter(t *testing.T) {
	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	ctx := createTestContext()
	orgID := uuid.New()

	// Create org-scoped account
	orgAccount, err := domain.NewInternalAccount(
		"IBA-FILTER-ORG-001",
		"FILTER_ORG_HOLD",
		"Org Holding",
		domain.AccountTypeHolding,
		domain.ClearingPurposeUnspecified,
		"GBP",
		"CURRENCY",
		domain.WithOrgPartyID(orgID),
	)
	require.NoError(t, err)
	require.NoError(t, tc.repo.Save(ctx, orgAccount))

	// Create global account - use HOLDING since createTestAccountIntegration
	// uses ClearingPurposeUnspecified which is invalid for CLEARING accounts
	globalAccount := createTestAccountIntegration(t, "IBA-FILTER-GLOBAL-001", "FILTER_GLOBAL_HOLD", "Global Holding", domain.AccountTypeHolding)
	require.NoError(t, tc.repo.Save(ctx, globalAccount))

	// List with OrgPartyID filter should return only org-scoped accounts
	results, err := tc.repo.List(ctx, domain.ListFilter{OrgPartyID: &orgID})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "FILTER_ORG_HOLD", results[0].AccountCode())

	// List without filter returns all
	allResults, err := tc.repo.List(ctx, domain.ListFilter{})
	require.NoError(t, err)
	assert.Len(t, allResults, 2)
}

// ============================================================================
// Performance Benchmarks
// ============================================================================
//
// These benchmarks test repository layer performance against a real PostgreSQL
// database using testcontainers.
//
// Performance targets:
//   - Account creation p99: <50ms
//   - Account lookup (by ID) p99: <5ms
//   - Account lookup (by code) p99: <5ms
//   - Concurrent creation: 1000 accounts in <30s
//
// Run with: go test -tags=integration -bench=. -benchmem ./services/internal-account/adapters/persistence/...

// benchTestContainer holds the benchmark test database container.
type benchTestContainer struct {
	container *postgres.PostgresContainer
	pool      *pgxpool.Pool
	db        *gorm.DB
	repo      *Repository
}

// setupBenchContainer creates a PostgreSQL testcontainer for benchmarks.
func setupBenchContainer(b *testing.B) *benchTestContainer {
	b.Helper()

	ctx := context.Background()

	// Create PostgreSQL container with explicit wait strategy
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("bench_internal_account"),
		postgres.WithUsername("bench"),
		postgres.WithPassword("bench"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(60*time.Second)),
	)
	if err != nil {
		b.Fatalf("Failed to start PostgreSQL container: %v", err)
	}

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		b.Fatalf("Failed to get connection string: %v", err)
	}

	// Create pgx pool for direct queries
	poolConfig, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		b.Fatalf("Failed to parse pool config: %v", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		b.Fatalf("Failed to create connection pool: %v", err)
	}

	// Create GORM connection
	db, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		b.Fatalf("Failed to connect to database with GORM: %v", err)
	}

	// Load schema using pgx pool
	loadBenchSchema(b, pool)

	// Create repository
	repo := NewRepository(db)

	return &benchTestContainer{
		container: pgContainer,
		pool:      pool,
		db:        db,
		repo:      repo,
	}
}

// loadBenchSchema creates the schema for benchmarks.
func loadBenchSchema(b *testing.B, pool *pgxpool.Pool) {
	b.Helper()

	ctx := context.Background()
	schemaName := tenant.TenantID("bench_tenant").SchemaName()

	// Create schema
	_, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
	if err != nil {
		b.Fatalf("Failed to create schema: %v", err)
	}

	// Create internal_account table
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.internal_account (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			created_by VARCHAR(100) NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_by VARCHAR(100) NOT NULL,
			deleted_at TIMESTAMPTZ,
			account_id VARCHAR(100) NOT NULL UNIQUE,
			account_code VARCHAR(50) NOT NULL,
			name VARCHAR(255) NOT NULL,
			account_type VARCHAR(20) NOT NULL,
			product_type_code VARCHAR(100) NULL,
			product_type_version INTEGER NULL,
			instrument_code VARCHAR(32) NOT NULL,
			dimension VARCHAR(20) NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
			counterparty_id VARCHAR(50),
			counterparty_name VARCHAR(255),
			counterparty_external_ref VARCHAR(100),
			attributes JSONB NOT NULL DEFAULT '{}',
			version BIGINT NOT NULL DEFAULT 1
		)
	`, pq.QuoteIdentifier(schemaName)))
	if err != nil {
		b.Fatalf("Failed to create internal_account table: %v", err)
	}

	// Create indexes
	_, err = pool.Exec(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_bench_account_code ON %s.internal_account (account_code)`, pq.QuoteIdentifier(schemaName)))
	if err != nil {
		b.Fatalf("Failed to create account_code index: %v", err)
	}

	// Create status history table
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.internal_account_status_history (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			created_by VARCHAR(100) NOT NULL,
			account_id VARCHAR(100) NOT NULL,
			old_status VARCHAR(20) NOT NULL,
			new_status VARCHAR(20) NOT NULL,
			reason TEXT
		)
	`, pq.QuoteIdentifier(schemaName)))
	if err != nil {
		b.Fatalf("Failed to create status_history table: %v", err)
	}
}

// cleanup closes the pool and terminates the container for benchmarks.
func (tc *benchTestContainer) cleanup(b *testing.B) {
	b.Helper()
	ctx := context.Background()

	if tc.pool != nil {
		tc.pool.Close()
	}

	if tc.container != nil {
		if err := tc.container.Terminate(ctx); err != nil {
			b.Logf("Warning: failed to terminate container: %v", err)
		}
	}
}

// createBenchAccount creates an account for benchmarking.
func createBenchAccount(accountID, accountCode, name string, accountType domain.AccountType) domain.InternalAccount {
	account, err := domain.NewInternalAccount(
		accountID,
		accountCode,
		name,
		accountType,
		domain.ClearingPurposeUnspecified,
		"GBP",
		"CURRENCY",
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create account: %v", err))
	}
	return account
}

// BenchmarkRepository_Save benchmarks saving a new account to PostgreSQL.
// Target: p99 < 50ms
func BenchmarkRepository_Save(b *testing.B) {
	tc := setupBenchContainer(b)
	defer tc.cleanup(b)

	tid := tenant.TenantID("bench_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, "bench-user")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		account := createBenchAccount(
			fmt.Sprintf("IBA-BENCH-%08d", i),
			fmt.Sprintf("BENCH_%08d", i),
			fmt.Sprintf("Benchmark Account %d", i),
			domain.AccountTypeClearing,
		)

		err := tc.repo.Save(ctx, account)
		if err != nil {
			b.Fatalf("Save failed: %v", err)
		}
	}
}

// BenchmarkRepository_FindByID benchmarks finding an account by ID.
// Target: p99 < 5ms
func BenchmarkRepository_FindByID(b *testing.B) {
	tc := setupBenchContainer(b)
	defer tc.cleanup(b)

	tid := tenant.TenantID("bench_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, "bench-user")

	// Create test account
	account := createBenchAccount("IBA-FIND-ID", "FIND_BY_ID", "Find By ID Test", domain.AccountTypeClearing)
	if err := tc.repo.Save(ctx, account); err != nil {
		b.Fatalf("Setup failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := tc.repo.FindByID(ctx, account.ID())
		if err != nil {
			b.Fatalf("FindByID failed: %v", err)
		}
	}
}

// BenchmarkRepository_FindByCode benchmarks finding an account by code.
// Target: p99 < 5ms
func BenchmarkRepository_FindByCode(b *testing.B) {
	tc := setupBenchContainer(b)
	defer tc.cleanup(b)

	tid := tenant.TenantID("bench_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, "bench-user")

	// Create test account
	account := createBenchAccount("IBA-FIND-CODE", "FIND_BY_CODE", "Find By Code Test", domain.AccountTypeClearing)
	if err := tc.repo.Save(ctx, account); err != nil {
		b.Fatalf("Setup failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := tc.repo.FindByCode(ctx, "FIND_BY_CODE")
		if err != nil {
			b.Fatalf("FindByCode failed: %v", err)
		}
	}
}

// BenchmarkRepository_ExistsByCode benchmarks checking account existence.
// Target: p99 < 5ms
func BenchmarkRepository_ExistsByCode(b *testing.B) {
	tc := setupBenchContainer(b)
	defer tc.cleanup(b)

	tid := tenant.TenantID("bench_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, "bench-user")

	// Create test account
	account := createBenchAccount("IBA-EXISTS", "EXISTS_CODE", "Exists Test", domain.AccountTypeClearing)
	if err := tc.repo.Save(ctx, account); err != nil {
		b.Fatalf("Setup failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		exists, err := tc.repo.ExistsByCode(ctx, "EXISTS_CODE")
		if err != nil {
			b.Fatalf("ExistsByCode failed: %v", err)
		}
		if !exists {
			b.Fatalf("ExistsByCode returned false, expected true")
		}
	}
}

// BenchmarkRepository_SaveParallel benchmarks concurrent account creation.
// Target: 1000 accounts in < 30s
func BenchmarkRepository_SaveParallel(b *testing.B) {
	tc := setupBenchContainer(b)
	defer tc.cleanup(b)

	tid := tenant.TenantID("bench_tenant")
	baseCtx := tenant.WithTenant(context.Background(), tid)
	baseCtx = context.WithValue(baseCtx, auth.UserIDContextKey, "bench-user")

	var counter int64

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pbb *testing.PB) {
		for pbb.Next() {
			i := atomic.AddInt64(&counter, 1)
			account := createBenchAccount(
				fmt.Sprintf("IBA-PAR-%08d", i),
				fmt.Sprintf("PAR_%08d", i),
				fmt.Sprintf("Parallel Account %d", i),
				domain.AccountTypeClearing,
			)

			err := tc.repo.Save(baseCtx, account)
			if err != nil {
				b.Errorf("Parallel Save failed: %v", err)
			}
		}
	})
}

// BenchmarkRepository_FindByIDParallel benchmarks concurrent FindByID operations.
func BenchmarkRepository_FindByIDParallel(b *testing.B) {
	tc := setupBenchContainer(b)
	defer tc.cleanup(b)

	tid := tenant.TenantID("bench_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, "bench-user")

	// Create test account
	account := createBenchAccount("IBA-PAR-FIND", "PAR_FIND", "Parallel Find Test", domain.AccountTypeClearing)
	if err := tc.repo.Save(ctx, account); err != nil {
		b.Fatalf("Setup failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pbb *testing.PB) {
		for pbb.Next() {
			_, err := tc.repo.FindByID(ctx, account.ID())
			if err != nil {
				b.Errorf("Parallel FindByID failed: %v", err)
			}
		}
	})
}

// TestIntegration_LoadTest_ConcurrentCreation tests creating 1000 accounts concurrently.
// Target: Complete in < 30 seconds
func TestIntegration_LoadTest_ConcurrentCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	tc := setupIntegrationTestContainer(t)
	defer tc.cleanup(t)

	tid := defaultTestTenantID
	ctx := tenant.WithTenant(context.Background(), tid)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, "load-test-user")

	const numAccounts = 1000
	const numWorkers = 10
	accountsPerWorker := numAccounts / numWorkers

	start := time.Now()
	var wg sync.WaitGroup
	errChan := make(chan error, numAccounts)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < accountsPerWorker; i++ {
				accountNum := workerID*accountsPerWorker + i
				account := createTestAccountIntegration(t,
					fmt.Sprintf("IBA-LOAD-%04d", accountNum),
					fmt.Sprintf("LOAD_%04d", accountNum),
					fmt.Sprintf("Load Test Account %d", accountNum),
					domain.AccountTypeClearing,
				)
				if err := tc.repo.Save(ctx, account); err != nil {
					errChan <- fmt.Errorf("worker %d account %d: %w", workerID, i, err)
				}
			}
		}(w)
	}

	wg.Wait()
	close(errChan)

	duration := time.Since(start)

	// Collect errors
	errs := make([]error, 0, len(errChan))
	for err := range errChan {
		errs = append(errs, err)
	}

	// Verify results
	assert.Empty(t, errs, "Should have no errors creating accounts")
	assert.Less(t, duration, 30*time.Second, "Should complete in under 30 seconds")

	t.Logf("Created %d accounts in %v (%.2f accounts/sec)",
		numAccounts, duration, float64(numAccounts)/duration.Seconds())

	// Verify all accounts were created
	accounts, err := tc.repo.List(ctx, domain.ListFilter{Limit: numAccounts + 10})
	require.NoError(t, err)
	// Count accounts with LOAD_ prefix
	loadAccountCount := 0
	for _, acc := range accounts {
		if len(acc.AccountCode()) >= 5 && acc.AccountCode()[:5] == "LOAD_" {
			loadAccountCount++
		}
	}
	assert.Equal(t, numAccounts, loadAccountCount, "Should have created all load test accounts")
}
