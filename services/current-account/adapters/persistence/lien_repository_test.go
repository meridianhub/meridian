package persistence

import (
	"context"
	"errors"
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

// testTenantID is the tenant ID used in tests.
// The schema will be created in setupLienTestDB.
const testTenantID = "test_tenant"

func setupLienTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&LienEntity{}})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the lien table in the tenant schema (matches LienEntity.TableName() = "lien")
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.lien (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(3) NOT NULL,
		instrument_code VARCHAR(32) NOT NULL DEFAULT '',
		dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
		precision INT NOT NULL DEFAULT 2,
		bucket_id VARCHAR(255) NOT NULL DEFAULT '',
		status VARCHAR(20) NOT NULL,
		payment_order_reference VARCHAR(255) NOT NULL UNIQUE,
		termination_reason TEXT,
		expires_at TIMESTAMP WITH TIME ZONE,
		reserved_quantity JSONB,
		valued_amount JSONB,
		valuation_analysis JSONB,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		version INT NOT NULL DEFAULT 1
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema so Create/Update work in the tenant schema
	// This ensures consistency - all operations use the tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func TestLienRepository_Create(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "bucket-abc", "PO-001", nil)
	require.NoError(t, err)

	err = repo.Create(ctx, lien)
	require.NoError(t, err)

	// Verify lien was saved
	retrieved, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)

	assert.Equal(t, lien.ID, retrieved.ID)
	assert.Equal(t, accountID, retrieved.AccountID)
	amountCents, err := retrieved.Amount.ToMinorUnits()
	require.NoError(t, err)
	assert.Equal(t, int64(10000), amountCents)
	assert.Equal(t, "GBP", retrieved.Amount.InstrumentCode())
	assert.Equal(t, "bucket-abc", retrieved.BucketID)
	assert.Equal(t, domain.LienStatusActive, retrieved.Status)
	assert.Equal(t, "PO-001", retrieved.PaymentOrderReference)
}

func TestLienRepository_FindByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrLienNotFound)
}

func TestLienRepository_FindByAccountID(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create two liens for same account
	lien1, err := domain.NewLien(accountID, amount, "", "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, lien1))

	lien2, err := domain.NewLien(accountID, amount, "", "PO-002", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, lien2))

	// Create lien for different account
	otherAccountID := uuid.New()
	lien3, err := domain.NewLien(otherAccountID, amount, "", "PO-003", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, lien3))

	liens, err := repo.FindByAccountID(ctx, accountID)
	require.NoError(t, err)

	assert.Len(t, liens, 2)
}

func TestLienRepository_FindActiveByAccountID(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create active lien
	activeLien, err := domain.NewLien(accountID, amount, "", "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, activeLien))

	// Create and execute a lien
	executedLien, err := domain.NewLien(accountID, amount, "", "PO-002", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, executedLien))
	require.NoError(t, executedLien.Execute())
	require.NoError(t, repo.Update(ctx, executedLien))

	// Create and terminate a lien
	terminatedLien, err := domain.NewLien(accountID, amount, "", "PO-003", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, terminatedLien))
	require.NoError(t, terminatedLien.Terminate("Cancelled"))
	require.NoError(t, repo.Update(ctx, terminatedLien))

	// Only active lien should be returned
	liens, err := repo.FindActiveByAccountID(ctx, accountID)
	require.NoError(t, err)

	assert.Len(t, liens, 1)
	assert.Equal(t, activeLien.ID, liens[0].ID)
}

func TestLienRepository_FindActiveByAccountID_ExcludesExpired(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create active lien without expiration
	activeLien, err := domain.NewLien(accountID, amount, "", "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, activeLien))

	// Create active lien with past expiration (should be excluded)
	past := time.Now().Add(-1 * time.Hour)
	expiredLien, err := domain.NewLien(accountID, amount, "", "PO-002", &past)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, expiredLien))

	// Only non-expired active lien should be returned
	liens, err := repo.FindActiveByAccountID(ctx, accountID)
	require.NoError(t, err)

	assert.Len(t, liens, 1)
	assert.Equal(t, activeLien.ID, liens[0].ID)
}

