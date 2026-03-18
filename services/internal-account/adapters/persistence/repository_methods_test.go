package persistence

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Repository accessor methods
// ---------------------------------------------------------------------------

func TestRepository_DB(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	assert.NotNil(t, repo.DB())
	assert.Equal(t, db, repo.DB())
}

func TestRepository_WithTx(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	tx := db.Begin()
	defer tx.Rollback()

	txRepo := repo.WithTx(tx)
	require.NotNil(t, txRepo)
}

// ---------------------------------------------------------------------------
// SaveInTx
// ---------------------------------------------------------------------------

func TestRepository_SaveInTx_NewAccount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account := createTestAccount(t, "IBA-SAVINTX-001", "SAVINTX-001", "SaveInTx Account", domain.AccountTypeClearing)

	tx := db.Begin()
	defer tx.Rollback()

	err := repo.SaveInTx(ctx, account, tx)
	require.NoError(t, err)

	tx.Commit()

	// Verify account was saved
	found, err := repo.FindByCode(ctx, "SAVINTX-001")
	require.NoError(t, err)
	assert.Equal(t, "IBA-SAVINTX-001", found.AccountID())
}

// ---------------------------------------------------------------------------
// FindByIDForUpdate
// ---------------------------------------------------------------------------

func TestRepository_FindByIDForUpdate_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account := createTestAccount(t, "IBA-FORUPDATE-001", "FORUPDATE-001", "ForUpdate Account", domain.AccountTypeHolding)
	require.NoError(t, repo.Save(ctx, account))

	// Find by UUID using FindByID to get the actual UUID
	found, err := repo.FindByCode(ctx, "FORUPDATE-001")
	require.NoError(t, err)

	// Now use FindByIDForUpdate with the UUID
	locked, err := repo.FindByIDForUpdate(ctx, found.ID())
	require.NoError(t, err)
	assert.Equal(t, found.AccountCode(), locked.AccountCode())
}

func TestRepository_FindByIDForUpdate_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByIDForUpdate(ctx, uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// ---------------------------------------------------------------------------
// FindByAccountID
// ---------------------------------------------------------------------------

func TestRepository_FindByAccountID_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account := createTestAccount(t, "IBA-BYACCTID-001", "BYACCTID-001", "FindByAccountID Account", domain.AccountTypeRevenue)
	require.NoError(t, repo.Save(ctx, account))

	found, err := repo.FindByAccountID(ctx, "IBA-BYACCTID-001")
	require.NoError(t, err)
	assert.Equal(t, "BYACCTID-001", found.AccountCode())
}

func TestRepository_FindByAccountID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByAccountID(ctx, "NONEXISTENT-ACCT-ID")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// ---------------------------------------------------------------------------
// FindByOrganization
// ---------------------------------------------------------------------------

func TestRepository_FindByOrganization_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()

	// Create two accounts in the same org
	acc1 := createTestAccountWithOrg(t, "IBA-ORG-001", "ORG-ACC-001", "Org Account 1", domain.AccountTypeHolding, orgID)
	acc2 := createTestAccountWithOrg(t, "IBA-ORG-002", "ORG-ACC-002", "Org Account 2", domain.AccountTypeHolding, orgID)
	otherAcc := createTestAccount(t, "IBA-OTHER-001", "OTHER-ACC-001", "Other Account", domain.AccountTypeHolding)

	require.NoError(t, repo.Save(ctx, acc1))
	require.NoError(t, repo.Save(ctx, acc2))
	require.NoError(t, repo.Save(ctx, otherAcc))

	found, err := repo.FindByOrganization(ctx, orgID)
	require.NoError(t, err)
	assert.Len(t, found, 2)
}

func TestRepository_FindByOrganization_Empty(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	found, err := repo.FindByOrganization(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, found)
}

// ---------------------------------------------------------------------------
// isDuplicateKeyError (unit tested directly since it's a package-private function)
// ---------------------------------------------------------------------------

func TestIsDuplicateKeyError_Nil(t *testing.T) {
	assert.False(t, isDuplicateKeyError(nil))
}

func TestIsDuplicateKeyError_NonPgError(t *testing.T) {
	assert.False(t, isDuplicateKeyError(assert.AnError))
}

// ---------------------------------------------------------------------------
// withTenantTransaction when already in a transaction (isInTransaction path)
// ---------------------------------------------------------------------------

func TestRepository_SaveInTx_UsesExistingTransaction(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Start a transaction manually
	tx := db.Begin()
	txRepo := repo.WithTx(tx)

	account := createTestAccount(t, "IBA-TXREPO-001", "TXREPO-001", "Tx Repo Account", domain.AccountTypeSuspense)

	// Save within the transaction
	err := txRepo.Save(ctx, account)
	require.NoError(t, err)

	// Rollback — the account should not persist
	tx.Rollback()

	_, err = repo.FindByCode(ctx, "TXREPO-001")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// ---------------------------------------------------------------------------
// NewValuationFeatureRepository
// ---------------------------------------------------------------------------

func TestNewValuationFeatureRepository(t *testing.T) {
	repo := NewValuationFeatureRepository(nil)
	assert.NotNil(t, repo)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// createTestAccountWithOrg creates a test account associated with an organisation party.
func createTestAccountWithOrg(t *testing.T, accountID, accountCode, name string, accountType domain.AccountType, orgID uuid.UUID) domain.InternalAccount {
	t.Helper()
	account, err := domain.NewInternalAccount(
		accountID, accountCode, name, accountType,
		domain.ClearingPurposeUnspecified, "GBP", "",
		domain.WithOrgPartyID(orgID),
	)
	require.NoError(t, err)
	return account
}
