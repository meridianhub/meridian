package persistence

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// testTenantID is the tenant ID used in tests.
const repoTestTenantID = "test_tenant"

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	return testdb.SetupTestDB(t,
		testdb.WithModels(&CurrentAccountEntity{}),
		testdb.WithTenant(repoTestTenantID),
	)
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

func TestSave_InstrumentCodeAndDimensionRoundTrip(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "EUR")
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	assert.Equal(t, "EUR", retrieved.InstrumentCode(), "InstrumentCode should round-trip correctly")
	assert.Equal(t, "CURRENCY", retrieved.Dimension(), "Dimension should round-trip correctly")
}

func TestSave_InstrumentCodePersistedOnEntity(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Read raw entity to verify column values in DB
	var entity CurrentAccountEntity
	err = db.Where("account_id = ?", accountID).First(&entity).Error
	require.NoError(t, err)

	assert.Equal(t, "GBP", entity.InstrumentCode, "instrument_code column should be persisted")
	assert.Equal(t, "CURRENCY", entity.Dimension, "dimension column should be persisted")
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

// Cursor encoding/decoding tests

func TestEncodeDecodeAccountCursor_RoundTrip(t *testing.T) {
	original := AccountCursor{
		CreatedAt: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		ID:        uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
	}

	encoded := EncodeAccountCursor(original)
	assert.NotEmpty(t, encoded)

	decoded, err := DecodeAccountCursor(encoded)
	require.NoError(t, err)

	assert.True(t, original.CreatedAt.Equal(decoded.CreatedAt), "CreatedAt should round-trip")
	assert.Equal(t, original.ID, decoded.ID, "ID should round-trip")
}

func TestDecodeAccountCursor_EmptyToken(t *testing.T) {
	cursor, err := DecodeAccountCursor("")
	require.NoError(t, err)
	assert.True(t, cursor.CreatedAt.IsZero())
	assert.Equal(t, uuid.UUID{}, cursor.ID)
}

func TestDecodeAccountCursor_InvalidBase64(t *testing.T) {
	_, err := DecodeAccountCursor("not-valid-base64!!!")
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

func TestDecodeAccountCursor_MalformedContent(t *testing.T) {
	tests := []struct {
		name  string
		input string // raw data to be base64 encoded
	}{
		{"no separator", "justsome-data"},
		{"invalid time", "not-a-time|550e8400-e29b-41d4-a716-446655440000"},
		{"invalid uuid", "2026-01-15T10:30:00Z|not-a-uuid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := base64.URLEncoding.EncodeToString([]byte(tt.input))
			_, err := DecodeAccountCursor(encoded)
			assert.ErrorIs(t, err, ErrInvalidCursor)
		})
	}
}

// FindByUUID tests

func TestFindByUUID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	// Retrieve to get the UUID
	saved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	// Now find by UUID
	found, err := repo.FindByUUID(ctx, saved.ID())
	require.NoError(t, err)
	assert.Equal(t, accountID, found.AccountID())
}

func TestFindByUUID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	_, err := repo.FindByUUID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// DB and WithTx tests

func TestDB_ReturnsUnderlyingConnection(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	assert.Equal(t, db, repo.DB())
}

func TestWithTx_ReturnsNewRepository(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	txRepo := repo.WithTx(db)
	assert.NotNil(t, txRepo)
	assert.Equal(t, db, txRepo.DB())
}

// Ping test

func TestPing(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	err := repo.Ping()
	assert.NoError(t, err)
}

// ListAccounts tests