func TestLienRepository_FindByPaymentOrderReference(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "", "PO-UNIQUE-123", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, lien))

	retrieved, err := repo.FindByPaymentOrderReference(ctx, "PO-UNIQUE-123")
	require.NoError(t, err)

	assert.Equal(t, lien.ID, retrieved.ID)
}

func TestLienRepository_FindByPaymentOrderReference_NotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	_, err := repo.FindByPaymentOrderReference(ctx, "PO-NONEXISTENT")
	assert.ErrorIs(t, err, ErrLienNotFound)
}

func TestLienRepository_Update_Execute(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "", "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, lien))

	// Execute the lien
	require.NoError(t, lien.Execute())
	require.NoError(t, repo.Update(ctx, lien))

	// Verify status was updated
	retrieved, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.LienStatusExecuted, retrieved.Status)
	assert.Equal(t, 2, retrieved.Version)
}

func TestLienRepository_Update_Terminate(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "", "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, lien))

	// Terminate the lien
	require.NoError(t, lien.Terminate("Payment failed"))
	require.NoError(t, repo.Update(ctx, lien))

	// Verify status and reason were updated
	retrieved, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.LienStatusTerminated, retrieved.Status)
	assert.Equal(t, "Payment failed", retrieved.TerminationReason)
	assert.Equal(t, 2, retrieved.Version)
}

func TestLienRepository_OptimisticLocking(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create initial lien
	lien, err := domain.NewLien(accountID, amount, "", "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, lien))

	// Load same lien twice (simulating concurrent access)
	lien1, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)

	lien2, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)

	// First update succeeds
	require.NoError(t, lien1.Execute())
	require.NoError(t, repo.Update(ctx, lien1))

	// Second update fails due to version conflict
	require.NoError(t, lien2.Terminate("Should fail"))
	err = repo.Update(ctx, lien2)
	assert.ErrorIs(t, err, ErrLienVersionConflict)

	// Verify first transaction's changes persisted
	final, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.LienStatusExecuted, final.Status)
	assert.Equal(t, 2, final.Version)
}

func TestLienRepository_SumActiveAmountByAccountID(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Create active liens
	amount1, _ := domain.NewMoney("GBP", 20000) // £200
	lien1, _ := domain.NewLien(accountID, amount1, "", "PO-001", nil)
	require.NoError(t, repo.Create(ctx, lien1))

	amount2, _ := domain.NewMoney("GBP", 15000) // £150
	lien2, _ := domain.NewLien(accountID, amount2, "", "PO-002", nil)
	require.NoError(t, repo.Create(ctx, lien2))

	// Create and execute a lien (should not be counted)
	amount3, _ := domain.NewMoney("GBP", 10000)
	lien3, _ := domain.NewLien(accountID, amount3, "", "PO-003", nil)
	require.NoError(t, repo.Create(ctx, lien3))
	require.NoError(t, lien3.Execute())
	require.NoError(t, repo.Update(ctx, lien3))

	// Sum should only include active non-expired liens
	total, err := repo.SumActiveAmountByAccountID(ctx, accountID)
	require.NoError(t, err)

	assert.Equal(t, int64(35000), total) // £200 + £150 = £350
}

func TestLienRepository_SumActiveAmountByAccountID_ExcludesExpired(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Create active lien
	amount1, _ := domain.NewMoney("GBP", 20000)
	lien1, _ := domain.NewLien(accountID, amount1, "", "PO-001", nil)
	require.NoError(t, repo.Create(ctx, lien1))

	// Create expired active lien (should not be counted)
	past := time.Now().Add(-1 * time.Hour)
	amount2, _ := domain.NewMoney("GBP", 15000)
	lien2, _ := domain.NewLien(accountID, amount2, "", "PO-002", &past)
	require.NoError(t, repo.Create(ctx, lien2))

	// Sum should only include non-expired active liens
	total, err := repo.SumActiveAmountByAccountID(ctx, accountID)
	require.NoError(t, err)

	assert.Equal(t, int64(20000), total) // Only £200, expired lien excluded
}

