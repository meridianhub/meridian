package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// withdrawalTestTenantID is the tenant ID used in withdrawal repository tests.
const withdrawalTestTenantID = "test_tenant_withdrawal"

// setupWithdrawalTestDB creates a test database with the withdrawal table.
func setupWithdrawalTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&WithdrawalEntity{}})

	// Create the tenant schema for tests
	tid := tenant.TenantID(withdrawalTestTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the withdrawal table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.withdrawal (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
		instrument_code VARCHAR(32) NOT NULL DEFAULT '',
		dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
		precision INT NOT NULL DEFAULT 2,
		status VARCHAR(20) NOT NULL CHECK (status IN ('PENDING','COMPLETED','FAILED','CANCELLED')),
		reference VARCHAR(255) NOT NULL UNIQUE,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		version BIGINT NOT NULL DEFAULT 1
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create index for efficient account + status queries
	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_withdrawal_account_status
		ON %s.withdrawal (account_id, status)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

// createTestWithdrawal creates a test withdrawal with the given parameters.
func createTestWithdrawal(t *testing.T, accountID uuid.UUID, amountCents int64, reference string) *domain.Withdrawal {
	t.Helper()
	amount, err := domain.NewMoney("GBP", amountCents)
	require.NoError(t, err)

	withdrawal, err := domain.NewWithdrawal(accountID, amount, reference)
	require.NoError(t, err)

	return withdrawal
}

// createTestWithdrawalWithTimestamp creates a test withdrawal with an explicit timestamp.
// This enables deterministic ordering in tests without relying on time.Sleep.
func createTestWithdrawalWithTimestamp(t *testing.T, accountID uuid.UUID, amountCents int64, reference string, createdAt time.Time) *domain.Withdrawal {
	t.Helper()
	withdrawal := createTestWithdrawal(t, accountID, amountCents, reference)
	withdrawal.CreatedAt = createdAt
	withdrawal.UpdatedAt = createdAt
	return withdrawal
}

// createWithdrawalTableInSchema creates the withdrawal table in the specified schema.
// This helper reduces duplication in tests that need multiple tenant schemas.
func createWithdrawalTableInSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.withdrawal (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
		instrument_code VARCHAR(32) NOT NULL DEFAULT '',
		dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
		precision INT NOT NULL DEFAULT 2,
		status VARCHAR(20) NOT NULL,
		reference VARCHAR(255) NOT NULL UNIQUE,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		version BIGINT NOT NULL DEFAULT 1
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)
}

// --- Create Tests ---

func TestWithdrawalRepository_Create_Success(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()
	withdrawal := createTestWithdrawal(t, accountID, 10000, "WD-001")

	err := repo.Create(ctx, withdrawal)
	require.NoError(t, err)

	// Verify withdrawal was saved
	retrieved, err := repo.FindByID(ctx, withdrawal.ID)
	require.NoError(t, err)

	assert.Equal(t, withdrawal.ID, retrieved.ID)
	assert.Equal(t, accountID, retrieved.AccountID)
	amountCents, err := retrieved.Amount.ToMinorUnits()
	require.NoError(t, err)
	assert.Equal(t, int64(10000), amountCents)
	assert.Equal(t, "GBP", retrieved.Amount.InstrumentCode())
	assert.Equal(t, domain.WithdrawalStatusPending, retrieved.Status)
	assert.Equal(t, "WD-001", retrieved.Reference)
	assert.Equal(t, 1, retrieved.Version)
}

func TestWithdrawalRepository_Create_DuplicateReference(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()

	// Create first withdrawal
	withdrawal1 := createTestWithdrawal(t, accountID, 10000, "WD-DUPLICATE")
	err := repo.Create(ctx, withdrawal1)
	require.NoError(t, err)

	// Attempt to create second withdrawal with same reference
	withdrawal2 := createTestWithdrawal(t, accountID, 5000, "WD-DUPLICATE")
	err = repo.Create(ctx, withdrawal2)

	// Should fail with unique constraint violation
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate") // GORM/PostgreSQL returns this for unique violations
}

// --- FindByID Tests ---

func TestWithdrawalRepository_FindByID_Success(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()
	withdrawal := createTestWithdrawal(t, accountID, 15000, "WD-FIND-001")
	require.NoError(t, repo.Create(ctx, withdrawal))

	retrieved, err := repo.FindByID(ctx, withdrawal.ID)
	require.NoError(t, err)

	assert.Equal(t, withdrawal.ID, retrieved.ID)
	assert.Equal(t, withdrawal.Reference, retrieved.Reference)
}

func TestWithdrawalRepository_FindByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrWithdrawalNotFound)
}

