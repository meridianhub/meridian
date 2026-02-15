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
		currency CHAR(3) NOT NULL DEFAULT 'GBP',
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

func TestOrgScopedAccount_CanCreateWithOrgPartyID(t *testing.T) {
	db, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	partyID := uuid.New().String()
	orgPartyID := uuid.New()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Set org_party_id directly via raw SQL since the domain model doesn't expose it yet
	err = db.Exec("UPDATE account SET org_party_id = ? WHERE account_id = ?", orgPartyID, accountID).Error
	require.NoError(t, err)

	// Verify org_party_id was stored
	var entity CurrentAccountEntity
	err = db.Where("account_id = ?", accountID).First(&entity).Error
	require.NoError(t, err)
	require.NotNil(t, entity.OrgPartyID)
	assert.Equal(t, orgPartyID, *entity.OrgPartyID)
}

func TestOrgScopedAccount_PersonalAccountHasNullOrgPartyID(t *testing.T) {
	db, repo, ctx, cleanup := setupOrgScopedTestDB(t)
	defer cleanup()

	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST98765432123456"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Verify personal account has NULL org_party_id
	var entity CurrentAccountEntity
	err = db.Where("account_id = ?", accountID).First(&entity).Error
	require.NoError(t, err)
	assert.Nil(t, entity.OrgPartyID, "Personal account should have NULL org_party_id")
}
