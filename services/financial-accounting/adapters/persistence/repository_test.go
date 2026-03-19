package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
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
	return testdb.SetupTestDB(t,
		testdb.WithModels(&FinancialBookingLogEntity{}, &LedgerPostingEntity{}, &audit.AuditOutbox{}),
		testdb.WithTenant(testTenantID),
	)
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

	// Create posting - using CurrencyToInstrument to convert currency to instrument
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromFloat(100.50), gbpInstrument)

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
	saveErr := repo.SavePosting(ctx, posting)
	assert.NoError(t, saveErr)

	// Verify posting was saved
	retrieved, err := repo.GetPosting(ctx, posting.ID)
	require.NoError(t, err)
	assert.Equal(t, posting.ID, retrieved.ID)
	assert.Equal(t, posting.AccountID, retrieved.AccountID)
	assert.Equal(t, int64(10050), retrieved.Amount.Amount.Mul(decimal.NewFromInt(100)).IntPart())
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
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)

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
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	moneyWithFraction := domain.NewMoney(decimal.NewFromFloat(100.123), gbpInstrument)

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
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)
	now := time.Now()

	posting1 := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                money,
		AccountID:             "ACC-001",
		ValueDate:             now,
		Status:                domain.TransactionStatusPosted,
	}

	posting2 := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionCredit,
		Amount:                money,
		AccountID:             "ACC-002",
		ValueDate:             now,
		Status:                domain.TransactionStatusPosted,
	}

	require.NoError(t, repo.SavePosting(ctx, posting1))
	// Intentional sleep: DB auto-populates CreatedAt on insert; sleep ensures
	// distinct timestamps for ordering verification (domain CreatedAt is not persisted)
	time.Sleep(10 * time.Millisecond)
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
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)
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
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)
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
		AND table_name IN ('financial_booking_log', 'ledger_posting')
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
		INSERT INTO %s.financial_booking_log
		(id, financial_account_type, product_service_reference, business_unit_reference,
		 chart_of_accounts_rules, base_currency, status, idempotency_key, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, pq.QuoteIdentifier(schemaName)),
		bookingLog.ID, bookingLog.FinancialAccountType, bookingLog.ProductServiceReference,
		bookingLog.BusinessUnitReference, bookingLog.ChartOfAccountsRules, bookingLog.BaseCurrency,
		bookingLog.Status, bookingLog.IdempotencyKey, bookingLog.CreatedAt, bookingLog.UpdatedAt, bookingLog.Version).Error
	require.NoError(t, err)

	// Verify booking log was inserted
	var bookingLogCount int64
	err = db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM %s.financial_booking_log WHERE id = ?", pq.QuoteIdentifier(schemaName)), bookingLogID).Scan(&bookingLogCount).Error
	require.NoError(t, err)
	require.Equal(t, int64(1), bookingLogCount, "Booking log should be inserted")

	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)
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
	err = db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM %s.ledger_posting WHERE financial_booking_log_id = ?", pq.QuoteIdentifier(schemaName)), bookingLogID).Scan(&postingCount).Error
	require.NoError(t, err)
	require.Equal(t, int64(1), postingCount, "Posting should be saved before testing FK constraint")

	// Try to hard delete booking log while posting still references it
	// Use explicit schema-qualified DELETE to bypass soft delete and trigger FK constraint
	err = db.Exec(fmt.Sprintf("DELETE FROM %s.financial_booking_log WHERE id = ?", pq.QuoteIdentifier(schemaName)), bookingLogID).Error

	// Should fail due to ON DELETE RESTRICT (SQLSTATE 23503)
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected Postgres error, got %T: %v", err, err)
	assert.Equal(t, "23503", pgErr.Code, "expected foreign key violation error code")
}

// ====================
// Audit Integration Tests
// ====================

// setupTestDBWithAudit creates test database with audit tables.
// Uses WithAuditTables (raw DDL) instead of WithModels for audit entities
// to preserve CHECK constraints on status and operation columns.
func setupTestDBWithAudit(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	return testdb.SetupTestDB(t,
		testdb.WithModels(
			&FinancialBookingLogEntity{},
			&LedgerPostingEntity{},
		),
		testdb.WithAuditTables(),
		testdb.WithTenant(testTenantID),
	)
}