func TestLienRepository_SumActiveAmountByAccountID_CurrencyInconsistency(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Create active lien in GBP
	amount1, _ := domain.NewMoney("GBP", 20000)
	lien1, _ := domain.NewLien(accountID, amount1, "", "PO-001", nil)
	require.NoError(t, repo.Create(ctx, lien1))

	// Manually insert lien with different instrument code (simulating data corruption)
	corruptedEntity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AmountCents:           15000,
		InstrumentCode:        "EUR", // Different instrument - data corruption
		Dimension:             "CURRENCY",
		Precision:             2,
		BucketID:              "",
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-CORRUPT",
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	// Sum should return currency inconsistency error
	_, err := repo.SumActiveAmountByAccountID(ctx, accountID)
	assert.ErrorIs(t, err, ErrLienCurrencyInconsistent)
}

func TestLienRepository_SumActiveAmountByAccountID_NoLiens(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	total, err := repo.SumActiveAmountByAccountID(ctx, accountID)
	require.NoError(t, err)

	assert.Equal(t, int64(0), total)
}

func TestLienRepository_SumActiveAmountByAccountIDAndBucket(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Create liens in bucket "bucket-a"
	amount1, _ := domain.NewMoney("GBP", 20000) // 200 GBP
	lien1, _ := domain.NewLien(accountID, amount1, "bucket-a", "PO-001", nil)
	require.NoError(t, repo.Create(ctx, lien1))

	amount2, _ := domain.NewMoney("GBP", 15000) // 150 GBP
	lien2, _ := domain.NewLien(accountID, amount2, "bucket-a", "PO-002", nil)
	require.NoError(t, repo.Create(ctx, lien2))

	// Create liens in bucket "bucket-b"
	amount3, _ := domain.NewMoney("GBP", 30000) // 300 GBP
	lien3, _ := domain.NewLien(accountID, amount3, "bucket-b", "PO-003", nil)
	require.NoError(t, repo.Create(ctx, lien3))

	// Sum for bucket-a should be 35000 cents (200 + 150 GBP)
	totalA, err := repo.SumActiveAmountByAccountIDAndBucket(ctx, accountID, "bucket-a")
	require.NoError(t, err)
	assert.Equal(t, int64(35000), totalA)

	// Sum for bucket-b should be 30000 cents (300 GBP)
	totalB, err := repo.SumActiveAmountByAccountIDAndBucket(ctx, accountID, "bucket-b")
	require.NoError(t, err)
	assert.Equal(t, int64(30000), totalB)

	// Sum for non-existent bucket should be 0
	totalC, err := repo.SumActiveAmountByAccountIDAndBucket(ctx, accountID, "bucket-c")
	require.NoError(t, err)
	assert.Equal(t, int64(0), totalC)
}

func TestLienRepository_SumActiveAmountByAccountIDAndBucket_ExcludesExecuted(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	bucketID := "test-bucket"

	// Create active lien
	amount1, _ := domain.NewMoney("GBP", 20000)
	lien1, _ := domain.NewLien(accountID, amount1, bucketID, "PO-001", nil)
	require.NoError(t, repo.Create(ctx, lien1))

	// Create and execute a lien (should not be counted)
	amount2, _ := domain.NewMoney("GBP", 10000)
	lien2, _ := domain.NewLien(accountID, amount2, bucketID, "PO-002", nil)
	require.NoError(t, repo.Create(ctx, lien2))
	require.NoError(t, lien2.Execute())
	require.NoError(t, repo.Update(ctx, lien2))

	// Sum should only include active lien
	total, err := repo.SumActiveAmountByAccountIDAndBucket(ctx, accountID, bucketID)
	require.NoError(t, err)
	assert.Equal(t, int64(20000), total)
}

func TestLienRepository_SumActiveAmountByAccountIDAndBucket_CurrencyInconsistency(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	bucketID := "test-bucket"

	// Create active lien in GBP
	amount1, _ := domain.NewMoney("GBP", 20000)
	lien1, _ := domain.NewLien(accountID, amount1, bucketID, "PO-001", nil)
	require.NoError(t, repo.Create(ctx, lien1))

	// Manually insert lien with different instrument code (simulating data corruption)
	corruptedEntity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AmountCents:           15000,
		InstrumentCode:        "EUR", // Different instrument - data corruption
		Dimension:             "CURRENCY",
		Precision:             2,
		BucketID:              bucketID,
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-CORRUPT",
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	// Sum should return currency inconsistency error
	_, err := repo.SumActiveAmountByAccountIDAndBucket(ctx, accountID, bucketID)
	assert.ErrorIs(t, err, ErrLienCurrencyInconsistent)
}

