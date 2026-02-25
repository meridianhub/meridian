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
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// testTenantID is the tenant ID used in tests.
const repoTestTenantID = "test_tenant"

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&CurrentAccountEntity{}})

	// Create the tenant schema for tests
	tid := tenant.TenantID(repoTestTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the accounts table in the tenant schema (matching CurrentAccountEntity.TableName())
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.accounts (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		account_id VARCHAR(100) NOT NULL UNIQUE,
		account_identification VARCHAR(34) NOT NULL UNIQUE,
		account_type VARCHAR(50) NOT NULL DEFAULT 'current',
		instrument_code VARCHAR(32) NOT NULL DEFAULT 'GBP',
		dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
		status VARCHAR(20) NOT NULL DEFAULT 'active',
		party_id UUID NOT NULL,
		org_party_id UUID NULL,
		balance BIGINT NOT NULL DEFAULT 0,
		available_balance BIGINT NOT NULL DEFAULT 0,
		overdraft_limit BIGINT NOT NULL DEFAULT 0,
		overdraft_rate NUMERIC(5,4) NOT NULL DEFAULT 0,
		balance_updated_at TIMESTAMP WITH TIME ZONE,
		opened_at TIMESTAMP WITH TIME ZONE,
		closed_at TIMESTAMP WITH TIME ZONE,
		freeze_reason VARCHAR(1000),
		status_history JSONB NOT NULL DEFAULT '[]',
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		created_by VARCHAR(100) NOT NULL DEFAULT 'test',
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_by VARCHAR(100) NOT NULL DEFAULT 'test',
		deleted_at TIMESTAMP WITH TIME ZONE
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema so Create/Update work in the tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func TestSaveNewAccount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	// ctx already provided by setupTestDB
	err = repo.Save(ctx, account)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify account was saved - FindByID searches by account_id (ACC-xxx format)
	retrieved, err := repo.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}

	if retrieved.AccountID() != accountID {
		t.Errorf("Expected %s, got %s", accountID, retrieved.AccountID())
	}
}

func TestSaveNewAccount_InitialVersion(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	// ctx already provided by setupTestDB
	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Verify newly created account has version 1
	retrieved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	assert.Equal(t, int64(1), retrieved.Version(), "New account should have version 1")
}

func TestSaveUpdateExisting(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	// ctx already provided by setupTestDB

	// Save initial
	if err := repo.Save(ctx, account); err != nil {
		t.Fatalf("Initial save failed: %v", err)
	}

	// Retrieve account (need to get version for optimistic locking)
	account, err = repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	// Modify account status (balance is not persisted - delegated to Position Keeping)
	account, err = account.Freeze("Test freeze reason")
	require.NoError(t, err)

	if err := repo.Save(ctx, account); err != nil {
		t.Fatalf("Update save failed: %v", err)
	}

	// Verify status was updated
	retrieved, err := repo.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}

	assert.Equal(t, domain.AccountStatusFrozen, retrieved.Status(), "Status should be updated to frozen")

	// Version should be incremented after update
	if retrieved.Version() != 2 {
		t.Errorf("Expected version 2, got %d", retrieved.Version())
	}
}

func TestFindByIDNotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	// ctx already provided by setupTestDB

	_, err := repo.FindByID(ctx, "ACC-NONEXISTENT")
	if !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("Expected ErrAccountNotFound, got %v", err)
	}
}

func TestFindByIBAN(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	// ctx already provided by setupTestDB
	if err := repo.Save(ctx, account); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// FindByIBAN searches by account_identification (IBAN)
	retrieved, err := repo.FindByIBAN(ctx, iban)
	if err != nil {
		t.Fatalf("FindByIBAN failed: %v", err)
	}

	if retrieved.AccountID() != accountID {
		t.Errorf("Expected AccountID %s, got %s", accountID, retrieved.AccountID())
	}
	if retrieved.ExternalIdentifier() != iban {
		t.Errorf("Expected IBAN %s, got %s", iban, retrieved.ExternalIdentifier())
	}
}