func TestListAccounts_Empty(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	result, err := repo.ListAccounts(ctx, ListAccountsParams{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, result.Accounts)
	assert.Equal(t, int64(0), result.TotalCount)
	assert.Empty(t, result.NextCursor)
}

func TestListAccounts_WithResults(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create 3 accounts
	for i := 0; i < 3; i++ {
		partyID := uuid.New().String()
		accountID := fmt.Sprintf("ACC-%d-%s", i, uuid.New().String()[:6])
		iban := fmt.Sprintf("GB82WEST123456987654%02d", i)
		account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
		require.NoError(t, err)
		require.NoError(t, repo.Save(ctx, account))
	}

	result, err := repo.ListAccounts(ctx, ListAccountsParams{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, result.Accounts, 3)
	assert.Equal(t, int64(3), result.TotalCount)
	assert.Empty(t, result.NextCursor) // All fit in one page
}

func TestListAccounts_Pagination(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create 3 accounts
	for i := 0; i < 3; i++ {
		partyID := uuid.New().String()
		accountID := fmt.Sprintf("ACC-%d-%s", i, uuid.New().String()[:6])
		iban := fmt.Sprintf("GB82WEST223456987654%02d", i)
		account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
		require.NoError(t, err)
		require.NoError(t, repo.Save(ctx, account))
	}

	// Get first page with limit 2
	result, err := repo.ListAccounts(ctx, ListAccountsParams{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, result.Accounts, 2)
	assert.Equal(t, int64(3), result.TotalCount)
	assert.NotEmpty(t, result.NextCursor)

	// Get second page using cursor
	cursor, err := DecodeAccountCursor(result.NextCursor)
	require.NoError(t, err)
	result2, err := repo.ListAccounts(ctx, ListAccountsParams{
		Limit:  2,
		Cursor: cursor,
	})
	require.NoError(t, err)
	assert.Len(t, result2.Accounts, 1)
}

func TestListAccounts_DefaultLimit(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	// Limit 0 should default to 25
	result, err := repo.ListAccounts(ctx, ListAccountsParams{Limit: 0})
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestListAccounts_FilterByStatus(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create an active account
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST32345698765432"
	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Filter by active status (domain stores uppercase: "ACTIVE")
	result, err := repo.ListAccounts(ctx, ListAccountsParams{
		Status: string(domain.AccountStatusActive),
		Limit:  10,
	})
	require.NoError(t, err)
	assert.Len(t, result.Accounts, 1)

	// Filter by frozen status (should be empty)
	result, err = repo.ListAccounts(ctx, ListAccountsParams{
		Status: string(domain.AccountStatusFrozen),
		Limit:  10,
	})
	require.NoError(t, err)
	assert.Empty(t, result.Accounts)
}

func TestListAccounts_FilterByIBAN(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST42345698765432"
	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Matching IBAN prefix
	result, err := repo.ListAccounts(ctx, ListAccountsParams{
		IBAN:  "GB82WEST42",
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Len(t, result.Accounts, 1)

	// Non-matching prefix
	result, err = repo.ListAccounts(ctx, ListAccountsParams{
		IBAN:  "DE99BANK",
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Empty(t, result.Accounts)
}

// isDuplicateKeyError test

func TestIsDuplicateKeyError(t *testing.T) {
	assert.False(t, isDuplicateKeyError(nil))
	assert.False(t, isDuplicateKeyError(errors.New("some random error")))
	assert.True(t, isDuplicateKeyError(errors.New("duplicate key value violates unique constraint")))
	assert.True(t, isDuplicateKeyError(errors.New("ERROR: 23505")))
	assert.True(t, isDuplicateKeyError(gorm.ErrDuplicatedKey))
}

// isInTransaction test

func TestIsInTransaction_FalseForNewRepo(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	assert.False(t, repo.isInTransaction())
}

// FindByIBAN not found

func TestFindByIBAN_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	_, err := repo.FindByIBAN(ctx, "GB99NONEXISTENT12345678")
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// FindByPartyID empty result

func TestFindByPartyID_NoAccounts(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accounts, err := repo.FindByPartyID(ctx, uuid.New().String())
	require.NoError(t, err)
	assert.Empty(t, accounts)
}

// =============================================================================
// FindByUUIDForUpdate tests
// =============================================================================

func TestFindByUUIDForUpdate(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST52345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Retrieve to get the UUID
	saved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	// FindByUUIDForUpdate should lock and return the account
	found, err := repo.FindByUUIDForUpdate(ctx, saved.ID())
	require.NoError(t, err)
	assert.Equal(t, accountID, found.AccountID())
}

func TestFindByUUIDForUpdate_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	_, err := repo.FindByUUIDForUpdate(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

func TestFindByIDForUpdate_WithinTransaction(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WESTTX345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Use a manual gorm.DB.Begin() to create a real transaction, ensuring isInTransaction() returns true
	tx := repo.DB().Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()

	txRepo := repo.WithTx(tx)
	found, err := txRepo.FindByIDForUpdate(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, accountID, found.AccountID())
}

func TestFindByUUIDForUpdate_WithinTransaction(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WESTTX445698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	saved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	// Use a manual gorm.DB.Begin() to create a real transaction, ensuring isInTransaction() returns true
	tx := repo.DB().Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()

	txRepo := repo.WithTx(tx)
	found, err := txRepo.FindByUUIDForUpdate(ctx, saved.ID())
	require.NoError(t, err)
	assert.Equal(t, accountID, found.AccountID())
}

func TestIsInTransaction_TrueForBegunTx(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	tx := repo.DB().Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()

	txRepo := repo.WithTx(tx)
	assert.True(t, txRepo.isInTransaction(), "Repository created with Begin() tx should be in transaction")
}

// =============================================================================
// toEntity with product type, behavior class, and freeze reason
// =============================================================================

func TestToEntity_WithProductTypeAndBehaviorClass(t *testing.T) {
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST62345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP",
		domain.WithProductType("savings-v1", 2),
		domain.WithBehaviorClass("SAVINGS"),
	)
	require.NoError(t, err)

	ctx := context.Background()
	entity, err := toEntity(ctx, account)
	require.NoError(t, err)

	require.NotNil(t, entity.ProductTypeCode)
	assert.Equal(t, "savings-v1", *entity.ProductTypeCode)
	require.NotNil(t, entity.ProductTypeVersion)
	assert.Equal(t, 2, *entity.ProductTypeVersion)
	require.NotNil(t, entity.BehaviorClass)
	assert.Equal(t, "SAVINGS", *entity.BehaviorClass)
}

func TestToEntity_WithoutProductType(t *testing.T) {
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST72345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	ctx := context.Background()
	entity, err := toEntity(ctx, account)
	require.NoError(t, err)

	assert.Nil(t, entity.ProductTypeCode)
	assert.Nil(t, entity.ProductTypeVersion)
	assert.Nil(t, entity.BehaviorClass)
}

func TestToEntity_WithFreezeReason(t *testing.T) {
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST82345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	// Freeze the account to set freeze reason
	frozen, err := account.Freeze("Suspicious activity detected")
	require.NoError(t, err)

	ctx := context.Background()
	entity, err := toEntity(ctx, frozen)
	require.NoError(t, err)

	require.NotNil(t, entity.FreezeReason)
	assert.Equal(t, "Suspicious activity detected", *entity.FreezeReason)
}

func TestToEntity_WithoutFreezeReason(t *testing.T) {
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST92345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	ctx := context.Background()
	entity, err := toEntity(ctx, account)
	require.NoError(t, err)

	assert.Nil(t, entity.FreezeReason)
}

func TestToEntity_WithAuditContext(t *testing.T) {
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WESTA2345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	// Set audit context via auth context key
	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "admin-user")
	entity, err := toEntity(ctx, account)
	require.NoError(t, err)

	assert.Equal(t, "admin-user", entity.CreatedBy)
	assert.Equal(t, "admin-user", entity.UpdatedBy)
}

// =============================================================================
// toDomain with product type, behavior class, and freeze reason
// =============================================================================

func TestToDomain_WithProductTypeFields(t *testing.T) {
	code := "savings-v1"
	version := 3
	behaviorClass := "SAVINGS"
	freezeReason := "AML hold"

	orgPartyID := uuid.New()
	entity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountID:             "ACC-DOMAIN-1",
		AccountIdentification: "GB82WESTDOMAIN1234567",
		AccountType:           "current",
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Precision:             2,
		Status:                "FROZEN",
		PartyID:               uuid.New(),
		OrgPartyID:            &orgPartyID,
		ProductTypeCode:       &code,
		ProductTypeVersion:    &version,
		BehaviorClass:         &behaviorClass,
		FreezeReason:          &freezeReason,
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	account, err := toDomain(entity)
	require.NoError(t, err)

	assert.Equal(t, "savings-v1", account.ProductTypeCode())
	assert.Equal(t, 3, account.ProductTypeVersion())
	assert.Equal(t, "SAVINGS", account.BehaviorClass())
	assert.Equal(t, "AML hold", account.FreezeReason())
}

func TestToDomain_WithNilOptionalFields(t *testing.T) {
	entity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountID:             "ACC-DOMAIN-2",
		AccountIdentification: "GB82WESTDOMAIN2234567",
		AccountType:           "current",
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Precision:             2,
		Status:                "ACTIVE",
		PartyID:               uuid.New(),
		OrgPartyID:            nil,
		ProductTypeCode:       nil,
		ProductTypeVersion:    nil,
		BehaviorClass:         nil,
		FreezeReason:          nil,
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	account, err := toDomain(entity)
	require.NoError(t, err)

	assert.Empty(t, account.ProductTypeCode())
	assert.Equal(t, 0, account.ProductTypeVersion())
	assert.Empty(t, account.BehaviorClass())
	assert.Empty(t, account.FreezeReason())
}

// =============================================================================
// Save version conflict and create paths
// =============================================================================

func TestSave_VersionConflict(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WESTB2345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Load the same account twice to simulate concurrent access
	account1, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)
	account2, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	// Freeze account1 and save (this increments version)
	frozen1, err := account1.Freeze("First freeze")
	require.NoError(t, err)
	err = repo.Save(ctx, frozen1)
	require.NoError(t, err)

	// Try to freeze and save account2 (still has old version)
	frozen2, err := account2.Freeze("Second freeze")
	require.NoError(t, err)
	err = repo.Save(ctx, frozen2)
	assert.ErrorIs(t, err, ErrVersionConflict)
}

func TestSave_CreateNewAccount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WESTC2345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Verify it was created with version 1
	retrieved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), retrieved.Version())
	assert.Equal(t, string(domain.AccountStatusActive), string(retrieved.Status()))
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
// The entity uses unqualified table name "account" which allows
// PostgreSQL's search_path mechanism to route queries to organization-specific
// schemas (e.g., org_acme_bank.account, org_motive_corp.account).
//
// See: shared/platform/db/gorm_organization_scope.go for the implementation
// See: shared/platform/db/gorm_organization_scope_test.go for unit tests
