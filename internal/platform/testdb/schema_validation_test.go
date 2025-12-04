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

	capersistence "github.com/meridianhub/meridian/internal/current-account/adapters/persistence"
	fapersistence "github.com/meridianhub/meridian/internal/financial-accounting/adapters/persistence"
	popersistence "github.com/meridianhub/meridian/internal/payment-order/adapters/persistence"
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
// - internal/domain/models/* (used by Atlas to generate migrations)
// - internal/*/adapters/persistence/*_entity.go (used by application code)
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

	t.Run("CurrentAccount/AccountEntity", func(t *testing.T) {
		testCurrentAccountEntity(t, gormDB)
	})

	t.Run("CurrentAccount/LienEntity", func(t *testing.T) {
		testLienEntity(t, gormDB)
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

	// Schemas to include in migration
	schemas := []string{
		"current_account",
		"position_keeping",
		"payment_order",
		"financial_accounting",
	}

	// Collect all migration files from all schemas
	var allMigrations []migrationFile
	for _, schema := range schemas {
		migrationDir := filepath.Join(projectRoot, "migrations", schema)

		entries, err := os.ReadDir(migrationDir)
		if err != nil {
			// Schema might not have migrations yet
			t.Logf("No migrations found for schema %s: %v", schema, err)
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

	// First create a customer (accounts have FK to customers)
	customerID := uuid.New()
	customerSQL := `
		INSERT INTO current_account.customers
		(id, customer_number, first_name, last_name, email, status, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	err := db.Exec(customerSQL, customerID, "CUST-LIEN-001", "Lien", "Test", "lien@example.com", "active", "system", "system").Error
	require.NoError(t, err, "Failed to create test customer for lien test")

	// Then create an account (liens have FK to accounts)
	accountID := uuid.New()
	account := &capersistence.CurrentAccountEntity{
		ID:                    accountID,
		AccountID:             "ACC-LIEN-001",
		AccountIdentification: "GB82WEST12345698765433",
		AccountType:           "current",
		Currency:              "GBP",
		Status:                "active",
		CustomerID:            customerID,
		Balance:               50000,
		AvailableBalance:      40000,
		OverdraftLimit:        5000,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}
	err = db.Create(account).Error
	require.NoError(t, err, "Failed to create test account for lien test")

	// Now create a lien
	expiresAt := time.Now().Add(24 * time.Hour)
	entity := &capersistence.LienEntity{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AmountCents:           10000,
		Currency:              "GBP",
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
	assert.Equal(t, entity.Currency, retrieved.Currency)
	assert.Equal(t, entity.Status, retrieved.Status)
	assert.Equal(t, entity.PaymentOrderReference, retrieved.PaymentOrderReference)
}

// testCurrentAccountEntity tests that CurrentAccountEntity works with the migrated schema
func testCurrentAccountEntity(t *testing.T, db *gorm.DB) {
	t.Helper()

	// First create a customer (accounts have FK to customers)
	customerID := uuid.New()
	customerSQL := `
		INSERT INTO current_account.customers
		(id, customer_number, first_name, last_name, email, status, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	err := db.Exec(customerSQL, customerID, "CUST-TEST-001", "Test", "User", "test@example.com", "active", "system", "system").Error
	require.NoError(t, err, "Failed to create test customer")

	// Entity fields must match migration schema columns exactly
	entity := &capersistence.CurrentAccountEntity{
		ID:                    uuid.New(),
		AccountID:             "ACC-TEST-001",           // Business identifier - matches account_id column
		AccountIdentification: "GB82WEST12345698765432", // IBAN - matches account_identification column
		AccountType:           "current",
		Currency:              "GBP",
		Status:                "active",
		CustomerID:            customerID,
		Balance:               10000,
		AvailableBalance:      8000,
		OverdraftLimit:        5000,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}

	// Create - will fail if columns don't match
	err = db.Create(entity).Error
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
	assert.Equal(t, entity.Balance, retrieved.Balance)
	assert.Equal(t, entity.Currency, retrieved.Currency)
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
		AmountCents:           15000,
		Currency:              "GBP",
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

	assert.Equal(t, entity.AmountCents, retrieved.AmountCents)
	assert.Equal(t, entity.PostingDirection, retrieved.PostingDirection)
}