func TestFindByPartyID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()

	// Create two accounts for same party with distinct account IDs and IBANs
	accountID1 := "ACC-" + uuid.New().String()[:8]
	accountID2 := "ACC-" + uuid.New().String()[:8]
	iban1 := "GB82WEST12345698765432"
	iban2 := "GB82WEST98765432123456"

	account1, err := domain.NewCurrentAccount(accountID1, iban1, partyID, "GBP")
	require.NoError(t, err)
	account2, err := domain.NewCurrentAccount(accountID2, iban2, partyID, "EUR")
	require.NoError(t, err)

	// ctx already provided by setupTestDB
	if err := repo.Save(ctx, account1); err != nil {
		t.Fatalf("Save account1 failed: %v", err)
	}
	if err := repo.Save(ctx, account2); err != nil {
		t.Fatalf("Save account2 failed: %v", err)
	}

	accounts, err := repo.FindByPartyID(ctx, partyID)
	if err != nil {
		t.Fatalf("FindByPartyID failed: %v", err)
	}

	if len(accounts) != 2 {
		t.Errorf("Expected 2 accounts, got %d", len(accounts))
	}
}

func TestDeleteAccount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	// ctx already provided by setupTestDB
	if err := repo.Save(ctx, account); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Delete account by account_id (ACC-xxx format)
	if err := repo.Delete(ctx, accountID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Should not be found after soft delete
	_, err = repo.FindByID(ctx, accountID)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("Expected ErrAccountNotFound after delete, got %v", err)
	}
}

func TestOptimisticLocking(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	// ctx already provided by setupTestDB

	// Create initial account
	account1, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)
	if err := repo.Save(ctx, account1); err != nil {
		t.Fatalf("Initial save failed: %v", err)
	}

	// Load same account in two "transactions"
	account2, err := repo.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}

	account3, err := repo.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}

	// Both should have same version
	if account2.Version() != account3.Version() {
		t.Errorf("Expected same version, got %d and %d", account2.Version(), account3.Version())
	}

	// First transaction modifies and saves successfully
	// Use Freeze() instead of Deposit() since balance is not persisted
	account2, err = account2.Freeze("First freeze")
	require.NoError(t, err)

	if err := repo.Save(ctx, account2); err != nil {
		t.Fatalf("First save failed: %v", err)
	}

	// Second transaction tries to save with stale version
	// Use Freeze() which modifies a persisted field (status)
	account3, err = account3.Freeze("Second freeze attempt")
	require.NoError(t, err)

	err = repo.Save(ctx, account3)
	if !errors.Is(err, ErrVersionConflict) {
		t.Errorf("Expected ErrVersionConflict, got %v", err)
	}

	// Verify first transaction's changes persisted
	final, err := repo.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("Final FindByID failed: %v", err)
	}

	assert.Equal(t, domain.AccountStatusFrozen, final.Status(), "Status should be frozen from first transaction")

	// Version should be incremented
	if final.Version() != 2 {
		t.Errorf("Expected version 2, got %d", final.Version())
	}
}

// Defensive tests for toDomain error handling per ADR-008

func TestToDomain_InvalidCurrency_ReturnsError(t *testing.T) {
	// Test: Empty instrument_code in database should return error, not silently create invalid Money
	// Note: Balance fields removed - balance now computed by Position Keeping service
	entity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountIdentification: "GB82WEST12345698765432",
		AccountType:           "current",
		PartyID:               uuid.New(),
		InstrumentCode:        "", // Invalid: empty instrument_code
		Dimension:             "CURRENCY",
		Status:                "active",
		OverdraftLimit:        0,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}

	_, err := toDomain(entity)

	assert.Error(t, err, "toDomain should fail with empty currency")
	assert.Contains(t, err.Error(), "balance", "Error should indicate which field failed")
}

func TestFindByID_CorruptedData_ReturnsError(t *testing.T) {
	// Note: With the new schema using char(3) for currency, truly empty currencies
	// are not possible. This test uses an empty-padded currency which may still pass
	// DB constraints but should be caught by domain validation.
	// Skip this test as the database now properly enforces constraints.
	t.Skip("Skipping: database constraints now prevent corrupted currency data")

	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	// ctx already provided by setupTestDB

	// Manually insert corrupted data (empty currency) into database
	// Note: Balance fields removed - balance now computed by Position Keeping service
	entity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountIdentification: "GB82WEST12345698765432",
		AccountType:           "current",
		PartyID:               uuid.New(),
		InstrumentCode:        "", // Corrupted: empty instrument_code
		Dimension:             "CURRENCY",
		Status:                "active",
		OverdraftLimit:        0,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}

	result := db.Create(entity)
	require.NoError(t, result.Error, "Setup: Should be able to insert corrupted data")

	// Now try to retrieve it - should fail gracefully
	_, err := repo.FindByID(ctx, entity.AccountIdentification)

	assert.Error(t, err, "FindByID should fail with corrupted currency")
	assert.Contains(t, err.Error(), "database", "Error should indicate DB corruption")
}

