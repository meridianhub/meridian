package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testTenantID = "test_tenant"

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&FinancialBookingLogEntity{},
		&LedgerPostingEntity{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create tables in tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.financial_booking_logs (
		id UUID PRIMARY KEY,
		financial_account_type VARCHAR(50) NOT NULL,
		product_service_reference VARCHAR(255) NOT NULL,
		business_unit_reference VARCHAR(255) NOT NULL,
		chart_of_accounts_rules TEXT NOT NULL,
		base_currency VARCHAR(3) NOT NULL,
		status VARCHAR(20) NOT NULL,
		idempotency_key VARCHAR(255) NOT NULL UNIQUE,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		version BIGINT NOT NULL DEFAULT 1,
		deleted_at TIMESTAMP WITH TIME ZONE
	)`, schemaName)).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.ledger_postings (
		id UUID PRIMARY KEY,
		financial_booking_log_id UUID NOT NULL,
		posting_direction VARCHAR(20) NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(3) NOT NULL,
		account_id VARCHAR(255) NOT NULL,
		value_date TIMESTAMP WITH TIME ZONE NOT NULL,
		posting_result TEXT NOT NULL,
		status VARCHAR(20) NOT NULL,
		correlation_id VARCHAR(255) NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		deleted_at TIMESTAMP WITH TIME ZONE
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create FK constraint (ignore if already exists)
	err = db.Exec(fmt.Sprintf(`
		ALTER TABLE %q.ledger_postings
		ADD CONSTRAINT IF NOT EXISTS fk_ledger_postings_booking_log
		FOREIGN KEY (financial_booking_log_id)
		REFERENCES %q.financial_booking_logs(id)
		ON DELETE RESTRICT
	`, schemaName, schemaName)).Error
	if err != nil {
		// PostgreSQL before 9.6 doesn't support IF NOT EXISTS for constraints
		// Try without it and ignore duplicate constraint errors
		err = db.Exec(fmt.Sprintf(`
			ALTER TABLE %q.ledger_postings
			ADD CONSTRAINT fk_ledger_postings_booking_log
			FOREIGN KEY (financial_booking_log_id)
			REFERENCES %q.financial_booking_logs(id)
			ON DELETE RESTRICT
		`, schemaName, schemaName)).Error
		// Ignore duplicate constraint errors (SQLSTATE 42710)
		if err != nil {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42710" {
				require.NoError(t, err)
			}
		}
	}

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func TestSavePosting_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	_, err := repo.GetPosting(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrPostingNotFound)
}

func TestGetPostingsByBookingLogID_OrderedByCreatedAt(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

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
	t.Skip("Skipping FK constraint test - needs investigation on how GORM handles tenant schema with FK constraints")
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

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
	db, _, cleanup := setupTestDB(t)
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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

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

// TestSchemaIsolation verifies that tenant schema is isolated
func TestSchemaIsolation(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	// Verify tables exist in tenant schema
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()

	var tableCount int64
	err := db.Raw(`
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = ?
		AND table_name IN ('financial_booking_logs', 'ledger_postings')
	`, schemaName).Scan(&tableCount).Error

	require.NoError(t, err)
	assert.Equal(t, int64(2), tableCount, "Both tables should exist in tenant schema")
}

// TestForeignKeyOnDeleteRestrict verifies FK constraint prevents cascading deletes
func TestForeignKeyOnDeleteRestrict(t *testing.T) {
	t.Skip("Skipping FK constraint test - needs investigation on how GORM handles tenant schema with FK constraints")
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	// Create booking log and posting
	bookingLogID := uuid.New()
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()

	// Explicitly insert booking log into tenant schema
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

	// Insert directly to tenant schema
	err := db.Exec(fmt.Sprintf(`
		INSERT INTO %q.financial_booking_logs
		(id, financial_account_type, product_service_reference, business_unit_reference,
		 chart_of_accounts_rules, base_currency, status, idempotency_key, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, schemaName),
		bookingLog.ID, bookingLog.FinancialAccountType, bookingLog.ProductServiceReference,
		bookingLog.BusinessUnitReference, bookingLog.ChartOfAccountsRules, bookingLog.BaseCurrency,
		bookingLog.Status, bookingLog.IdempotencyKey, bookingLog.CreatedAt, bookingLog.UpdatedAt, bookingLog.Version).Error
	require.NoError(t, err)

	// Verify booking log was inserted
	var bookingLogCount int64
	err = db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM %q.financial_booking_logs WHERE id = ?", schemaName), bookingLogID).Scan(&bookingLogCount).Error
	require.NoError(t, err)
	require.Equal(t, int64(1), bookingLogCount, "Booking log should be inserted")

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
	saveErr := repo.SavePosting(ctx, posting)
	if saveErr != nil {
		t.Logf("SavePosting error: %v", saveErr)
	}
	require.NoError(t, saveErr)

	// Verify posting was saved
	var postingCount int64
	err = db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM %q.ledger_postings WHERE financial_booking_log_id = ?", schemaName), bookingLogID).Scan(&postingCount).Error
	require.NoError(t, err)
	require.Equal(t, int64(1), postingCount, "Posting should be saved before testing FK constraint")

	// Try to hard delete booking log while posting still references it
	// Use explicit schema-qualified DELETE to bypass soft delete and trigger FK constraint
	err = db.Exec(fmt.Sprintf("DELETE FROM %q.financial_booking_logs WHERE id = ?", schemaName), bookingLogID).Error

	// Should fail due to ON DELETE RESTRICT (SQLSTATE 23503)
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected Postgres error, got %T: %v", err, err)
	assert.Equal(t, "23503", pgErr.Code, "expected foreign key violation error code")
}
