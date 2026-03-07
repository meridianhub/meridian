package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contractTestTenantID is a consistent tenant ID used across contract tests.
// Named differently to avoid collision with testTenantID in lien_repository_test.go.
var contractTestTenantID = tenant.MustNewTenantID("contract_test")

// Contract tests validate that repository operations work correctly with
// different values for account_id vs account_identification.
//
// These tests were added after discovering that FindByID was querying the
// wrong column (account_identification instead of account_id).

// TestFindByID_UsesAccountID validates that FindByID searches by account_id,
// not by account_identification. This is critical for deposit operations.
//
// Background: The ExecuteDeposit endpoint receives account_id (e.g., "ACC-12345678")
// and must look up the account using that value, not the IBAN.
func TestFindByID_UsesAccountID(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&CurrentAccountEntity{}})
	defer cleanup()

	repo := NewRepository(db)
	ctx := tenant.WithTenant(context.Background(), contractTestTenantID)

	// Create account with DISTINCT values for account_id and account_identification
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]     // Short ID like "ACC-12345678"
	accountIdentification := "GB82WEST12345698765432" // IBAN

	account, err := domain.NewCurrentAccountWithDimension(accountID, accountIdentification, partyID, "GBP", "CURRENCY", 2)
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err, "Save should succeed")

	// FindByID should find by account_id (ACC-xxx), NOT account_identification (IBAN)
	found, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err, "FindByID with account_id should succeed")
	assert.Equal(t, accountID, found.AccountID())
	assert.Equal(t, accountIdentification, found.ExternalIdentifier())

	// FindByID with IBAN should NOT find the account (it's not searching that column)
	_, err = repo.FindByID(ctx, accountIdentification)
	assert.Error(t, err, "FindByID with account_identification should NOT find the account")
}

// TestFindByIDForUpdate_UsesAccountID validates the FOR UPDATE variant also
// uses the correct column.
func TestFindByIDForUpdate_UsesAccountID(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&CurrentAccountEntity{}})
	defer cleanup()

	repo := NewRepository(db)
	ctx := tenant.WithTenant(context.Background(), contractTestTenantID)

	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	accountIdentification := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccountWithDimension(accountID, accountIdentification, partyID, "GBP", "CURRENCY", 2)
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// FindByIDForUpdate should find by account_id
	found, err := repo.FindByIDForUpdate(ctx, accountID)
	require.NoError(t, err, "FindByIDForUpdate with account_id should succeed")
	assert.Equal(t, accountID, found.AccountID())
}

// TestDelete_UsesAccountID validates Delete uses the correct column.
func TestDelete_UsesAccountID(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&CurrentAccountEntity{}})
	defer cleanup()

	repo := NewRepository(db)
	ctx := tenant.WithTenant(context.Background(), contractTestTenantID)

	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	accountIdentification := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccountWithDimension(accountID, accountIdentification, partyID, "GBP", "CURRENCY", 2)
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Delete by account_id should work
	err = repo.Delete(ctx, accountID)
	require.NoError(t, err, "Delete with account_id should succeed")

	// Verify it's deleted
	_, err = repo.FindByID(ctx, accountID)
	assert.ErrorIs(t, err, ErrAccountNotFound, "Account should be deleted")
}