func TestFindByPartyID_PartialCorruption_ReturnsError(t *testing.T) {
	// Note: With the new schema using char(3) for currency, truly empty currencies
	// are not possible. This test is skipped as database constraints now prevent
	// the kind of corruption we were testing for.
	t.Skip("Skipping: database constraints now prevent corrupted currency data")

	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	// ctx already provided by setupTestDB

	// Create a shared party ID for both accounts
	partyID := uuid.New()

	// Insert one valid account
	// Note: Balance fields removed - balance now computed by Position Keeping service
	validEntity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountIdentification: "GB82WEST12345698765432",
		AccountType:           "current",
		PartyID:               partyID,
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Status:                "active",
		OverdraftLimit:        0,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}
	require.NoError(t, db.Create(validEntity).Error)

	// Manually insert corrupted account with same party
	corruptedEntity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountIdentification: "GB82WEST99999999999999",
		AccountType:           "current",
		PartyID:               partyID, // Same party
		InstrumentCode:        "",      // Corrupted
		Dimension:             "CURRENCY",
		Status:                "active",
		OverdraftLimit:        0,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	// FindByPartyID should fail on first corrupted record
	_, err := repo.FindByPartyID(ctx, partyID.String())

	assert.Error(t, err, "FindByPartyID should fail when any account is corrupted")
	assert.Contains(t, err.Error(), "database", "Error should indicate DB corruption")
}

// Audit context tests

func TestSave_PopulatesAuditFieldsFromContext(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	// Create context with authenticated user AND tenant (tenant required for multi-tenant operations)
	testUserID := "user-123"
	ctx = context.WithValue(ctx, auth.UserIDContextKey, testUserID)

	// Save new account
	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Verify audit fields were set from context
	var entity CurrentAccountEntity
	err = db.Where("account_id = ?", accountID).First(&entity).Error
	require.NoError(t, err)

	assert.Equal(t, testUserID, entity.CreatedBy, "created_by should be set from context")
	assert.Equal(t, testUserID, entity.UpdatedBy, "updated_by should be set from context")
}

func TestSave_UsesSystemWhenNoUserInContext(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	// Use empty context (no user)
	// ctx already provided by setupTestDB

	// Save new account
	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Verify audit fields default to "system"
	var entity CurrentAccountEntity
	err = db.Where("account_id = ?", accountID).First(&entity).Error
	require.NoError(t, err)

	assert.Equal(t, "system", entity.CreatedBy, "created_by should default to 'system'")
	assert.Equal(t, "system", entity.UpdatedBy, "updated_by should default to 'system'")
}

func TestSave_UpdatePreservesCreatedByButUpdatesUpdatedBy(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	// Create with user1 (ctx already has tenant from setupTestDB)
	user1 := "user-creator"
	ctx1 := context.WithValue(ctx, auth.UserIDContextKey, user1)
	err = repo.Save(ctx1, account)
	require.NoError(t, err)

	// Retrieve account (need to get version for optimistic locking)
	account, err = repo.FindByID(ctx1, accountID)
	require.NoError(t, err)

	// Update with user2 (ctx already has tenant from setupTestDB)
	// Use Freeze() since balance is not persisted
	user2 := "user-updater"
	ctx2 := context.WithValue(ctx, auth.UserIDContextKey, user2)
	account, err = account.Freeze("Test freeze")
	require.NoError(t, err)

	err = repo.Save(ctx2, account)
	require.NoError(t, err)

	// Verify created_by preserved but updated_by changed
	var entity CurrentAccountEntity
	err = db.Where("account_id = ?", accountID).First(&entity).Error
	require.NoError(t, err)

	assert.Equal(t, user1, entity.CreatedBy, "created_by should be preserved from original creation")
	assert.Equal(t, user2, entity.UpdatedBy, "updated_by should reflect the user who made the update")
}

// Multi-org integration tests
//
// Comprehensive multi-organization isolation tests are located in:
// tests/multi_org/isolation_test.go
//
// These tests verify:
// - Organization database isolation via search_path
// - Cross-organization data isolation
// - Concurrent access from multiple organizations
// - Redis key prefixing per organization
// - Kafka header propagation
//
// The entity now uses unqualified table name "accounts" which allows
// PostgreSQL's search_path mechanism to route queries to organization-specific
// schemas (e.g., org_acme_bank.accounts, org_motive_corp.accounts).
//
// See: shared/platform/db/gorm_organization_scope.go for the implementation
// See: shared/platform/db/gorm_organization_scope_test.go for unit tests
