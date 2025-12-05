package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&FinancialBookingLogEntity{},
		&LedgerPostingEntity{},
	})

	// Manually create FK constraint since GORM AutoMigrate doesn't create them
	// This matches the Atlas migration FK constraint
	err := db.Exec(`
		ALTER TABLE financial_accounting.ledger_postings
		ADD CONSTRAINT fk_ledger_postings_booking_log
		FOREIGN KEY (financial_booking_log_id)
		REFERENCES financial_accounting.financial_booking_logs(id)
		ON DELETE RESTRICT
	`).Error
	if err != nil {
		t.Fatalf("Failed to create FK constraint: %v", err)
	}

	return db, cleanup
}

func TestSavePosting_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	ctx := context.Background()

	// Create a test booking log first (for FK constraint)
	bookingLogID := uuid.New()
	bookingLog := &FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          "test-key-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	// Create posting
	money, err := domain.NewMoney(decimal.NewFromFloat(100.50), "GBP")
	require.NoError(t, err)

	posting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                money,
		AccountID:             "ACC-001",
		ValueDate:             time.Now(),
		PostingResult:         "SUCCESS",
		Status:                domain.TransactionStatusPosted,
		CorrelationID:         "CORR-001",
		CreatedAt:             time.Now(),
	}

	// Save posting
	err = repo.SavePosting(ctx, posting)
	assert.NoError(t, err)

	// Verify posting was saved
	retrieved, err := repo.GetPosting(ctx, posting.ID)
	require.NoError(t, err)
	assert.Equal(t, posting.ID, retrieved.ID)
	assert.Equal(t, posting.AccountID, retrieved.AccountID)
	assert.Equal(t, int64(10050), retrieved.Amount.Amount().Mul(decimal.NewFromInt(100)).IntPart())
}

func TestSavePostingsInTransaction_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	ctx := context.Background()

	// Create booking log
	bookingLogID := uuid.New()
	bookingLog := &FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          "test-key-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	// Create two postings (debit and credit for double-entry)
	money, _ := domain.NewMoney(decimal.NewFromInt(100), "GBP")

	postings := []*domain.LedgerPosting{
		{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID,
			Direction:             domain.PostingDirectionDebit,
			Amount:                money,
			AccountID:             "ACC-001",
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPosted,
			CorrelationID:         "CORR-001",
			CreatedAt:             time.Now(),
		},
		{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID,
			Direction:             domain.PostingDirectionCredit,
			Amount:                money,
			AccountID:             "ACC-002",
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPosted,
			CorrelationID:         "CORR-001",
			CreatedAt:             time.Now(),
		},
	}

	// Save both postings in transaction
	err := repo.SavePostingsInTransaction(ctx, postings)
	assert.NoError(t, err)

	// Verify both postings were saved
	retrieved, err := repo.GetPostingsByBookingLogID(ctx, bookingLogID)
	require.NoError(t, err)
	assert.Len(t, retrieved, 2)
}

func TestSavePostingsInTransaction_RollbackOnError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	ctx := context.Background()

	bookingLogID := uuid.New()

	// Create posting with invalid fractional cents (should fail)
	moneyWithFraction, _ := domain.NewMoney(decimal.NewFromFloat(100.123), "GBP")

	postings := []*domain.LedgerPosting{
		{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID,
			Direction:             domain.PostingDirectionDebit,
			Amount:                moneyWithFraction,
			AccountID:             "ACC-001",
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPosted,
			CreatedAt:             time.Now(),
		},
	}

	// Transaction should fail due to fractional cents
	err := repo.SavePostingsInTransaction(ctx, postings)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFractionalCents)

	// Verify no postings were saved (rollback)
	var count int64
	db.Model(&LedgerPostingEntity{}).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestGetPosting_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	ctx := context.Background()

	_, err := repo.GetPosting(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrPostingNotFound)
}

func TestGetPostingsByBookingLogID_OrderedByCreatedAt(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	ctx := context.Background()

	// Create booking log
	bookingLogID := uuid.New()
	bookingLog := &FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          "test-key-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	// Create postings with different timestamps
	money, _ := domain.NewMoney(decimal.NewFromInt(100), "GBP")
	now := time.Now()

	posting1 := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                money,
		AccountID:             "ACC-001",
		ValueDate:             now,
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             now,
	}

	posting2 := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionCredit,
		Amount:                money,
		AccountID:             "ACC-002",
		ValueDate:             now,
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             now.Add(time.Second),
	}

	require.NoError(t, repo.SavePosting(ctx, posting1))
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	require.NoError(t, repo.SavePosting(ctx, posting2))

	// Retrieve postings - should be ordered by created_at
	postings, err := repo.GetPostingsByBookingLogID(ctx, bookingLogID)
	require.NoError(t, err)
	require.Len(t, postings, 2)

	// Verify order (first saved should be first in list)
	assert.Equal(t, posting1.ID, postings[0].ID)
	assert.Equal(t, posting2.ID, postings[1].ID)
}

