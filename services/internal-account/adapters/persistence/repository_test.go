package persistence

import (
	"context"
	"fmt"
	"testing"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testTenantID = "test_tenant"

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&InternalAccountEntity{},
		&StatusHistoryEntity{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the internal_account table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.internal_account (
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
		clearing_purpose VARCHAR(32) NULL,
		org_party_id UUID NULL,
		product_type_code VARCHAR(100) NULL,
		product_type_version INTEGER NULL,
		instrument_code VARCHAR(32) NOT NULL,
		dimension VARCHAR(20) NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		counterparty_id VARCHAR(100),
		counterparty_name VARCHAR(255),
		counterparty_external_ref VARCHAR(255),
		attributes JSONB NOT NULL DEFAULT '{}',
		version BIGINT NOT NULL DEFAULT 1
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the status history table
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.internal_account_status_history (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		account_id VARCHAR(100) NOT NULL,
		from_status VARCHAR(20) NOT NULL,
		to_status VARCHAR(20) NOT NULL,
		reason TEXT,
		changed_by VARCHAR(100) NOT NULL,
		changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create index on account_code
	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_account_code ON %s.internal_account (account_code)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func createTestAccount(t *testing.T, accountID, accountCode, name string, accountType domain.AccountType) domain.InternalAccount {
	t.Helper()
	// CLEARING accounts require a specific purpose; use GENERAL for tests
	clearingPurpose := domain.ClearingPurposeUnspecified
	if accountType == domain.AccountTypeClearing {
		clearingPurpose = domain.ClearingPurposeGeneral
	}
	account, err := domain.NewInternalAccount(
		accountID,
		accountCode,
		name,
		accountType,
		clearingPurpose,
		"GBP",
		"CURRENCY",
	)
	require.NoError(t, err)
	return account
}

func createTestAccountWithClearingPurpose(t *testing.T, accountID, accountCode, name string, accountType domain.AccountType, clearingPurpose domain.ClearingPurpose) domain.InternalAccount {
	t.Helper()
	account, err := domain.NewInternalAccount(
		accountID,
		accountCode,
		name,
		accountType,
		clearingPurpose,
		"GBP",
		"CURRENCY",
	)
	require.NoError(t, err)
	return account
}

func TestSaveNewAccount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account := createTestAccount(t, "IBA-001", "GBP_CLEARING", "GBP Clearing Account", domain.AccountTypeClearing)

	err := repo.Save(ctx, account)
	require.NoError(t, err)

	// Verify account was saved
	retrieved, err := repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	assert.Equal(t, account.AccountID(), retrieved.AccountID())
	assert.Equal(t, account.AccountCode(), retrieved.AccountCode())
	assert.Equal(t, account.Name(), retrieved.Name())
	assert.Equal(t, domain.AccountStatusActive, retrieved.Status())
	assert.Equal(t, int64(1), retrieved.Version())
}

func TestSaveNewAccount_InitialVersion(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account := createTestAccount(t, "IBA-002", "USD_CLEARING", "USD Clearing Account", domain.AccountTypeClearing)

	err := repo.Save(ctx, account)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	assert.Equal(t, int64(1), retrieved.Version(), "New account should have version 1")
}

func TestFindByID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account := createTestAccount(t, "IBA-003", "EUR_CLEARING", "EUR Clearing Account", domain.AccountTypeClearing)

	err := repo.Save(ctx, account)
	require.NoError(t, err)

	// Find by UUID
	found, err := repo.FindByID(ctx, account.ID())
	require.NoError(t, err)
	assert.Equal(t, account.AccountID(), found.AccountID())
}

func TestFindByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

func TestFindByCode(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account := createTestAccount(t, "IBA-004", "CHF_CLEARING", "CHF Clearing Account", domain.AccountTypeClearing)

	err := repo.Save(ctx, account)
	require.NoError(t, err)

	// Find by code
	found, err := repo.FindByCode(ctx, "CHF_CLEARING")
	require.NoError(t, err)
	assert.Equal(t, account.AccountID(), found.AccountID())
}

func TestFindByCode_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByCode(ctx, "NONEXISTENT")
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

func TestListWithFilters(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create accounts of different types
	clearing := createTestAccount(t, "IBA-010", "GBP_CLR", "GBP Clearing", domain.AccountTypeClearing)
	holding := createTestAccount(t, "IBA-011", "GBP_HOLD", "GBP Holding", domain.AccountTypeHolding)
	suspense := createTestAccount(t, "IBA-012", "GBP_SUSP", "GBP Suspense", domain.AccountTypeSuspense)

	require.NoError(t, repo.Save(ctx, clearing))
	require.NoError(t, repo.Save(ctx, holding))
	require.NoError(t, repo.Save(ctx, suspense))

	// Filter by account type
	clearingType := domain.AccountTypeClearing
	filter := domain.ListFilter{AccountType: &clearingType}
	results, err := repo.List(ctx, filter)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "GBP_CLR", results[0].AccountCode())

	// Filter by instrument code
	instrumentCode := "GBP"
	filter = domain.ListFilter{InstrumentCode: &instrumentCode}
	results, err = repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestListWithClearingPurposeFilter(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create clearing accounts with different purposes
	depositAcc := createTestAccountWithClearingPurpose(t, "IBA-CP-001", "CLR_DEPOSIT", "Deposit Clearing", domain.AccountTypeClearing, domain.ClearingPurposeDeposit)
	withdrawalAcc := createTestAccountWithClearingPurpose(t, "IBA-CP-002", "CLR_WITHDRAWAL", "Withdrawal Clearing", domain.AccountTypeClearing, domain.ClearingPurposeWithdrawal)
	settlementAcc := createTestAccountWithClearingPurpose(t, "IBA-CP-003", "CLR_SETTLEMENT", "Settlement Clearing", domain.AccountTypeClearing, domain.ClearingPurposeSettlement)
	generalAcc := createTestAccountWithClearingPurpose(t, "IBA-CP-004", "CLR_GENERAL", "General Clearing", domain.AccountTypeClearing, domain.ClearingPurposeGeneral)

	require.NoError(t, repo.Save(ctx, depositAcc))
	require.NoError(t, repo.Save(ctx, withdrawalAcc))
	require.NoError(t, repo.Save(ctx, settlementAcc))
	require.NoError(t, repo.Save(ctx, generalAcc))

	// Filter by clearing purpose - DEPOSIT
	depositPurpose := domain.ClearingPurposeDeposit
	filter := domain.ListFilter{ClearingPurpose: &depositPurpose}
	results, err := repo.List(ctx, filter)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "CLR_DEPOSIT", results[0].AccountCode())
	assert.Equal(t, domain.ClearingPurposeDeposit, results[0].ClearingPurpose())

	// Filter by clearing purpose - SETTLEMENT
	settlementPurpose := domain.ClearingPurposeSettlement
	filter = domain.ListFilter{ClearingPurpose: &settlementPurpose}
	results, err = repo.List(ctx, filter)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "CLR_SETTLEMENT", results[0].AccountCode())

	// Combine filters: account type + clearing purpose
	clearingType := domain.AccountTypeClearing
	filter = domain.ListFilter{
		AccountType:     &clearingType,
		ClearingPurpose: &depositPurpose,
	}
	results, err = repo.List(ctx, filter)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "CLR_DEPOSIT", results[0].AccountCode())

	// No filter returns all accounts
	filter = domain.ListFilter{}
	results, err = repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, results, 4)
}

