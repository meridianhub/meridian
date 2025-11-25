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

func setupLienTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	return testdb.SetupPostgres(t, []interface{}{&LienEntity{}})
}

func TestLienRepository_Create(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "PO-001", nil)
	require.NoError(t, err)

	err = repo.Create(lien)
	require.NoError(t, err)

	// Verify lien was saved
	retrieved, err := repo.FindByID(lien.ID)
	require.NoError(t, err)

	assert.Equal(t, lien.ID, retrieved.ID)
	assert.Equal(t, accountID, retrieved.AccountID)
	assert.Equal(t, int64(10000), retrieved.Amount.AmountCents())
	assert.Equal(t, "GBP", retrieved.Amount.Currency())
	assert.Equal(t, domain.LienStatusActive, retrieved.Status)
	assert.Equal(t, "PO-001", retrieved.PaymentOrderReference)
}

func TestLienRepository_FindByID_NotFound(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	_, err := repo.FindByID(uuid.New())
	assert.ErrorIs(t, err, ErrLienNotFound)
}

func TestLienRepository_FindByAccountID(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create two liens for same account
	lien1, err := domain.NewLien(accountID, amount, "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(lien1))

	lien2, err := domain.NewLien(accountID, amount, "PO-002", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(lien2))

	// Create lien for different account
	otherAccountID := uuid.New()
	lien3, err := domain.NewLien(otherAccountID, amount, "PO-003", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(lien3))

	liens, err := repo.FindByAccountID(accountID)
	require.NoError(t, err)

	assert.Len(t, liens, 2)
}

func TestLienRepository_FindActiveByAccountID(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create active lien
	activeLien, err := domain.NewLien(accountID, amount, "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(activeLien))

	// Create and execute a lien
	executedLien, err := domain.NewLien(accountID, amount, "PO-002", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(executedLien))
	require.NoError(t, executedLien.Execute())
	require.NoError(t, repo.Update(executedLien))

	// Create and terminate a lien
	terminatedLien, err := domain.NewLien(accountID, amount, "PO-003", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(terminatedLien))
	require.NoError(t, terminatedLien.Terminate("Cancelled"))
	require.NoError(t, repo.Update(terminatedLien))

	// Only active lien should be returned
	liens, err := repo.FindActiveByAccountID(accountID)
	require.NoError(t, err)

	assert.Len(t, liens, 1)
	assert.Equal(t, activeLien.ID, liens[0].ID)
}

func TestLienRepository_FindByPaymentOrderReference(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "PO-UNIQUE-123", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(lien))

	retrieved, err := repo.FindByPaymentOrderReference("PO-UNIQUE-123")
	require.NoError(t, err)

	assert.Equal(t, lien.ID, retrieved.ID)
}

func TestLienRepository_FindByPaymentOrderReference_NotFound(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	_, err := repo.FindByPaymentOrderReference("PO-NONEXISTENT")
	assert.ErrorIs(t, err, ErrLienNotFound)
}

func TestLienRepository_Update_Execute(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(lien))

	// Execute the lien
	require.NoError(t, lien.Execute())
	require.NoError(t, repo.Update(lien))

	// Verify status was updated
	retrieved, err := repo.FindByID(lien.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.LienStatusExecuted, retrieved.Status)
	assert.Equal(t, 2, retrieved.Version)
}

func TestLienRepository_Update_Terminate(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(lien))

	// Terminate the lien
	require.NoError(t, lien.Terminate("Payment failed"))
	require.NoError(t, repo.Update(lien))

	// Verify status and reason were updated
	retrieved, err := repo.FindByID(lien.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.LienStatusTerminated, retrieved.Status)
	assert.Equal(t, "Payment failed", retrieved.TerminationReason)
	assert.Equal(t, 2, retrieved.Version)
}