// --- FindByReference Tests ---

func TestWithdrawalRepository_FindByReference_Success(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()
	withdrawal := createTestWithdrawal(t, accountID, 20000, "WD-REF-UNIQUE-123")
	require.NoError(t, repo.Create(ctx, withdrawal))

	retrieved, err := repo.FindByReference(ctx, "WD-REF-UNIQUE-123")
	require.NoError(t, err)

	assert.Equal(t, withdrawal.ID, retrieved.ID)
	assert.Equal(t, "WD-REF-UNIQUE-123", retrieved.Reference)
}

func TestWithdrawalRepository_FindByReference_NotFound(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)

	_, err := repo.FindByReference(ctx, "WD-NONEXISTENT")
	assert.ErrorIs(t, err, ErrWithdrawalNotFound)
}

// --- Update Tests ---

func TestWithdrawalRepository_Update_Success(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()
	withdrawal := createTestWithdrawal(t, accountID, 25000, "WD-UPDATE-001")
	require.NoError(t, repo.Create(ctx, withdrawal))

	// Complete the withdrawal
	require.NoError(t, withdrawal.Complete())
	require.NoError(t, repo.Update(ctx, withdrawal))

	// Verify status was updated
	retrieved, err := repo.FindByID(ctx, withdrawal.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.WithdrawalStatusCompleted, retrieved.Status)
	assert.Equal(t, 2, retrieved.Version)
}

func TestWithdrawalRepository_Update_StaleVersion(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()
	withdrawal := createTestWithdrawal(t, accountID, 30000, "WD-OPTIMISTIC-001")
	require.NoError(t, repo.Create(ctx, withdrawal))

	// Load same withdrawal twice (simulating concurrent access)
	withdrawal1, err := repo.FindByID(ctx, withdrawal.ID)
	require.NoError(t, err)

	withdrawal2, err := repo.FindByID(ctx, withdrawal.ID)
	require.NoError(t, err)

	// First update succeeds
	require.NoError(t, withdrawal1.Complete())
	require.NoError(t, repo.Update(ctx, withdrawal1))

	// Second update fails due to version conflict
	require.NoError(t, withdrawal2.Fail())
	err = repo.Update(ctx, withdrawal2)
	assert.ErrorIs(t, err, ErrWithdrawalVersionConflict)

	// Verify first transaction's changes persisted
	final, err := repo.FindByID(ctx, withdrawal.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.WithdrawalStatusCompleted, final.Status)
	assert.Equal(t, 2, final.Version)
}

func TestWithdrawalRepository_Update_NonExistent(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)

	// Create a withdrawal in memory but don't save it
	withdrawal := createTestWithdrawal(t, uuid.New(), 10000, "WD-NONEXISTENT")

	// Try to update non-existent withdrawal
	err := repo.Update(ctx, withdrawal)

	// Should fail with version conflict (no rows affected)
	assert.ErrorIs(t, err, ErrWithdrawalVersionConflict)
}

// --- List Tests ---

