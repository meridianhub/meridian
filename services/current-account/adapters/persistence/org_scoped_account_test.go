package persistence

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const orgScopedTestTenantID = "org_scoped_test"

func setupOrgScopedTestDB(t *testing.T) (*gorm.DB, *Repository, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&CurrentAccountEntity{}})

	tid := tenant.TenantID(orgScopedTestTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the accounts table in tenant schema with org_party_id column
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

	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)
	repo := NewRepository(db)

	return db, repo, ctx, cleanup
}

func TestOrgScopedAccount_SaveWithOrgPartyID(t *testing.T) {
	db, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	partyID := uuid.New().String()
	orgPartyID := uuid.New()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP",
		domain.WithOrgPartyID(orgPartyID))
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Verify org_party_id was persisted via entity
	var entity CurrentAccountEntity
	err = db.Where("account_id = ?", accountID).First(&entity).Error
	require.NoError(t, err)
	require.NotNil(t, entity.OrgPartyID)
	assert.Equal(t, orgPartyID, *entity.OrgPartyID)
}

func TestOrgScopedAccount_RoundTripPreservesOrgPartyID(t *testing.T) {
	_, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	partyID := uuid.New().String()
	orgPartyID := uuid.New()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP",
		domain.WithOrgPartyID(orgPartyID))
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Retrieve and verify domain model has OrgPartyID
	retrieved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	assert.True(t, retrieved.IsScopedToOrganization())
	require.NotNil(t, retrieved.OrgPartyID())
	assert.Equal(t, orgPartyID, *retrieved.OrgPartyID())
}

func TestOrgScopedAccount_PersonalAccountHasNullOrgPartyID(t *testing.T) {
	_, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST98765432123456"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Retrieve and verify personal account has nil OrgPartyID
	retrieved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	assert.False(t, retrieved.IsScopedToOrganization())
	assert.Nil(t, retrieved.OrgPartyID())
}

func TestOrgScopedAccount_FindByScopedParty(t *testing.T) {
	_, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	partyID := uuid.New().String()
	orgPartyID := uuid.New()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP",
		domain.WithOrgPartyID(orgPartyID))
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Find by scoped party
	found, err := repo.FindByScopedParty(ctx, partyID, orgPartyID, "GBP")
	require.NoError(t, err)

	assert.Equal(t, accountID, found.AccountID())
	require.NotNil(t, found.OrgPartyID())
	assert.Equal(t, orgPartyID, *found.OrgPartyID())
}

func TestOrgScopedAccount_FindByScopedParty_NotFound(t *testing.T) {
	_, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	// Search for non-existent scoped account
	_, err := repo.FindByScopedParty(ctx, uuid.New().String(), uuid.New(), "GBP")
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

func TestOrgScopedAccount_FindByScopedParty_WrongCurrency(t *testing.T) {
	_, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	partyID := uuid.New().String()
	orgPartyID := uuid.New()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP",
		domain.WithOrgPartyID(orgPartyID))
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Search with wrong currency
	_, err = repo.FindByScopedParty(ctx, partyID, orgPartyID, "EUR")
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

func TestOrgScopedAccount_ListByOrganization(t *testing.T) {
	_, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	orgPartyID := uuid.New()

	// Create two accounts for different parties within the same org
	party1 := uuid.New().String()
	party2 := uuid.New().String()

	acc1, err := domain.NewCurrentAccount("ACC-"+uuid.New().String()[:8], "GB82WEST12345698765432", party1, "GBP",
		domain.WithOrgPartyID(orgPartyID))
	require.NoError(t, err)

	acc2, err := domain.NewCurrentAccount("ACC-"+uuid.New().String()[:8], "GB82WEST98765432123456", party2, "GBP",
		domain.WithOrgPartyID(orgPartyID))
	require.NoError(t, err)

	// Create a personal account (not org-scoped) - should not appear
	acc3, err := domain.NewCurrentAccount("ACC-"+uuid.New().String()[:8], "GB82WEST55555555555555", uuid.New().String(), "GBP")
	require.NoError(t, err)

	err = repo.Save(ctx, acc1)
	require.NoError(t, err)
	err = repo.Save(ctx, acc2)
	require.NoError(t, err)
	err = repo.Save(ctx, acc3)
	require.NoError(t, err)

	// List by organization
	accounts, err := repo.ListByOrganization(ctx, orgPartyID)
	require.NoError(t, err)

	assert.Len(t, accounts, 2, "Should return only the 2 org-scoped accounts")
	for _, acc := range accounts {
		assert.True(t, acc.IsScopedToOrganization())
		assert.Equal(t, orgPartyID, *acc.OrgPartyID())
	}
}

func TestOrgScopedAccount_ListByOrganization_Empty(t *testing.T) {
	_, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	// List by non-existent org
	accounts, err := repo.ListByOrganization(ctx, uuid.New())
	require.NoError(t, err)

	assert.Empty(t, accounts)
}

func TestOrgScopedAccount_ListByOrganization_ExcludesDeleted(t *testing.T) {
	_, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	orgPartyID := uuid.New()
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]

	account, err := domain.NewCurrentAccount(accountID, "GB82WEST12345698765432", partyID, "GBP",
		domain.WithOrgPartyID(orgPartyID))
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Soft delete
	err = repo.Delete(ctx, accountID)
	require.NoError(t, err)

	// Should not appear in list
	accounts, err := repo.ListByOrganization(ctx, orgPartyID)
	require.NoError(t, err)
	assert.Empty(t, accounts)
}

// Entity mapping unit tests

func TestToEntity_MapsOrgPartyID(t *testing.T) {
	orgPartyID := uuid.New()
	partyID := uuid.New().String()

	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", partyID, "GBP",
		domain.WithOrgPartyID(orgPartyID))
	require.NoError(t, err)

	entity, err := toEntity(context.Background(), account)
	require.NoError(t, err)

	require.NotNil(t, entity.OrgPartyID)
	assert.Equal(t, orgPartyID, *entity.OrgPartyID)
}

func TestToEntity_NilOrgPartyIDForPersonalAccount(t *testing.T) {
	partyID := uuid.New().String()

	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", partyID, "GBP")
	require.NoError(t, err)

	entity, err := toEntity(context.Background(), account)
	require.NoError(t, err)

	assert.Nil(t, entity.OrgPartyID)
}

func TestToDomain_MapsOrgPartyID(t *testing.T) {
	orgPartyID := uuid.New()
	entity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountID:             "ACC-001",
		AccountIdentification: "GB82WEST12345698765432",
		AccountType:           "current",
		PartyID:               uuid.New(),
		OrgPartyID:            &orgPartyID,
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Status:                "ACTIVE",
		StatusHistory:         StatusHistoryJSON{},
		Version:               1,
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}

	account, err := toDomain(entity)
	require.NoError(t, err)

	assert.True(t, account.IsScopedToOrganization())
	require.NotNil(t, account.OrgPartyID())
	assert.Equal(t, orgPartyID, *account.OrgPartyID())
}

func TestToDomain_NilOrgPartyIDForPersonalAccount(t *testing.T) {
	entity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountID:             "ACC-001",
		AccountIdentification: "GB82WEST12345698765432",
		AccountType:           "current",
		PartyID:               uuid.New(),
		OrgPartyID:            nil,
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Status:                "ACTIVE",
		StatusHistory:         StatusHistoryJSON{},
		Version:               1,
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}

	account, err := toDomain(entity)
	require.NoError(t, err)

	assert.False(t, account.IsScopedToOrganization())
	assert.Nil(t, account.OrgPartyID())
}