// TestForeignKeyConstraint_ViolationPrevented tests that FK constraint prevents orphan postings
func TestForeignKeyConstraint_ViolationPrevented(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Try to create a posting without corresponding booking log (FK violation)
	money, _ := domain.NewMoney(decimal.NewFromInt(100), "GBP")
	posting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: uuid.New(), // Non-existent booking log ID
		Direction:             domain.PostingDirectionDebit,
		Amount:                money,
		AccountID:             "ACC-001",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             time.Now(),
	}

	repo := NewLedgerRepository(db)
	err := repo.SavePosting(ctx, posting)

	// Should fail due to FK constraint violation (SQLSTATE 23503)
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected Postgres error, got %T: %v", err, err)
	assert.Equal(t, "23503", pgErr.Code, "expected foreign key violation error code")
}

// TestIdempotencyKeyUniqueness verifies that duplicate idempotency keys are rejected
func TestIdempotencyKeyUniqueness(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	idempotencyKey := "unique-key-" + uuid.New().String()

	// Create first booking log with idempotency key
	bookingLog1 := &FinancialBookingLogEntity{
		ID:                      uuid.New(),
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          idempotencyKey,
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog1).Error)

	// Try to create second booking log with same idempotency key
	bookingLog2 := &FinancialBookingLogEntity{
		ID:                      uuid.New(),
		FinancialAccountType:    "CREDIT",
		ProductServiceReference: "PROD-002",
		BusinessUnitReference:   "BU-002",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "USD",
		Status:                  "ACTIVE",
		IdempotencyKey:          idempotencyKey, // Duplicate!
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}

	err := db.Create(bookingLog2).Error
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected Postgres error, got %T: %v", err, err)
	assert.Equal(t, "23505", pgErr.Code, "expected unique constraint violation error code")
}

// TestSoftDelete verifies soft delete functionality
func TestSoftDelete(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create booking log
	bookingLogID := uuid.New()
	bookingLog := &FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          "test-key-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	// Create posting
	money, _ := domain.NewMoney(decimal.NewFromInt(100), "GBP")
	posting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                money,
		AccountID:             "ACC-001",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             time.Now(),
	}

	repo := NewLedgerRepository(db)
	require.NoError(t, repo.SavePosting(ctx, posting))

	// Soft delete the posting
	now := time.Now()
	err := db.Model(&LedgerPostingEntity{}).
		Where("id = ?", posting.ID).
		Update("deleted_at", now).Error
	require.NoError(t, err)

	// Verify posting is hidden from normal queries (GORM auto-filters deleted_at IS NULL)
	_, err = repo.GetPosting(ctx, posting.ID)
	assert.ErrorIs(t, err, ErrPostingNotFound)

	// Verify posting still exists in database with Unscoped query
	var entity LedgerPostingEntity
	err = db.Unscoped().First(&entity, "id = ?", posting.ID).Error
	require.NoError(t, err)
	assert.True(t, entity.DeletedAt.Valid, "DeletedAt should be marked valid after soft delete")
	assert.False(t, entity.DeletedAt.Time.IsZero(), "DeletedAt timestamp should be set after soft delete")
}

// TestSchemaIsolation verifies that financial_accounting schema is isolated
func TestSchemaIsolation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Verify tables exist in financial_accounting schema
	var tableCount int64
	err := db.Raw(`
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = 'financial_accounting'
		AND table_name IN ('financial_booking_logs', 'ledger_postings')
	`).Scan(&tableCount).Error

	require.NoError(t, err)
	assert.Equal(t, int64(2), tableCount, "Both tables should exist in financial_accounting schema")

	// Verify indexes exist with correct naming convention
	var indexCount int64
	err = db.Raw(`
		SELECT COUNT(*)
		FROM pg_indexes
		WHERE schemaname = 'financial_accounting'
		AND tablename = 'ledger_postings'
		AND indexname LIKE 'idx_financial_accounting_ledger_postings_%'
	`).Scan(&indexCount).Error

	require.NoError(t, err)
	assert.Greater(t, indexCount, int64(0), "Indexes should follow naming convention")
}

// TestForeignKeyOnDeleteRestrict verifies FK constraint prevents cascading deletes
func TestForeignKeyOnDeleteRestrict(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create booking log and posting
	bookingLogID := uuid.New()
	bookingLog := &FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          "test-key-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	money, _ := domain.NewMoney(decimal.NewFromInt(100), "GBP")
	posting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                money,
		AccountID:             "ACC-001",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             time.Now(),
	}

	repo := NewLedgerRepository(db)
	require.NoError(t, repo.SavePosting(ctx, posting))

	// Try to hard delete booking log while posting still references it
	// Use Unscoped to bypass soft delete and trigger FK constraint
	err := db.Unscoped().Delete(&FinancialBookingLogEntity{}, "id = ?", bookingLogID).Error

	// Should fail due to ON DELETE RESTRICT (SQLSTATE 23503)
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected Postgres error, got %T: %v", err, err)
	assert.Equal(t, "23503", pgErr.Code, "expected foreign key violation error code")
}
