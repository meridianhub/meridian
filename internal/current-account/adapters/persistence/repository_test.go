package persistence

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	"github.com/meridianhub/meridian/internal/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	return testdb.SetupPostgres(t, &CurrentAccountEntity{})
}

func TestSaveNewAccount(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)

	err = repo.Save(account)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify account was saved
	retrieved, err := repo.FindByID("ACC-001")
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}

	if retrieved.AccountID != "ACC-001" {
		t.Errorf("Expected ACC-001, got %s", retrieved.AccountID)
	}
}

func TestSaveUpdateExisting(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)

	// Save initial
	if err := repo.Save(account); err != nil {
		t.Fatalf("Initial save failed: %v", err)
	}

	// Modify and save again
	depositMoney, _ := domain.NewMoney("GBP", 10000)
	err = account.Deposit(depositMoney)
	if err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	if err := repo.Save(account); err != nil {
		t.Fatalf("Update save failed: %v", err)
	}

	// Verify balance was updated
	retrieved, err := repo.FindByID("ACC-001")
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}

	if retrieved.Balance.AmountCents() != 10000 {
		t.Errorf("Expected balance 10000, got %d", retrieved.Balance.AmountCents())
	}

	// Version should be incremented
	if retrieved.Version != 2 {
		t.Errorf("Expected version 2, got %d", retrieved.Version)
	}
}

func TestFindByIDNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByID("ACC-NONEXISTENT")
	if !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("Expected ErrAccountNotFound, got %v", err)
	}
}

func TestFindByIBAN(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)

	if err := repo.Save(account); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	retrieved, err := repo.FindByIBAN("GB82WEST12345698765432")
	if err != nil {
		t.Fatalf("FindByIBAN failed: %v", err)
	}

	if retrieved.AccountID != "ACC-001" {
		t.Errorf("Expected ACC-001, got %s", retrieved.AccountID)
	}
}

func TestFindByCustomerID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create two accounts for same customer
	account1, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)
	account2, err := domain.NewCurrentAccount("ACC-002", "GB82WEST98765432123456", "CUST-001", "EUR")
	require.NoError(t, err)

	if err := repo.Save(account1); err != nil {
		t.Fatalf("Save account1 failed: %v", err)
	}
	if err := repo.Save(account2); err != nil {
		t.Fatalf("Save account2 failed: %v", err)
	}

	accounts, err := repo.FindByCustomerID("CUST-001")
	if err != nil {
		t.Fatalf("FindByCustomerID failed: %v", err)
	}

	if len(accounts) != 2 {
		t.Errorf("Expected 2 accounts, got %d", len(accounts))
	}
}

func TestDeleteAccount(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)

	if err := repo.Save(account); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Delete account
	if err := repo.Delete("ACC-001"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Should not be found after soft delete
	_, err = repo.FindByID("ACC-001")
	if !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("Expected ErrAccountNotFound after delete, got %v", err)
	}
}

func TestOptimisticLocking(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create initial account
	account1, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)
	if err := repo.Save(account1); err != nil {
		t.Fatalf("Initial save failed: %v", err)
	}

	// Load same account in two "transactions"
	account2, err := repo.FindByID("ACC-001")
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}

	account3, err := repo.FindByID("ACC-001")
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}

	// Both should have same version
	if account2.Version != account3.Version {
		t.Errorf("Expected same version, got %d and %d", account2.Version, account3.Version)
	}

	// First transaction modifies and saves successfully
	deposit1, _ := domain.NewMoney("GBP", 5000)
	if err := account2.Deposit(deposit1); err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	if err := repo.Save(account2); err != nil {
		t.Fatalf("First save failed: %v", err)
	}

	// Second transaction tries to save with stale version
	deposit2, _ := domain.NewMoney("GBP", 10000)
	if err := account3.Deposit(deposit2); err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	err = repo.Save(account3)
	if !errors.Is(err, ErrVersionConflict) {
		t.Errorf("Expected ErrVersionConflict, got %v", err)
	}

	// Verify first transaction's changes persisted
	final, err := repo.FindByID("ACC-001")
	if err != nil {
		t.Fatalf("Final FindByID failed: %v", err)
	}

	if final.Balance.AmountCents() != 5000 {
		t.Errorf("Expected balance 5000, got %d", final.Balance.AmountCents())
	}

	// Version should be incremented
	if final.Version != 2 {
		t.Errorf("Expected version 2, got %d", final.Version)
	}
}

// Defensive tests for toDomain error handling per ADR-008

func TestToDomain_InvalidCurrency_ReturnsError(t *testing.T) {
	// Test: Empty currency in database should return error, not silently create invalid Money
	entity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountID:             "ACC-001",
		AccountIdentification: "GB82WEST12345698765432",
		CustomerID:            "CUST-001",
		BalanceCents:          10000,
		AvailableBalanceCents: 10000,
		Currency:              "", // Invalid: empty currency
		Status:                "ACTIVE",
		OverdraftLimitCents:   0,
		OverdraftEnabled:      false,
		OverdraftRate:         0,
		BalanceUpdatedAt:      time.Now(),
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	_, err := toDomain(entity)

	assert.Error(t, err, "toDomain should fail with empty currency")
	assert.Contains(t, err.Error(), "balance", "Error should indicate which field failed")
	assert.Contains(t, err.Error(), "database", "Error should indicate DB corruption")
}

func TestFindByID_CorruptedData_ReturnsError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Manually insert corrupted data (empty currency) into database
	entity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountID:             "ACC-CORRUPT",
		AccountIdentification: "GB82WEST12345698765432",
		CustomerID:            "CUST-001",
		BalanceCents:          10000,
		AvailableBalanceCents: 10000,
		Currency:              "", // Corrupted: empty currency
		Status:                "ACTIVE",
		OverdraftLimitCents:   0,
		OverdraftEnabled:      false,
		OverdraftRate:         0,
		BalanceUpdatedAt:      time.Now(),
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	result := db.Create(entity)
	require.NoError(t, result.Error, "Setup: Should be able to insert corrupted data")

	// Now try to retrieve it - should fail gracefully
	_, err := repo.FindByID("ACC-CORRUPT")

	assert.Error(t, err, "FindByID should fail with corrupted currency")
	assert.Contains(t, err.Error(), "database", "Error should indicate DB corruption")
}

func TestFindByCustomerID_PartialCorruption_ReturnsError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Insert one valid account and one corrupted account for same customer
	validAccount, err := domain.NewCurrentAccount("ACC-VALID", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(validAccount))

	// Manually insert corrupted account
	corruptedEntity := &CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountID:             "ACC-CORRUPT",
		AccountIdentification: "GB82WEST99999999999999",
		CustomerID:            "CUST-001", // Same customer
		BalanceCents:          5000,
		AvailableBalanceCents: 5000,
		Currency:              "", // Corrupted
		Status:                "ACTIVE",
		OverdraftLimitCents:   0,
		OverdraftEnabled:      false,
		OverdraftRate:         0,
		BalanceUpdatedAt:      time.Now(),
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	// FindByCustomerID should fail on first corrupted record
	_, err = repo.FindByCustomerID("CUST-001")

	assert.Error(t, err, "FindByCustomerID should fail when any account is corrupted")
	assert.Contains(t, err.Error(), "database", "Error should indicate DB corruption")
}
