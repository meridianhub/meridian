// Package testdb provides utilities for setting up test databases.
package testdb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	capersistence "github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	fapersistence "github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	popersistence "github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
)

// ErrProjectRootNotFound is returned when the project root cannot be determined.
var ErrProjectRootNotFound = errors.New("could not find project root (no go.mod found)")

// findProjectRoot traverses up from the current directory to find the project root
// (identified by the presence of go.mod file)
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrProjectRootNotFound
		}
		dir = parent
	}
}

// TestMigrationsMatchEntities validates that SQL migrations produce a schema
// compatible with GORM entities. This prevents drift between:
// - shared/domain/models/* (used by Atlas to generate migrations)
// - services/*/adapters/persistence/*_entity.go (used by application code)
//
// Why this matters (ref: GitHub Issue #202):
// - Unit tests use AutoMigrate which creates schema from entities
// - Production uses SQL migrations from Atlas
// - If these drift, tests pass but production fails
func TestMigrationsMatchEntities(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping schema validation test in short mode")
	}

	// Create a PostgreSQL container
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("schema_test"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")
	defer func() {
		if err := pgContainer.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}()

	// Get connection string for raw SQL (migrations)
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Connect with database/sql for running migrations
	sqlDB, err := sql.Open("pgx", connStr)
	require.NoError(t, err, "Failed to connect with pgx")
	defer sqlDB.Close()

	// Run the actual migrations (not AutoMigrate!)
	t.Run("ApplyMigrations", func(t *testing.T) {
		applyMigrations(ctx, t, sqlDB)
	})

	// Connect with GORM for entity operations
	gormDB, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "Failed to connect with GORM")

	// Now test that each entity can operate against the migrated schema
	// These will fail if columns are missing or misnamed
	//
	// With database-per-service architecture, all entities use unqualified table names
	// in the default public schema. No search_path manipulation needed.

	t.Run("CurrentAccount/AccountEntity", func(t *testing.T) {
		testCurrentAccountEntity(t, gormDB)
	})

	t.Run("CurrentAccount/LienEntity", func(t *testing.T) {
		testLienEntity(t, gormDB)
	})

	t.Run("CurrentAccount/WithdrawalEntity", func(t *testing.T) {
		testWithdrawalEntity(t, gormDB)
	})

	t.Run("PaymentOrder/PaymentOrderEntity", func(t *testing.T) {
		testPaymentOrderEntity(t, gormDB)
	})

	t.Run("FinancialAccounting/BookingLogEntity", func(t *testing.T) {
		testFinancialBookingLogEntity(t, gormDB)
	})

	t.Run("FinancialAccounting/LedgerPostingEntity", func(t *testing.T) {
		testLedgerPostingEntity(t, gormDB)
	})

	t.Run("ControlPlane/ManifestVersionEntity", func(t *testing.T) {
		testManifestVersionEntity(t, gormDB)
	})
}

// migrationFile represents a migration with its schema and filename
type migrationFile struct {
	schema   string
	filename string
	fullPath string
}

// applyMigrations runs all SQL migration files against the database in global timestamp order.
// This ensures cross-schema FK constraints are created/updated in the correct order,
// matching how migrations would be applied in production.
func applyMigrations(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()

	projectRoot, err := findProjectRoot()
	require.NoError(t, err, "Failed to find project root")

	// Services to include in migration (maps service dir name to schema name)
	services := map[string]string{
		"control-plane":        "control_plane",
		"current-account":      "current_account",
		"position-keeping":     "position_keeping",
		"payment-order":        "payment_order",
		"financial-accounting": "financial_accounting",
	}

	// Collect all migration files from all services
	var allMigrations []migrationFile
	for serviceDir, schema := range services {
		migrationDir := filepath.Join(projectRoot, "services", serviceDir, "migrations")

		entries, err := os.ReadDir(migrationDir)
		if err != nil {
			// Service might not have migrations yet
			t.Logf("No migrations found for service %s: %v", serviceDir, err)
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
				allMigrations = append(allMigrations, migrationFile{
					schema:   schema,
					filename: entry.Name(),
					fullPath: filepath.Join(migrationDir, entry.Name()),
				})
			}
		}
	}

	// Sort all migrations by filename (timestamp) globally across all schemas
	// This ensures correct ordering for cross-schema FK constraints
	sort.Slice(allMigrations, func(i, j int) bool {
		return allMigrations[i].filename < allMigrations[j].filename
	})

	// Execute each migration in global timestamp order
	for _, mig := range allMigrations {
		content, err := os.ReadFile(mig.fullPath)
		require.NoError(t, err, "Failed to read migration %s", mig.fullPath)

		_, err = db.ExecContext(ctx, string(content))
		require.NoError(t, err, "Failed to apply migration %s: SQL error", mig.fullPath)

		t.Logf("Applied migration: [%s] %s", mig.schema, mig.filename)
	}
}

