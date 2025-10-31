package persistence

import (
	"errors"
	"testing"

	"github.com/meridianhub/meridian/internal/current-account/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Run migrations
	if err := db.AutoMigrate(&CurrentAccountEntity{}); err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	return db, cleanup
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