func TestLienRepository_CreateWithExpiration(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)
	expiresAt := time.Now().Add(24 * time.Hour)

	lien, err := domain.NewLien(accountID, amount, "", "PO-001", &expiresAt)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, lien))

	retrieved, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)

	require.NotNil(t, retrieved.ExpiresAt)
	assert.Equal(t, expiresAt.Unix(), retrieved.ExpiresAt.Unix())
}

// Defensive tests for toLienDomain error handling

func TestToLienDomain_InvalidInstrumentCode_ReturnsError(t *testing.T) {
	entity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10000,
		InstrumentCode:        "", // Invalid: empty instrument code
		Dimension:             "CURRENCY",
		Precision:             2,
		BucketID:              "",
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-001",
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	_, err := toLienDomain(entity)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}

func TestLienRepository_FindByID_CorruptedData_ReturnsError(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	// Manually insert corrupted data (empty instrument code)
	corruptedEntity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10000,
		InstrumentCode:        "", // Corrupted
		Dimension:             "CURRENCY",
		Precision:             2,
		BucketID:              "",
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-CORRUPT",
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	_, err := repo.FindByID(ctx, corruptedEntity.ID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}

func TestLienRepository_FindByAccountID_PartialCorruption_ReturnsError(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Create valid lien
	validAmount, _ := domain.NewMoney("GBP", 10000)
	validLien, _ := domain.NewLien(accountID, validAmount, "", "PO-001", nil)
	require.NoError(t, repo.Create(ctx, validLien))

	// Manually insert corrupted lien for same account
	corruptedEntity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AmountCents:           5000,
		InstrumentCode:        "", // Corrupted
		Dimension:             "CURRENCY",
		Precision:             2,
		BucketID:              "",
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-CORRUPT",
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	_, err := repo.FindByAccountID(ctx, accountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}

func TestLienRepository_Update_NonExistent_ReturnsError(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	// Create a lien in memory but don't save it
	amount, _ := domain.NewMoney("GBP", 10000)
	lien, _ := domain.NewLien(uuid.New(), amount, "", "PO-001", nil)

	// Try to update non-existent lien
	err := repo.Update(ctx, lien)

	// Should fail with version conflict (no rows affected)
	assert.True(t, errors.Is(err, ErrLienVersionConflict))
}

// =============================================================================
// Tenant Isolation Integration Tests
// =============================================================================
//
// These tests verify that cross-tenant data leakage is prevented at the
// repository level. The repository uses withTenantTransaction which sets
// the PostgreSQL search_path to the tenant's schema, ensuring complete
// data isolation between tenants.

// setupMultiTenantLienTestDB creates a PostgreSQL container with multiple tenant schemas
// and returns contexts for each tenant.
func setupMultiTenantLienTestDB(t *testing.T, tenantIDs ...string) (*gorm.DB, map[string]context.Context, func()) {
	t.Helper()
	// Pass nil models to avoid auto-migration to public schema
	db, cleanup := testdb.SetupPostgres(t, nil)

	contexts := make(map[string]context.Context)
	for _, tenantID := range tenantIDs {
		tid := tenant.TenantID(tenantID)
		schemaName := tid.SchemaName()

		// Create the tenant schema
		err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
		require.NoError(t, err)

		// Create the lien table in the tenant schema (matches LienEntity.TableName() = "lien")
		err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.lien (
			id UUID PRIMARY KEY,
			account_id UUID NOT NULL,
			amount_cents BIGINT NOT NULL,
			currency VARCHAR(3) NOT NULL,
			instrument_code VARCHAR(32) NOT NULL DEFAULT '',
			dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
			precision INT NOT NULL DEFAULT 2,
			bucket_id VARCHAR(255) NOT NULL DEFAULT '',
			status VARCHAR(20) NOT NULL,
			payment_order_reference VARCHAR(255) NOT NULL UNIQUE,
			termination_reason TEXT,
			expires_at TIMESTAMP WITH TIME ZONE,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL,
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
			version INT NOT NULL DEFAULT 1,
			reserved_quantity JSONB,
			valued_amount JSONB,
			valuation_analysis JSONB
		)`, pq.QuoteIdentifier(schemaName))).Error
		require.NoError(t, err)

		// Create context with tenant
		contexts[tenantID] = tenant.WithTenant(context.Background(), tid)
	}

	return db, contexts, cleanup
}

func TestLienRepository_TenantIsolation_FindByID_CrossTenantReturnsNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, contexts, cleanup := setupMultiTenantLienTestDB(t, "org_123", "org_456")
	defer cleanup()

	repo := NewLienRepository(db)
	ctxOrg123 := contexts["org_123"]
	ctxOrg456 := contexts["org_456"]

	// Create lien in org_123 schema
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "", "PO-CROSS-TENANT-001", nil)
	require.NoError(t, err)

	err = repo.Create(ctxOrg123, lien)
	require.NoError(t, err, "Failed to create lien in org_123")

	// Verify lien exists in org_123
	retrieved, err := repo.FindByID(ctxOrg123, lien.ID)
	require.NoError(t, err)
	assert.Equal(t, lien.ID, retrieved.ID)

	// Query from org_456 context should return ErrLienNotFound
	_, err = repo.FindByID(ctxOrg456, lien.ID)
	assert.ErrorIs(t, err, ErrLienNotFound,
		"Query from org_456 should not find lien created in org_123")
}

func TestLienRepository_TenantIsolation_FindActiveByAccountID_OnlyReturnsTenantData(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, contexts, cleanup := setupMultiTenantLienTestDB(t, "tenant_alpha", "tenant_beta")
	defer cleanup()

	repo := NewLienRepository(db)
	ctxAlpha := contexts["tenant_alpha"]
	ctxBeta := contexts["tenant_beta"]

	// Use the same account ID in both tenants (simulating same logical account ID)
	sharedAccountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create liens in tenant_alpha
	lienAlpha1, err := domain.NewLien(sharedAccountID, amount, "", "PO-ALPHA-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctxAlpha, lienAlpha1))

	lienAlpha2, err := domain.NewLien(sharedAccountID, amount, "", "PO-ALPHA-002", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctxAlpha, lienAlpha2))

	// Create liens in tenant_beta with same account ID
	lienBeta1, err := domain.NewLien(sharedAccountID, amount, "", "PO-BETA-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctxBeta, lienBeta1))

	lienBeta2, err := domain.NewLien(sharedAccountID, amount, "", "PO-BETA-002", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctxBeta, lienBeta2))

	lienBeta3, err := domain.NewLien(sharedAccountID, amount, "", "PO-BETA-003", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctxBeta, lienBeta3))

	// Query from tenant_alpha context should only return alpha's liens
	liensFromAlpha, err := repo.FindActiveByAccountID(ctxAlpha, sharedAccountID)
	require.NoError(t, err)
	assert.Len(t, liensFromAlpha, 2, "tenant_alpha should only see its 2 liens")

	// Verify the IDs are the ones we created for alpha
	alphaIDs := map[uuid.UUID]bool{lienAlpha1.ID: true, lienAlpha2.ID: true}
	for _, lien := range liensFromAlpha {
		assert.True(t, alphaIDs[lien.ID], "Lien %s should belong to tenant_alpha", lien.ID)
	}

	// Query from tenant_beta context should only return beta's liens
	liensFromBeta, err := repo.FindActiveByAccountID(ctxBeta, sharedAccountID)
	require.NoError(t, err)
	assert.Len(t, liensFromBeta, 3, "tenant_beta should only see its 3 liens")

	// Verify the IDs are the ones we created for beta
	betaIDs := map[uuid.UUID]bool{lienBeta1.ID: true, lienBeta2.ID: true, lienBeta3.ID: true}
	for _, lien := range liensFromBeta {
		assert.True(t, betaIDs[lien.ID], "Lien %s should belong to tenant_beta", lien.ID)
	}
}

func TestLienRepository_TenantIsolation_FindByIDForUpdate_WrongTenantReturnsNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, contexts, cleanup := setupMultiTenantLienTestDB(t, "owner_tenant", "other_tenant")
	defer cleanup()

	repo := NewLienRepository(db)
	ctxOwner := contexts["owner_tenant"]
	ctxOther := contexts["other_tenant"]

	// Create lien in owner_tenant schema
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "", "PO-LOCKED-001", nil)
	require.NoError(t, err)

	err = repo.Create(ctxOwner, lien)
	require.NoError(t, err, "Failed to create lien in owner_tenant")

	// Verify lien can be locked by owner
	retrieved, err := repo.FindByIDForUpdate(ctxOwner, lien.ID)
	require.NoError(t, err)
	assert.Equal(t, lien.ID, retrieved.ID)

	// Attempt to lock from other_tenant should return ErrLienNotFound
	// even though the lien exists in a different schema
	_, err = repo.FindByIDForUpdate(ctxOther, lien.ID)
	assert.ErrorIs(t, err, ErrLienNotFound,
		"FindByIDForUpdate from wrong tenant should return ErrLienNotFound")
}

func TestLienRepository_TenantIsolation_Update_WrongTenantCannotModify(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, contexts, cleanup := setupMultiTenantLienTestDB(t, "data_owner", "malicious_tenant")
	defer cleanup()

	repo := NewLienRepository(db)
	ctxOwner := contexts["data_owner"]
	ctxMalicious := contexts["malicious_tenant"]

	// Create lien in data_owner schema
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 25000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "", "PO-PROTECTED-001", nil)
	require.NoError(t, err)

	err = repo.Create(ctxOwner, lien)
	require.NoError(t, err, "Failed to create lien in data_owner")

	// Load the lien as the owner to get the current version
	originalLien, err := repo.FindByID(ctxOwner, lien.ID)
	require.NoError(t, err)
	originalVersion := originalLien.Version

	// Attempt to update from malicious_tenant context
	// First, we need to construct a lien object with the same ID but different context
	maliciousLien := &domain.Lien{
		ID:                    lien.ID,
		AccountID:             accountID,
		Amount:                amount,
		BucketID:              "",
		Status:                domain.LienStatusTerminated, // Trying to terminate
		PaymentOrderReference: lien.PaymentOrderReference,
		TerminationReason:     "Malicious termination attempt",
		Version:               originalVersion,
		CreatedAt:             lien.CreatedAt,
		UpdatedAt:             time.Now(),
	}

	// Update with wrong tenant context should fail (no rows affected = version conflict)
	err = repo.Update(ctxMalicious, maliciousLien)
	assert.ErrorIs(t, err, ErrLienVersionConflict,
		"Update from wrong tenant should fail as no rows match in that tenant's schema")

	// Verify the original lien was NOT modified
	afterAttempt, err := repo.FindByID(ctxOwner, lien.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.LienStatusActive, afterAttempt.Status,
		"Lien status should remain ACTIVE after failed cross-tenant update attempt")
	assert.Equal(t, originalVersion, afterAttempt.Version,
		"Lien version should remain unchanged after failed cross-tenant update attempt")
	assert.Empty(t, afterAttempt.TerminationReason,
		"Lien termination reason should remain empty")
}

func TestLienRepository_TenantIsolation_SumActiveAmount_OnlyCountsTenantData(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, contexts, cleanup := setupMultiTenantLienTestDB(t, "sum_tenant_a", "sum_tenant_b")
	defer cleanup()

	repo := NewLienRepository(db)
	ctxA := contexts["sum_tenant_a"]
	ctxB := contexts["sum_tenant_b"]

	// Use the same account ID in both tenants
	sharedAccountID := uuid.New()

	// Create liens in tenant A: 100 + 200 = 300 GBP
	amountA1, _ := domain.NewMoney("GBP", 10000) // 100 GBP
	lienA1, _ := domain.NewLien(sharedAccountID, amountA1, "", "PO-SUM-A-001", nil)
	require.NoError(t, repo.Create(ctxA, lienA1))

	amountA2, _ := domain.NewMoney("GBP", 20000) // 200 GBP
	lienA2, _ := domain.NewLien(sharedAccountID, amountA2, "", "PO-SUM-A-002", nil)
	require.NoError(t, repo.Create(ctxA, lienA2))

	// Create liens in tenant B: 500 + 750 = 1250 GBP
	amountB1, _ := domain.NewMoney("GBP", 50000) // 500 GBP
	lienB1, _ := domain.NewLien(sharedAccountID, amountB1, "", "PO-SUM-B-001", nil)
	require.NoError(t, repo.Create(ctxB, lienB1))

	amountB2, _ := domain.NewMoney("GBP", 75000) // 750 GBP
	lienB2, _ := domain.NewLien(sharedAccountID, amountB2, "", "PO-SUM-B-002", nil)
	require.NoError(t, repo.Create(ctxB, lienB2))

	// Sum from tenant A should be 30000 cents (300 GBP)
	sumA, err := repo.SumActiveAmountByAccountID(ctxA, sharedAccountID)
	require.NoError(t, err)
	assert.Equal(t, int64(30000), sumA,
		"Tenant A sum should be 30000 cents (10000 + 20000)")

	// Sum from tenant B should be 125000 cents (1250 GBP)
	sumB, err := repo.SumActiveAmountByAccountID(ctxB, sharedAccountID)
	require.NoError(t, err)
	assert.Equal(t, int64(125000), sumB,
		"Tenant B sum should be 125000 cents (50000 + 75000)")
}

func TestLienRepository_TenantIsolation_CountActive_OnlyCountsTenantData(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, contexts, cleanup := setupMultiTenantLienTestDB(t, "count_tenant_x", "count_tenant_y")
	defer cleanup()

	repo := NewLienRepository(db)
	ctxX := contexts["count_tenant_x"]
	ctxY := contexts["count_tenant_y"]

	// Use the same account ID in both tenants
	sharedAccountID := uuid.New()
	amount, _ := domain.NewMoney("GBP", 10000)

	// Create 2 liens in tenant X
	lienX1, _ := domain.NewLien(sharedAccountID, amount, "", "PO-COUNT-X-001", nil)
	require.NoError(t, repo.Create(ctxX, lienX1))

	lienX2, _ := domain.NewLien(sharedAccountID, amount, "", "PO-COUNT-X-002", nil)
	require.NoError(t, repo.Create(ctxX, lienX2))

	// Create 5 liens in tenant Y
	for i := 1; i <= 5; i++ {
		lien, _ := domain.NewLien(sharedAccountID, amount, "", fmt.Sprintf("PO-COUNT-Y-%03d", i), nil)
		require.NoError(t, repo.Create(ctxY, lien))
	}

	// Count from tenant X should be 2
	countX, err := repo.CountActiveByAccountID(ctxX, sharedAccountID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), countX, "Tenant X should count 2 liens")

	// Count from tenant Y should be 5
	countY, err := repo.CountActiveByAccountID(ctxY, sharedAccountID)
	require.NoError(t, err)
	assert.Equal(t, int64(5), countY, "Tenant Y should count 5 liens")
}

func TestLienRepository_TenantIsolation_FindByPaymentOrderReference_CrossTenantNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, contexts, cleanup := setupMultiTenantLienTestDB(t, "po_owner", "po_stranger")
	defer cleanup()

	repo := NewLienRepository(db)
	ctxOwner := contexts["po_owner"]
	ctxStranger := contexts["po_stranger"]

	// Create lien with specific payment order reference in owner tenant
	accountID := uuid.New()
	amount, _ := domain.NewMoney("GBP", 10000)
	paymentOrderRef := "PO-UNIQUE-CROSS-TENANT-REF"

	lien, _ := domain.NewLien(accountID, amount, "", paymentOrderRef, nil)
	require.NoError(t, repo.Create(ctxOwner, lien))

	// Owner can find by payment order reference
	found, err := repo.FindByPaymentOrderReference(ctxOwner, paymentOrderRef)
	require.NoError(t, err)
	assert.Equal(t, lien.ID, found.ID)

	// Stranger tenant should not find it
	_, err = repo.FindByPaymentOrderReference(ctxStranger, paymentOrderRef)
	assert.ErrorIs(t, err, ErrLienNotFound,
		"FindByPaymentOrderReference from wrong tenant should return ErrLienNotFound")
}