// testLienEntity tests that LienEntity works with the migrated schema
func testLienEntity(t *testing.T, db *gorm.DB) {
	t.Helper()

	// Create an account (liens have FK to accounts)
	// Note: party_id is a reference to Party Service (no FK constraint)
	accountID := uuid.New()
	partyID := uuid.New() // References a party in Party Service (no local FK)
	now := time.Now()
	// Note: Balance fields removed - balance now computed by Position Keeping service
	account := &capersistence.CurrentAccountEntity{
		ID:                    accountID,
		AccountID:             "ACC-LIEN-001",
		AccountIdentification: "GB82WEST12345698765433",
		AccountType:           "current",
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Status:                "active",
		PartyID:               partyID,
		OverdraftLimit:        5000,
		CreatedAt:             now,
		UpdatedAt:             now,
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}
	err := db.Create(account).Error
	require.NoError(t, err, "Failed to create test account for lien test")

	// Now create a lien
	expiresAt := time.Now().Add(24 * time.Hour)
	entity := &capersistence.LienEntity{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AmountCents:           10000,
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Precision:             2,
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-LIEN-TEST-001",
		ExpiresAt:             &expiresAt,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
	}

	// Create - will fail if columns don't match
	err = db.Create(entity).Error
	if err != nil {
		t.Fatalf("Failed to create LienEntity - schema mismatch detected: %v", err)
	}

	// Read back - will fail if columns don't match
	var retrieved capersistence.LienEntity
	err = db.First(&retrieved, "id = ?", entity.ID).Error
	if err != nil {
		t.Fatalf("Failed to read LienEntity - schema mismatch detected: %v", err)
	}

	// Verify data integrity
	assert.Equal(t, entity.AccountID, retrieved.AccountID)
	assert.Equal(t, entity.AmountCents, retrieved.AmountCents)
	assert.Equal(t, entity.Status, retrieved.Status)
	assert.Equal(t, entity.PaymentOrderReference, retrieved.PaymentOrderReference)
	assert.NotNil(t, retrieved.ExpiresAt)
	assert.WithinDuration(t, *entity.ExpiresAt, *retrieved.ExpiresAt, time.Second)

	// Update - verify status transitions work (ACTIVE -> EXECUTED)
	retrieved.Status = "EXECUTED"
	retrieved.UpdatedAt = time.Now()
	err = db.Save(&retrieved).Error
	if err != nil {
		t.Fatalf("Failed to update LienEntity - schema mismatch detected: %v", err)
	}

	// Verify update persisted
	var updated capersistence.LienEntity
	err = db.First(&updated, "id = ?", entity.ID).Error
	require.NoError(t, err)
	assert.Equal(t, "EXECUTED", updated.Status)

	// Test TERMINATED status transition with termination reason
	terminatedLien := &capersistence.LienEntity{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AmountCents:           5000,
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Precision:             2,
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-LIEN-TEST-TERM",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
	}
	err = db.Create(terminatedLien).Error
	require.NoError(t, err)
	terminatedLien.Status = "TERMINATED"
	terminatedLien.TerminationReason = "Payment order cancelled by user"
	err = db.Save(terminatedLien).Error
	assert.NoError(t, err, "TERMINATED status transition should succeed")

	// Constraint validation: amount_cents must be > 0
	invalidAmountLien := &capersistence.LienEntity{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AmountCents:           0, // Invalid: must be > 0
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Precision:             2,
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-LIEN-TEST-002",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
	}
	err = db.Create(invalidAmountLien).Error
	assert.Error(t, err, "Expected error for amount_cents <= 0 constraint violation")

	// Constraint validation: unique payment_order_reference
	duplicateLien := &capersistence.LienEntity{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AmountCents:           5000,
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Precision:             2,
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-LIEN-TEST-001", // Duplicate of first lien
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
	}
	err = db.Create(duplicateLien).Error
	assert.Error(t, err, "Expected error for duplicate payment_order_reference")

	// Constraint validation: FK to non-existent account
	orphanLien := &capersistence.LienEntity{
		ID:                    uuid.New(),
		AccountID:             uuid.New(), // Non-existent account
		AmountCents:           5000,
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Precision:             2,
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-LIEN-TEST-003",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
	}
	err = db.Create(orphanLien).Error
	assert.Error(t, err, "Expected error for FK constraint violation")
}