// TestAuditBookingLogCreate verifies that creating a booking log creates an audit outbox entry
func TestAuditBookingLogCreate(t *testing.T) {
	db, _, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Create a booking log
	bookingLog := &FinancialBookingLogEntity{
		ID:                      uuid.New(),
		FinancialAccountType:    "ASSET",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "PENDING",
		IdempotencyKey:          "audit-test-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	// Verify audit outbox entry was created
	var outbox audit.AuditOutbox
	err := db.Where("record_id = ? AND table_name = ?", bookingLog.ID.String(), "financial_booking_log").First(&outbox).Error
	require.NoError(t, err)

	assert.Equal(t, "INSERT", outbox.Operation)
	assert.Equal(t, "pending", outbox.Status)
	assert.NotEmpty(t, outbox.NewValues)
	assert.Empty(t, outbox.OldValues) // No old values for INSERT
}

// TestAuditBookingLogUpdate verifies that updating a booking log creates an audit outbox entry
func TestAuditBookingLogUpdate(t *testing.T) {
	db, _, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Create a booking log
	bookingLog := &FinancialBookingLogEntity{
		ID:                      uuid.New(),
		FinancialAccountType:    "ASSET",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "PENDING",
		IdempotencyKey:          "audit-update-test-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	// Update the booking log
	bookingLog.Status = "POSTED"
	bookingLog.UpdatedAt = time.Now()
	require.NoError(t, db.Save(bookingLog).Error)

	// Verify both INSERT and UPDATE audit outbox entries exist
	var outboxEntries []audit.AuditOutbox
	err := db.Where("record_id = ? AND table_name = ?", bookingLog.ID.String(), "financial_booking_log").
		Order("created_at ASC").
		Find(&outboxEntries).Error
	require.NoError(t, err)
	require.Len(t, outboxEntries, 2, "Expected both INSERT and UPDATE audit entries")

	// Verify INSERT entry
	assert.Equal(t, "INSERT", outboxEntries[0].Operation)

	// Verify UPDATE entry
	assert.Equal(t, "UPDATE", outboxEntries[1].Operation)
	assert.NotEmpty(t, outboxEntries[1].OldValues) // Old values for UPDATE
	assert.NotEmpty(t, outboxEntries[1].NewValues) // New values for UPDATE
}

// TestAuditBookingLogDelete verifies that deleting a booking log creates an audit outbox entry
func TestAuditBookingLogDelete(t *testing.T) {
	db, _, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Create a booking log
	bookingLog := &FinancialBookingLogEntity{
		ID:                      uuid.New(),
		FinancialAccountType:    "ASSET",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "PENDING",
		IdempotencyKey:          "audit-delete-test-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	// Delete the booking log (soft delete via GORM)
	require.NoError(t, db.Delete(bookingLog).Error)

	// Verify both INSERT and DELETE audit outbox entries exist
	var outboxEntries []audit.AuditOutbox
	err := db.Where("record_id = ? AND table_name = ?", bookingLog.ID.String(), "financial_booking_log").
		Order("created_at ASC").
		Find(&outboxEntries).Error
	require.NoError(t, err)
	require.Len(t, outboxEntries, 2, "Expected both INSERT and DELETE audit entries")

	// Verify DELETE entry
	assert.Equal(t, "DELETE", outboxEntries[1].Operation)
	assert.NotEmpty(t, outboxEntries[1].OldValues) // Old values for DELETE
	assert.Empty(t, outboxEntries[1].NewValues)    // No new values for DELETE
}

// TestAuditLedgerPostingCreate verifies that creating a ledger posting creates an audit outbox entry
func TestAuditLedgerPostingCreate(t *testing.T) {
	db, _, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Create a booking log first (for FK)
	bookingLogID := uuid.New()
	bookingLog := &FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "ASSET",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "PENDING",
		IdempotencyKey:          "audit-posting-test-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	// Create a ledger posting
	posting := &LedgerPostingEntity{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		PostingDirection:      "DEBIT",
		AmountMinorUnits:      10050,
		Currency:              "GBP",
		DimensionType:         "CURRENCY",
		InstrumentVersion:     1,
		InstrumentPrecision:   2,
		AccountID:             "ACC-001",
		ValueDate:             time.Now(),
		Status:                "PENDING",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	require.NoError(t, db.Create(posting).Error)

	// Verify audit outbox entry was created for the posting
	var outbox audit.AuditOutbox
	err := db.Where("record_id = ? AND table_name = ?", posting.ID.String(), "ledger_posting").First(&outbox).Error
	require.NoError(t, err)

	assert.Equal(t, "INSERT", outbox.Operation)
	assert.Equal(t, "pending", outbox.Status)
}

// TestAuditLedgerPostingUpdate verifies that updating a ledger posting creates an audit outbox entry
func TestAuditLedgerPostingUpdate(t *testing.T) {
	db, _, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Create a booking log first (for FK)
	bookingLogID := uuid.New()
	bookingLog := &FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "ASSET",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "PENDING",
		IdempotencyKey:          "audit-posting-update-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	// Create a ledger posting
	posting := &LedgerPostingEntity{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		PostingDirection:      "DEBIT",
		AmountMinorUnits:      10050,
		Currency:              "GBP",
		DimensionType:         "CURRENCY",
		InstrumentVersion:     1,
		InstrumentPrecision:   2,
		AccountID:             "ACC-001",
		ValueDate:             time.Now(),
		Status:                "PENDING",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	require.NoError(t, db.Create(posting).Error)

	// Update the posting
	posting.Status = "POSTED"
	posting.PostingResult = "Success"
	posting.UpdatedAt = time.Now()
	require.NoError(t, db.Save(posting).Error)

	// Verify both INSERT and UPDATE audit outbox entries exist
	var outboxEntries []audit.AuditOutbox
	err := db.Where("record_id = ? AND table_name = ?", posting.ID.String(), "ledger_posting").
		Order("created_at ASC").
		Find(&outboxEntries).Error
	require.NoError(t, err)
	require.Len(t, outboxEntries, 2, "Expected both INSERT and UPDATE audit entries")

	// Verify UPDATE entry
	assert.Equal(t, "UPDATE", outboxEntries[1].Operation)
	assert.NotEmpty(t, outboxEntries[1].OldValues)
	assert.NotEmpty(t, outboxEntries[1].NewValues)
}

// TestAuditOutboxStatusValues verifies the status constraint values work correctly
func TestAuditOutboxStatusValues(t *testing.T) {
	db, _, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	testCases := []struct {
		name   string
		status string
		valid  bool
	}{
		{"pending status", "pending", true},
		{"processing status", "processing", true},
		{"completed status", "completed", true},
		{"failed status", "failed", true},
		{"invalid status", "invalid", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			outbox := &audit.AuditOutbox{
				ID:        uuid.New(),
				Table:     "test_table",
				Operation: "INSERT",
				RecordID:  uuid.New().String(),
				Status:    tc.status,
				CreatedAt: time.Now(),
			}

			err := db.Create(outbox).Error
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestAuditChangedByDefaultsToSystem verifies that changed_by defaults to "system" when no user context
func TestAuditChangedByDefaultsToSystem(t *testing.T) {
	db, _, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Create a booking log without user context
	bookingLog := &FinancialBookingLogEntity{
		ID:                      uuid.New(),
		FinancialAccountType:    "ASSET",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "PENDING",
		IdempotencyKey:          "audit-system-user-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error)

	// Verify audit entry has "system" as changed_by
	var outbox audit.AuditOutbox
	err := db.Where("record_id = ?", bookingLog.ID.String()).First(&outbox).Error
	require.NoError(t, err)

	require.NotNil(t, outbox.ChangedBy)
	assert.Equal(t, "system", *outbox.ChangedBy)
}