func TestLienRepository_OptimisticLocking(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create initial lien
	lien, err := domain.NewLien(accountID, amount, "PO-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(lien))

	// Load same lien twice (simulating concurrent access)
	lien1, err := repo.FindByID(lien.ID)
	require.NoError(t, err)

	lien2, err := repo.FindByID(lien.ID)
	require.NoError(t, err)

	// First update succeeds
	require.NoError(t, lien1.Execute())
	require.NoError(t, repo.Update(lien1))

	// Second update fails due to version conflict
	require.NoError(t, lien2.Terminate("Should fail"))
	err = repo.Update(lien2)
	assert.ErrorIs(t, err, ErrLienVersionConflict)

	// Verify first transaction's changes persisted
	final, err := repo.FindByID(lien.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.LienStatusExecuted, final.Status)
	assert.Equal(t, 2, final.Version)
}

func TestLienRepository_SumActiveAmountByAccountID(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Create active liens
	amount1, _ := domain.NewMoney("GBP", 20000) // £200
	lien1, _ := domain.NewLien(accountID, amount1, "PO-001", nil)
	require.NoError(t, repo.Create(lien1))

	amount2, _ := domain.NewMoney("GBP", 15000) // £150
	lien2, _ := domain.NewLien(accountID, amount2, "PO-002", nil)
	require.NoError(t, repo.Create(lien2))

	// Create and execute a lien (should not be counted)
	amount3, _ := domain.NewMoney("GBP", 10000)
	lien3, _ := domain.NewLien(accountID, amount3, "PO-003", nil)
	require.NoError(t, repo.Create(lien3))
	require.NoError(t, lien3.Execute())
	require.NoError(t, repo.Update(lien3))

	// Sum should only include active liens
	total, err := repo.SumActiveAmountByAccountID(accountID)
	require.NoError(t, err)

	assert.Equal(t, int64(35000), total) // £200 + £150 = £350
}

func TestLienRepository_SumActiveAmountByAccountID_NoLiens(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	total, err := repo.SumActiveAmountByAccountID(accountID)
	require.NoError(t, err)

	assert.Equal(t, int64(0), total)
}

func TestLienRepository_CreateWithExpiration(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)
	expiresAt := time.Now().Add(24 * time.Hour)

	lien, err := domain.NewLien(accountID, amount, "PO-001", &expiresAt)
	require.NoError(t, err)
	require.NoError(t, repo.Create(lien))

	retrieved, err := repo.FindByID(lien.ID)
	require.NoError(t, err)

	require.NotNil(t, retrieved.ExpiresAt)
	assert.Equal(t, expiresAt.Unix(), retrieved.ExpiresAt.Unix())
}

// Defensive tests for toLienDomain error handling

func TestToLienDomain_InvalidCurrency_ReturnsError(t *testing.T) {
	entity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10000,
		Currency:              "", // Invalid: empty currency
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
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	// Manually insert corrupted data (empty currency)
	corruptedEntity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10000,
		Currency:              "", // Corrupted
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-CORRUPT",
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	_, err := repo.FindByID(corruptedEntity.ID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}

func TestLienRepository_FindByAccountID_PartialCorruption_ReturnsError(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Create valid lien
	validAmount, _ := domain.NewMoney("GBP", 10000)
	validLien, _ := domain.NewLien(accountID, validAmount, "PO-001", nil)
	require.NoError(t, repo.Create(validLien))

	// Manually insert corrupted lien for same account
	corruptedEntity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AmountCents:           5000,
		Currency:              "", // Corrupted
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-CORRUPT",
		Version:               1,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	_, err := repo.FindByAccountID(accountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}

func TestLienRepository_Update_NonExistent_ReturnsError(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	// Create a lien in memory but don't save it
	amount, _ := domain.NewMoney("GBP", 10000)
	lien, _ := domain.NewLien(uuid.New(), amount, "PO-001", nil)

	// Try to update non-existent lien
	err := repo.Update(lien)

	// Should fail with version conflict (no rows affected)
	assert.True(t, errors.Is(err, ErrLienVersionConflict))
}