// testWithdrawalEntity tests that WithdrawalEntity works with the migrated schema
func testWithdrawalEntity(t *testing.T, db *gorm.DB) {
	t.Helper()

	// Create an account (withdrawals have FK to accounts)
	accountID := uuid.New()
	partyID := uuid.New()
	now := time.Now()
	account := &capersistence.CurrentAccountEntity{
		ID:                    accountID,
		AccountID:             "ACC-WD-001",
		AccountIdentification: "GB82WEST12345698765499",
		AccountType:           "current",
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Status:                "active",
		PartyID:               partyID,
		OverdraftLimit:        5000,
		CreatedAt:             now,
		UpdatedAt:             now,
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}
	err := db.Create(account).Error
	require.NoError(t, err, "Failed to create test account for withdrawal test")

	// Create a withdrawal
	entity := &capersistence.WithdrawalEntity{
		ID:             uuid.New(),
		AccountID:      accountID,
		AmountCents:    10000,
		InstrumentCode: "GBP",
		Dimension:      "CURRENCY",
		Precision:      2,
		Status:         "PENDING",
		Reference:      "WD-SCHEMA-TEST-001",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Version:        1,
	}

	// Create - will fail if columns don't match
	err = db.Create(entity).Error
	if err != nil {
		t.Fatalf("Failed to create WithdrawalEntity - schema mismatch detected: %v", err)
	}

	// Read back - will fail if columns don't match
	var retrieved capersistence.WithdrawalEntity
	err = db.First(&retrieved, "id = ?", entity.ID).Error
	if err != nil {
		t.Fatalf("Failed to read WithdrawalEntity - schema mismatch detected: %v", err)
	}

	// Verify data integrity
	assert.Equal(t, entity.AccountID, retrieved.AccountID)
	assert.Equal(t, entity.AmountCents, retrieved.AmountCents)
	assert.Equal(t, entity.Status, retrieved.Status)
	assert.Equal(t, entity.Reference, retrieved.Reference)
	assert.Equal(t, int64(1), retrieved.Version)

	// Update - verify status transitions work (PENDING -> COMPLETED)
	retrieved.Status = "COMPLETED"
	retrieved.UpdatedAt = time.Now()
	err = db.Save(&retrieved).Error
	if err != nil {
		t.Fatalf("Failed to update WithdrawalEntity - schema mismatch detected: %v", err)
	}

	// Verify update persisted
	var updated capersistence.WithdrawalEntity
	err = db.First(&updated, "id = ?", entity.ID).Error
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", updated.Status)

	// Test FAILED and CANCELLED status transitions
	for _, tc := range []struct {
		status    string
		reference string
	}{
		{"FAILED", "WD-SCHEMA-TEST-FAIL"},
		{"CANCELLED", "WD-SCHEMA-TEST-CANCEL"},
	} {
		wd := &capersistence.WithdrawalEntity{
			ID:             uuid.New(),
			AccountID:      accountID,
			AmountCents:    5000,
			InstrumentCode: "GBP",
			Dimension:      "CURRENCY",
			Precision:      2,
			Status:         tc.status,
			Reference:      tc.reference,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			Version:        1,
		}
		err = db.Create(wd).Error
		assert.NoError(t, err, "Status %s should be accepted by check constraint", tc.status)
	}

	// Constraint validation: amount_cents must be > 0
	invalidAmountWd := &capersistence.WithdrawalEntity{
		ID:             uuid.New(),
		AccountID:      accountID,
		AmountCents:    0, // Invalid: must be > 0
		InstrumentCode: "GBP",
		Dimension:      "CURRENCY",
		Precision:      2,
		Status:         "PENDING",
		Reference:      "WD-SCHEMA-TEST-002",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Version:        1,
	}
	err = db.Create(invalidAmountWd).Error
	assert.Error(t, err, "Expected error for amount_cents <= 0 constraint violation")

	// Constraint validation: invalid status rejected
	invalidStatusWd := &capersistence.WithdrawalEntity{
		ID:             uuid.New(),
		AccountID:      accountID,
		AmountCents:    5000,
		InstrumentCode: "GBP",
		Dimension:      "CURRENCY",
		Precision:      2,
		Status:         "INVALID_STATUS",
		Reference:      "WD-SCHEMA-TEST-003",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Version:        1,
	}
	err = db.Create(invalidStatusWd).Error
	assert.Error(t, err, "Expected error for invalid status constraint violation")

	// Constraint validation: unique reference
	duplicateWd := &capersistence.WithdrawalEntity{
		ID:             uuid.New(),
		AccountID:      accountID,
		AmountCents:    5000,
		InstrumentCode: "GBP",
		Dimension:      "CURRENCY",
		Precision:      2,
		Status:         "PENDING",
		Reference:      "WD-SCHEMA-TEST-001", // Duplicate of first withdrawal
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Version:        1,
	}
	err = db.Create(duplicateWd).Error
	assert.Error(t, err, "Expected error for duplicate reference")

	// Constraint validation: FK to non-existent account
	orphanWd := &capersistence.WithdrawalEntity{
		ID:             uuid.New(),
		AccountID:      uuid.New(), // Non-existent account
		AmountCents:    5000,
		InstrumentCode: "GBP",
		Dimension:      "CURRENCY",
		Precision:      2,
		Status:         "PENDING",
		Reference:      "WD-SCHEMA-TEST-004",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Version:        1,
	}
	err = db.Create(orphanWd).Error
	assert.Error(t, err, "Expected error for FK constraint violation")
}