func TestOptimisticLocking(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account := createTestAccount(t, "IBA-020", "LOCK_TEST", "Lock Test Account", domain.AccountTypeClearing)

	// Save initial
	err := repo.Save(ctx, account)
	require.NoError(t, err)

	// Retrieve account
	retrieved1, err := repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	retrieved2, err := repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	// Suspend first copy (increments version to 2)
	suspended, err := retrieved1.Suspend("Testing")
	require.NoError(t, err)
	err = repo.Save(ctx, suspended)
	require.NoError(t, err)

	// Try to update second copy (still has version 1)
	// This should fail due to version mismatch
	suspended2, err := retrieved2.Suspend("Concurrent update")
	require.NoError(t, err)
	err = repo.Save(ctx, suspended2)
	assert.ErrorIs(t, err, ErrVersionConflict)
}

func TestTenantIsolation(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&InternalAccountEntity{},
		&StatusHistoryEntity{},
	})
	defer cleanup()

	// Create two tenant schemas
	tenant1 := tenant.TenantID("tenant_1")
	tenant2 := tenant.TenantID("tenant_2")

	for _, tid := range []tenant.TenantID{tenant1, tenant2} {
		schemaName := tid.SchemaName()
		err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
		require.NoError(t, err)

		err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.internal_account (
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
			clearing_purpose VARCHAR(32) NULL,
			org_party_id UUID NULL,
			product_type_code VARCHAR(100) NULL,
			product_type_version INTEGER NULL,
			instrument_code VARCHAR(32) NOT NULL,
			dimension VARCHAR(20) NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
			counterparty_id VARCHAR(100),
			counterparty_name VARCHAR(255),
			counterparty_external_ref VARCHAR(255),
			attributes JSONB NOT NULL DEFAULT '{}',
			version BIGINT NOT NULL DEFAULT 1
		)`, pq.QuoteIdentifier(schemaName))).Error
		require.NoError(t, err)
	}

	repo := NewRepository(db)

	// Create account in tenant 1
	ctx1 := tenant.WithTenant(context.Background(), tenant1)
	account1 := createTestAccount(t, "IBA-T1", "TENANT1_ACC", "Tenant 1 Account", domain.AccountTypeClearing)
	require.NoError(t, repo.Save(ctx1, account1))

	// Create account in tenant 2
	ctx2 := tenant.WithTenant(context.Background(), tenant2)
	account2 := createTestAccount(t, "IBA-T2", "TENANT2_ACC", "Tenant 2 Account", domain.AccountTypeClearing)
	require.NoError(t, repo.Save(ctx2, account2))

	// Verify tenant 1 can only see their account
	results1, err := repo.List(ctx1, domain.ListFilter{})
	require.NoError(t, err)
	require.Len(t, results1, 1)
	assert.Equal(t, "TENANT1_ACC", results1[0].AccountCode())

	// Verify tenant 2 can only see their account
	results2, err := repo.List(ctx2, domain.ListFilter{})
	require.NoError(t, err)
	require.Len(t, results2, 1)
	assert.Equal(t, "TENANT2_ACC", results2[0].AccountCode())
}

func TestStatusHistoryRecording(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Record a status change
	err := repo.RecordStatusChange(ctx, "IBA-030", "ACTIVE", "SUSPENDED", "Test suspension")
	require.NoError(t, err)

	// Verify the status history was recorded (query directly to check)
	var count int64
	err = db.Model(&StatusHistoryEntity{}).Where("account_id = ?", "IBA-030").Count(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestCounterpartyDetails(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create a NOSTRO account
	nostro, err := domain.NewInternalAccount(
		"IBA-040",
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
	err = repo.Save(ctx, nostro)
	require.NoError(t, err)

	// Retrieve and verify counterparty details
	retrieved, err := repo.FindByID(ctx, nostro.ID())
	require.NoError(t, err)

	require.NotNil(t, retrieved.Counterparty())
	assert.Equal(t, "CITI001", retrieved.Counterparty().CounterpartyID())
	assert.Equal(t, "Citibank NA", retrieved.Counterparty().CounterpartyName())
	assert.Equal(t, "12345678", retrieved.Counterparty().ExternalRef())
}

func TestJSONBAttributes(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create account with custom attributes using the builder
	account := domain.NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-050").
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

	err := repo.Save(ctx, account)
	require.NoError(t, err)

	// Retrieve and verify attributes
	retrieved, err := repo.FindByID(ctx, account.ID())
	require.NoError(t, err)

	attrs := retrieved.Attributes()
	require.NotNil(t, attrs)
	assert.Equal(t, "CC001", attrs["cost_center"])
	assert.Equal(t, "Treasury", attrs["department"])
	assert.Equal(t, "EMEA", attrs["region"])
}

func TestRoundTripMapping(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create a fully populated VOSTRO account
	original, err := domain.NewInternalAccount(
		"IBA-060",
		"EUR_VOSTRO_DB",
		"EUR VOSTRO from Deutsche Bank",
		domain.AccountTypeVostro,
		domain.ClearingPurposeUnspecified,
		"EUR",
		"CURRENCY",
	)
	require.NoError(t, err)

	counterparty, err := domain.NewCounterpartyDetails("DB001", "Deutsche Bank AG", "DE89370400440532013000")
	require.NoError(t, err)
	original, err = original.UpdateCounterparty(counterparty)
	require.NoError(t, err)

	// Save
	err = repo.Save(ctx, original)
	require.NoError(t, err)

	// Retrieve
	retrieved, err := repo.FindByID(ctx, original.ID())
	require.NoError(t, err)

	// Verify all fields round-trip correctly
	assert.Equal(t, original.ID(), retrieved.ID())
	assert.Equal(t, original.AccountID(), retrieved.AccountID())
	assert.Equal(t, original.AccountCode(), retrieved.AccountCode())
	assert.Equal(t, original.Name(), retrieved.Name())
	assert.Equal(t, original.AccountType(), retrieved.AccountType())
	assert.Equal(t, original.InstrumentCode(), retrieved.InstrumentCode())
	assert.Equal(t, original.Dimension(), retrieved.Dimension())
	assert.Equal(t, original.Status(), retrieved.Status())
	assert.Equal(t, original.Version(), retrieved.Version())

	// Verify counterparty details
	require.NotNil(t, retrieved.Counterparty())
	assert.Equal(t, original.Counterparty().CounterpartyID(), retrieved.Counterparty().CounterpartyID())
	assert.Equal(t, original.Counterparty().CounterpartyName(), retrieved.Counterparty().CounterpartyName())
	assert.Equal(t, original.Counterparty().ExternalRef(), retrieved.Counterparty().ExternalRef())
}

func TestExistsByCode(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account := createTestAccount(t, "IBA-070", "EXISTS_TEST", "Exists Test Account", domain.AccountTypeClearing)

	// Should not exist before save
	exists, err := repo.ExistsByCode(ctx, "EXISTS_TEST")
	require.NoError(t, err)
	assert.False(t, exists)

	// Save
	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Should exist after save
	exists, err = repo.ExistsByCode(ctx, "EXISTS_TEST")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestDelete(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account := createTestAccount(t, "IBA-080", "DELETE_TEST", "Delete Test Account", domain.AccountTypeClearing)

	err := repo.Save(ctx, account)
	require.NoError(t, err)

	// Delete
	err = repo.Delete(ctx, account.ID())
	require.NoError(t, err)

	// Should not be found after delete
	_, err = repo.FindByID(ctx, account.ID())
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

func TestDelete_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	err := repo.Delete(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

func TestPing(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	err := repo.Ping()
	require.NoError(t, err)
}