func TestWithdrawalRepository_List_Pagination(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()

	// Create 5 withdrawals with explicit monotonically increasing timestamps
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		timestamp := baseTime.Add(time.Duration(i) * time.Second)
		withdrawal := createTestWithdrawalWithTimestamp(t, accountID, int64(10000+i*1000), fmt.Sprintf("WD-LIST-%03d", i), timestamp)
		require.NoError(t, repo.Create(ctx, withdrawal))
	}

	// Test page 1 (limit 2)
	page1, err := repo.List(ctx, accountID, PaginationParams{Offset: 0, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, page1, 2)

	// Test page 2 (offset 2, limit 2)
	page2, err := repo.List(ctx, accountID, PaginationParams{Offset: 2, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, page2, 2)

	// Test page 3 (offset 4, limit 2) - should return 1
	page3, err := repo.List(ctx, accountID, PaginationParams{Offset: 4, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, page3, 1)

	// Verify ordering (DESC by created_at - most recent first)
	// The last created withdrawal should be first in page 1
	assert.Equal(t, "WD-LIST-004", page1[0].Reference)
	assert.Equal(t, "WD-LIST-003", page1[1].Reference)
}

func TestWithdrawalRepository_List_EmptyResult(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()

	// List for account with no withdrawals
	withdrawals, err := repo.List(ctx, accountID, PaginationParams{Offset: 0, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, withdrawals)
}

func TestWithdrawalRepository_List_FiltersByAccountID(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountA := uuid.New()
	accountB := uuid.New()

	// Create withdrawals for two different accounts
	withdrawalA1 := createTestWithdrawal(t, accountA, 10000, "WD-A-001")
	require.NoError(t, repo.Create(ctx, withdrawalA1))

	withdrawalA2 := createTestWithdrawal(t, accountA, 20000, "WD-A-002")
	require.NoError(t, repo.Create(ctx, withdrawalA2))

	withdrawalB1 := createTestWithdrawal(t, accountB, 15000, "WD-B-001")
	require.NoError(t, repo.Create(ctx, withdrawalB1))

	// List for account A should return only A's withdrawals
	listA, err := repo.List(ctx, accountA, PaginationParams{Offset: 0, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, listA, 2)

	// List for account B should return only B's withdrawals
	listB, err := repo.List(ctx, accountB, PaginationParams{Offset: 0, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, listB, 1)
}

// --- Tenant Isolation Tests ---

func TestWithdrawalRepository_TenantIsolation(t *testing.T) {
	// This test verifies that withdrawals in tenant A are not visible to tenant B
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&WithdrawalEntity{}})
	defer cleanup()

	// Setup tenant A
	tenantA := tenant.TenantID("tenant_a")
	createWithdrawalTableInSchema(t, db, tenantA.SchemaName())

	// Setup tenant B
	tenantB := tenant.TenantID("tenant_b")
	createWithdrawalTableInSchema(t, db, tenantB.SchemaName())

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()

	// Create withdrawal in tenant A
	ctxA := tenant.WithTenant(context.Background(), tenantA)
	withdrawalA := createTestWithdrawal(t, accountID, 10000, "WD-TENANT-A")
	err := repo.Create(ctxA, withdrawalA)
	require.NoError(t, err)

	// Verify withdrawal is visible in tenant A
	found, err := repo.FindByID(ctxA, withdrawalA.ID)
	require.NoError(t, err)
	assert.Equal(t, withdrawalA.ID, found.ID)

	// Verify withdrawal is NOT visible in tenant B
	ctxB := tenant.WithTenant(context.Background(), tenantB)
	_, err = repo.FindByID(ctxB, withdrawalA.ID)
	assert.ErrorIs(t, err, ErrWithdrawalNotFound)

	// Verify FindByReference also respects tenant isolation
	_, err = repo.FindByReference(ctxB, "WD-TENANT-A")
	assert.ErrorIs(t, err, ErrWithdrawalNotFound)

	// Verify List also respects tenant isolation
	listB, err := repo.List(ctxB, accountID, PaginationParams{Offset: 0, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, listB)
}

// --- Domain Conversion Error Tests ---

func TestWithdrawalRepository_FindByID_CorruptedData_ReturnsError(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)

	// Manually insert corrupted data (empty instrument code)
	corruptedEntity := &WithdrawalEntity{
		ID:             uuid.New(),
		AccountID:      uuid.New(),
		AmountCents:    10000,
		InstrumentCode: "", // Corrupted: empty instrument code
		Dimension:      "CURRENCY",
		Precision:      2,
		Status:         "PENDING",
		Reference:      "WD-CORRUPT",
		Version:        1,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	_, err := repo.FindByID(ctx, corruptedEntity.ID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}

func TestWithdrawalRepository_List_PartialCorruption_ReturnsError(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()

	// Create valid withdrawal
	validWithdrawal := createTestWithdrawal(t, accountID, 10000, "WD-VALID")
	require.NoError(t, repo.Create(ctx, validWithdrawal))

	// Manually insert corrupted withdrawal for same account
	corruptedEntity := &WithdrawalEntity{
		ID:             uuid.New(),
		AccountID:      accountID,
		AmountCents:    5000,
		InstrumentCode: "", // Corrupted: empty instrument code
		Dimension:      "CURRENCY",
		Precision:      2,
		Status:         "PENDING",
		Reference:      "WD-CORRUPT-LIST",
		Version:        1,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	_, err := repo.List(ctx, accountID, PaginationParams{Offset: 0, Limit: 10})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}

// --- WithTx Tests ---

func TestWithdrawalRepository_WithTx(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()
	withdrawal := createTestWithdrawal(t, accountID, 50000, "WD-TX-001")

	// Start a transaction
	tx := db.Begin()
	require.NoError(t, tx.Error)

	// Set search_path for the transaction
	tid := tenant.TenantID(withdrawalTestTenantID)
	schemaName := tid.SchemaName()
	err := tx.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Use WithTx to create repository scoped to transaction
	txRepo := repo.WithTx(tx)

	// Create within transaction
	err = txRepo.Create(ctx, withdrawal)
	require.NoError(t, err)

	// Verify visible within transaction
	found, err := txRepo.FindByID(ctx, withdrawal.ID)
	require.NoError(t, err)
	assert.Equal(t, withdrawal.ID, found.ID)

	// Rollback transaction
	require.NoError(t, tx.Rollback().Error)

	// Verify NOT visible after rollback
	_, err = repo.FindByID(ctx, withdrawal.ID)
	assert.ErrorIs(t, err, ErrWithdrawalNotFound)
}

// --- State Machine Integration Tests ---

func TestWithdrawalRepository_StateTransitions(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()

	testCases := []struct {
		name           string
		transition     func(*domain.Withdrawal) error
		expectedStatus domain.WithdrawalStatus
	}{
		{
			name:           "Complete",
			transition:     func(w *domain.Withdrawal) error { return w.Complete() },
			expectedStatus: domain.WithdrawalStatusCompleted,
		},
		{
			name:           "Fail",
			transition:     func(w *domain.Withdrawal) error { return w.Fail() },
			expectedStatus: domain.WithdrawalStatusFailed,
		},
		{
			name:           "Cancel",
			transition:     func(w *domain.Withdrawal) error { return w.Cancel() },
			expectedStatus: domain.WithdrawalStatusCancelled,
		},
	}

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			withdrawal := createTestWithdrawal(t, accountID, 10000, fmt.Sprintf("WD-STATE-%d", i))
			require.NoError(t, repo.Create(ctx, withdrawal))

			// Apply state transition
			require.NoError(t, tc.transition(withdrawal))
			require.NoError(t, repo.Update(ctx, withdrawal))

			// Verify persisted state
			retrieved, err := repo.FindByID(ctx, withdrawal.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedStatus, retrieved.Status)
			assert.Equal(t, 2, retrieved.Version)
		})
	}
}