// testCurrentAccountEntity tests that CurrentAccountEntity works with the migrated schema
func testCurrentAccountEntity(t *testing.T, db *gorm.DB) {
	t.Helper()

	// Note: party_id is a reference to Party Service (no local FK constraint)
	// The customers table has been removed - party data is managed by Party Service
	partyID := uuid.New() // References a party in Party Service

	// Entity fields must match migration schema columns exactly
	// Note: Balance fields removed - balance now computed by Position Keeping service
	now := time.Now()
	entity := &capersistence.CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountID:             "ACC-TEST-001",           // Business identifier - matches account_id column
		AccountIdentification: "GB82WEST12345698765432", // IBAN - matches account_identification column
		AccountType:           "current",
		InstrumentCode:        "GBP",
		Dimension:             "CURRENCY",
		Status:                "active",
		PartyID:               partyID,
		OverdraftLimit:        5000,
		CreatedAt:             now,
		UpdatedAt:             now,
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}

	// Create - will fail if columns don't match
	err := db.Create(entity).Error
	if err != nil {
		t.Fatalf("Failed to create CurrentAccountEntity - schema mismatch detected: %v", err)
	}

	// Read back - will fail if columns don't match
	var retrieved capersistence.CurrentAccountEntity
	err = db.First(&retrieved, "id = ?", entity.ID).Error
	if err != nil {
		t.Fatalf("Failed to read CurrentAccountEntity - schema mismatch detected: %v", err)
	}

	// Verify data integrity
	assert.Equal(t, entity.AccountIdentification, retrieved.AccountIdentification)
	assert.Equal(t, entity.InstrumentCode, retrieved.InstrumentCode)
	assert.Equal(t, entity.Dimension, retrieved.Dimension)
	assert.Equal(t, entity.OverdraftLimit, retrieved.OverdraftLimit)
}

// testPaymentOrderEntity tests that PaymentOrderEntity works with the migrated schema
func testPaymentOrderEntity(t *testing.T, db *gorm.DB) {
	t.Helper()

	entity := &popersistence.PaymentOrderEntity{
		ID:                uuid.New(),
		DebtorAccountID:   "ACC-DEBTOR-001",
		CreditorReference: "CRED-REF-001",
		AmountCents:       25000,
		Currency:          "GBP",
		Status:            "INITIATED",
		CorrelationID:     uuid.New().String(),
		IdempotencyKey:    uuid.New().String(),
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		Version:           1,
	}

	// Create
	err := db.Create(entity).Error
	if err != nil {
		t.Fatalf("Failed to create PaymentOrderEntity - schema mismatch detected: %v", err)
	}

	// Read back
	var retrieved popersistence.PaymentOrderEntity
	err = db.First(&retrieved, "id = ?", entity.ID).Error
	if err != nil {
		t.Fatalf("Failed to read PaymentOrderEntity - schema mismatch detected: %v", err)
	}

	assert.Equal(t, entity.AmountCents, retrieved.AmountCents)
	assert.Equal(t, entity.Status, retrieved.Status)
	assert.Equal(t, entity.IdempotencyKey, retrieved.IdempotencyKey)
}

// testFinancialBookingLogEntity tests that FinancialBookingLogEntity works with the migrated schema
func testFinancialBookingLogEntity(t *testing.T, db *gorm.DB) {
	t.Helper()

	entity := &fapersistence.FinancialBookingLogEntity{
		ID:                      uuid.New(),
		FinancialAccountType:    "CURRENT",
		ProductServiceReference: "PSR-001",
		BusinessUnitReference:   "BUR-001",
		ChartOfAccountsRules:    `{"rules": []}`,
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		CreatedBy:               "system",
		UpdatedBy:               "system",
		Version:                 1,
	}

	// Create
	err := db.Create(entity).Error
	if err != nil {
		t.Fatalf("Failed to create FinancialBookingLogEntity - schema mismatch detected: %v", err)
	}

	// Read back
	var retrieved fapersistence.FinancialBookingLogEntity
	err = db.First(&retrieved, "id = ?", entity.ID).Error
	if err != nil {
		t.Fatalf("Failed to read FinancialBookingLogEntity - schema mismatch detected: %v", err)
	}

	assert.Equal(t, entity.FinancialAccountType, retrieved.FinancialAccountType)
	assert.Equal(t, entity.IdempotencyKey, retrieved.IdempotencyKey)
}

// testLedgerPostingEntity tests that LedgerPostingEntity works with the migrated schema
func testLedgerPostingEntity(t *testing.T, db *gorm.DB) {
	t.Helper()

	// First create a booking log (ledger postings have FK)
	bookingLogID := uuid.New()
	bookingLog := &fapersistence.FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "CURRENT",
		ProductServiceReference: "PSR-002",
		BusinessUnitReference:   "BUR-002",
		ChartOfAccountsRules:    `{"rules": []}`,
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		CreatedBy:               "system",
		UpdatedBy:               "system",
		Version:                 1,
	}
	require.NoError(t, db.Create(bookingLog).Error, "Failed to create booking log for ledger posting test")

	entity := &fapersistence.LedgerPostingEntity{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		PostingDirection:      "DEBIT",
		AmountMinorUnits:      15000,
		Currency:              "GBP",
		DimensionType:         "CURRENCY",
		InstrumentVersion:     1,
		InstrumentPrecision:   2,
		AccountID:             "ACC-LEDGER-001",
		ValueDate:             time.Now(),
		PostingResult:         "SUCCESS",
		Status:                "COMPLETED",
		CorrelationID:         uuid.New().String(),
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}

	// Create
	err := db.Create(entity).Error
	if err != nil {
		t.Fatalf("Failed to create LedgerPostingEntity - schema mismatch detected: %v", err)
	}

	// Read back
	var retrieved fapersistence.LedgerPostingEntity
	err = db.First(&retrieved, "id = ?", entity.ID).Error
	if err != nil {
		t.Fatalf("Failed to read LedgerPostingEntity - schema mismatch detected: %v", err)
	}

	assert.Equal(t, entity.AmountMinorUnits, retrieved.AmountMinorUnits)
	assert.Equal(t, entity.PostingDirection, retrieved.PostingDirection)
	assert.Equal(t, entity.DimensionType, retrieved.DimensionType)
	assert.Equal(t, entity.InstrumentVersion, retrieved.InstrumentVersion)
	assert.Equal(t, entity.InstrumentPrecision, retrieved.InstrumentPrecision)
}

// testManifestVersionEntity tests that the control-plane manifest_version table
// accepts string versions after migrations. This catches drift between the GORM
// model (varchar version) and the migration DDL - the exact bug from PR #914
// where the migration used INTEGER but the code expected VARCHAR(50).
//
// Uses raw SQL because the entity is in an internal package.
func testManifestVersionEntity(t *testing.T, db *gorm.DB) {
	t.Helper()

	id := uuid.New()
	now := time.Now()

	// Insert with a semver string version - this is what the bootstrap does.
	// If the column is INTEGER, this will fail with "invalid input syntax".
	err := db.Exec(`INSERT INTO manifest_version
		(id, version, manifest_json, applied_at, applied_by, apply_status, sequence_number, created_at)
		VALUES (?, ?, ?::jsonb, ?, ?, ?, ?, ?)`,
		id, "1.0", `{"version":"1.0"}`, now, "system:test", "APPLIED", 1, now,
	).Error
	if err != nil {
		t.Fatalf("Failed to insert manifest_version with string version - schema mismatch: %v", err)
	}

	// Read back and verify the string round-trips correctly
	var version string
	err = db.Raw(`SELECT version FROM manifest_version WHERE id = ?`, id).Scan(&version).Error
	if err != nil {
		t.Fatalf("Failed to read manifest_version - schema mismatch: %v", err)
	}

	assert.Equal(t, "1.0", version)
}
