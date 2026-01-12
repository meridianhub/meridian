package migrations_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getMigrationsDir returns the absolute path to the migrations directory.
func getMigrationsDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "Failed to get caller info")
	return filepath.Dir(filename)
}

// TestMigrations_ApplySuccessfully verifies that all migrations can be applied
// to a clean PostgreSQL database.
func TestMigrations_ApplySuccessfully(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migration test in short mode")
	}

	migrationsDir := getMigrationsDir(t)

	// Setup PostgreSQL container - pass nil for models since we're using Atlas
	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	// Verify PostgreSQL connection
	var version string
	err := db.Raw("SELECT version()").Scan(&version).Error
	require.NoError(t, err)
	require.Contains(t, version, "PostgreSQL")

	// Read and execute migration SQL via GORM
	migrationSQL, err := readMigrationFile(filepath.Join(migrationsDir, "20260112000001_initial.sql"))
	require.NoError(t, err)

	// Execute migration
	err = db.Exec(migrationSQL).Error
	require.NoError(t, err, "Migration should apply successfully")

	t.Logf("Migration applied successfully")
}

// readMigrationFile reads the migration SQL file.
func readMigrationFile(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// TestSchema_TableStructure verifies the created table structure matches expectations.
func TestSchema_TableStructure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping schema test in short mode")
	}

	migrationsDir := getMigrationsDir(t)

	// Setup PostgreSQL
	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	// Read and execute migration
	migrationSQL, err := readMigrationFile(filepath.Join(migrationsDir, "20260112000001_initial.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(migrationSQL).Error)

	// Verify internal_bank_account table exists
	var tableCount int
	err = db.Raw(`
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_name = 'internal_bank_account'
	`).Scan(&tableCount).Error
	require.NoError(t, err)
	assert.Equal(t, 1, tableCount, "internal_bank_account table should exist")

	// Verify status_history table exists
	err = db.Raw(`
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_name = 'internal_bank_account_status_history'
	`).Scan(&tableCount).Error
	require.NoError(t, err)
	assert.Equal(t, 1, tableCount, "internal_bank_account_status_history table should exist")

	// Verify indexes exist (at least 5 on main table)
	var indexCount int
	err = db.Raw(`
		SELECT COUNT(*) FROM pg_indexes
		WHERE tablename = 'internal_bank_account'
	`).Scan(&indexCount).Error
	require.NoError(t, err)
	assert.GreaterOrEqual(t, indexCount, 5, "Should have at least 5 indexes on internal_bank_account")

	// Verify specific indexes exist
	var indexNames []string
	err = db.Raw(`
		SELECT indexname FROM pg_indexes
		WHERE tablename = 'internal_bank_account'
		ORDER BY indexname
	`).Scan(&indexNames).Error
	require.NoError(t, err)

	expectedIndexes := []string{
		"idx_internal_bank_account_account_id",
		"idx_internal_bank_account_code",
		"idx_internal_bank_account_instrument",
		"idx_internal_bank_account_status",
		"idx_internal_bank_account_type",
	}
	for _, expected := range expectedIndexes {
		assert.Contains(t, indexNames, expected, "Index %s should exist", expected)
	}
}

// TestSchema_ConstraintsWork verifies CHECK constraints reject invalid data.
func TestSchema_ConstraintsWork(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping constraint test in short mode")
	}

	migrationsDir := getMigrationsDir(t)

	// Setup and apply migration
	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	migrationSQL, err := readMigrationFile(filepath.Join(migrationsDir, "20260112000001_initial.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(migrationSQL).Error)

	// Test: Valid insert should succeed
	err = db.Exec(`
		INSERT INTO internal_bank_account (
			account_id, account_code, name, account_type,
			instrument_code, dimension, created_by, updated_by
		) VALUES (
			'ACC-001', 'CLEAR-GBP', 'GBP Clearing Account', 'CLEARING',
			'GBP', 'CURRENCY', 'test', 'test'
		)
	`).Error
	assert.NoError(t, err, "Valid insert should succeed")

	// Test: Invalid account_type should fail
	err = db.Exec(`
		INSERT INTO internal_bank_account (
			account_id, account_code, name, account_type,
			instrument_code, dimension, created_by, updated_by
		) VALUES (
			'ACC-002', 'INVALID', 'Invalid Account', 'INVALID_TYPE',
			'GBP', 'CURRENCY', 'test', 'test'
		)
	`).Error
	assert.Error(t, err, "Invalid account_type should be rejected")
	assert.Contains(t, err.Error(), "chk_account_type", "Error should reference constraint name")

	// Test: Invalid dimension should fail
	err = db.Exec(`
		INSERT INTO internal_bank_account (
			account_id, account_code, name, account_type,
			instrument_code, dimension, created_by, updated_by
		) VALUES (
			'ACC-003', 'INVALID', 'Invalid Dimension', 'CLEARING',
			'GBP', 'INVALID_DIM', 'test', 'test'
		)
	`).Error
	assert.Error(t, err, "Invalid dimension should be rejected")
	assert.Contains(t, err.Error(), "chk_dimension", "Error should reference constraint name")

	// Test: Invalid status should fail
	err = db.Exec(`
		INSERT INTO internal_bank_account (
			account_id, account_code, name, account_type,
			instrument_code, dimension, status, created_by, updated_by
		) VALUES (
			'ACC-004', 'INVALID', 'Invalid Status', 'CLEARING',
			'GBP', 'CURRENCY', 'INVALID_STATUS', 'test', 'test'
		)
	`).Error
	assert.Error(t, err, "Invalid status should be rejected")
	assert.Contains(t, err.Error(), "chk_status", "Error should reference constraint name")
}

// TestSchema_ForeignKeyConstraint verifies the status_history foreign key works.
func TestSchema_ForeignKeyConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping foreign key test in short mode")
	}

	migrationsDir := getMigrationsDir(t)

	// Setup and apply migration
	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	migrationSQL, err := readMigrationFile(filepath.Join(migrationsDir, "20260112000001_initial.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(migrationSQL).Error)

	// Create an account first
	err = db.Exec(`
		INSERT INTO internal_bank_account (
			account_id, account_code, name, account_type,
			instrument_code, dimension, created_by, updated_by
		) VALUES (
			'ACC-FK-TEST', 'FK-TEST', 'FK Test Account', 'CLEARING',
			'GBP', 'CURRENCY', 'test', 'test'
		)
	`).Error
	require.NoError(t, err)

	// Valid status history insert should succeed
	err = db.Exec(`
		INSERT INTO internal_bank_account_status_history (
			account_id, from_status, to_status, reason, changed_by
		) VALUES (
			'ACC-FK-TEST', 'ACTIVE', 'SUSPENDED', 'Test suspension', 'test'
		)
	`).Error
	assert.NoError(t, err, "Valid status history insert should succeed")

	// Invalid foreign key should fail
	err = db.Exec(`
		INSERT INTO internal_bank_account_status_history (
			account_id, from_status, to_status, reason, changed_by
		) VALUES (
			'NON-EXISTENT', 'ACTIVE', 'SUSPENDED', 'Test', 'test'
		)
	`).Error
	assert.Error(t, err, "Invalid foreign key should be rejected")
	assert.Contains(t, err.Error(), "fk_status_history_account", "Error should reference constraint name")

	// Verify DELETE RESTRICT works
	err = db.Exec(`DELETE FROM internal_bank_account WHERE account_id = 'ACC-FK-TEST'`).Error
	assert.Error(t, err, "Cannot delete account with status history")
}

// TestSchema_JSONBAttributes verifies JSONB column works correctly.
func TestSchema_JSONBAttributes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping JSONB test in short mode")
	}

	migrationsDir := getMigrationsDir(t)

	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	migrationSQL, err := readMigrationFile(filepath.Join(migrationsDir, "20260112000001_initial.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(migrationSQL).Error)

	// Insert with JSON attributes
	err = db.Exec(`
		INSERT INTO internal_bank_account (
			account_id, account_code, name, account_type,
			instrument_code, dimension, attributes, created_by, updated_by
		) VALUES (
			'ACC-JSON', 'JSON-TEST', 'JSON Test Account', 'HOLDING',
			'BTC', 'CURRENCY',
			'{"cost_center": "CC-001", "regulatory_flags": ["MiFID", "EMIR"], "metadata": {"source": "migration"}}',
			'test', 'test'
		)
	`).Error
	require.NoError(t, err)

	// Query JSONB field
	var costCenter string
	err = db.Raw(`
		SELECT attributes->>'cost_center' FROM internal_bank_account
		WHERE account_id = 'ACC-JSON'
	`).Scan(&costCenter).Error
	require.NoError(t, err)
	assert.Equal(t, "CC-001", costCenter)

	// Query nested JSONB
	var source string
	err = db.Raw(`
		SELECT attributes->'metadata'->>'source' FROM internal_bank_account
		WHERE account_id = 'ACC-JSON'
	`).Scan(&source).Error
	require.NoError(t, err)
	assert.Equal(t, "migration", source)
}

// TestSchema_AllAccountTypes verifies all account types are valid.
func TestSchema_AllAccountTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping account types test in short mode")
	}

	migrationsDir := getMigrationsDir(t)

	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	migrationSQL, err := readMigrationFile(filepath.Join(migrationsDir, "20260112000001_initial.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(migrationSQL).Error)

	accountTypes := []string{
		"CLEARING", "NOSTRO", "VOSTRO", "HOLDING",
		"SUSPENSE", "REVENUE", "EXPENSE", "INVENTORY",
	}

	for _, accountType := range accountTypes {
		accountID := "ACC-TYPE-" + accountType
		err = db.Exec(`
			INSERT INTO internal_bank_account (
				account_id, account_code, name, account_type,
				instrument_code, dimension, created_by, updated_by
			) VALUES (?, ?, ?, ?, 'GBP', 'CURRENCY', 'test', 'test')
		`, accountID, "CODE-"+accountType, accountType+" Account", accountType).Error
		assert.NoError(t, err, "Account type %s should be valid", accountType)
	}
}

// TestSchema_AllDimensions verifies all dimension types are valid.
func TestSchema_AllDimensions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping dimensions test in short mode")
	}

	migrationsDir := getMigrationsDir(t)

	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	migrationSQL, err := readMigrationFile(filepath.Join(migrationsDir, "20260112000001_initial.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(migrationSQL).Error)

	dimensions := []string{
		"CURRENCY", "ENERGY", "MASS", "VOLUME", "TIME",
		"COMPUTE", "CARBON", "DATA", "COUNT",
	}

	for _, dimension := range dimensions {
		accountID := "ACC-DIM-" + dimension
		err = db.Exec(`
			INSERT INTO internal_bank_account (
				account_id, account_code, name, account_type,
				instrument_code, dimension, created_by, updated_by
			) VALUES (?, ?, ?, 'HOLDING', 'TEST', ?, 'test', 'test')
		`, accountID, "CODE-"+dimension, dimension+" Dimension Account", dimension).Error
		assert.NoError(t, err, "Dimension %s should be valid", dimension)
	}
}

// TestSchema_UniqueConstraints verifies unique constraints work.
func TestSchema_UniqueConstraints(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping unique constraint test in short mode")
	}

	migrationsDir := getMigrationsDir(t)

	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	migrationSQL, err := readMigrationFile(filepath.Join(migrationsDir, "20260112000001_initial.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(migrationSQL).Error)

	// Insert first account
	err = db.Exec(`
		INSERT INTO internal_bank_account (
			account_id, account_code, name, account_type,
			instrument_code, dimension, created_by, updated_by
		) VALUES (
			'ACC-UNIQUE', 'UNIQUE-CODE', 'Unique Account', 'CLEARING',
			'GBP', 'CURRENCY', 'test', 'test'
		)
	`).Error
	require.NoError(t, err)

	// Duplicate account_id should fail
	err = db.Exec(`
		INSERT INTO internal_bank_account (
			account_id, account_code, name, account_type,
			instrument_code, dimension, created_by, updated_by
		) VALUES (
			'ACC-UNIQUE', 'DIFFERENT-CODE', 'Another Account', 'CLEARING',
			'GBP', 'CURRENCY', 'test', 'test'
		)
	`).Error
	assert.Error(t, err, "Duplicate account_id should fail")
	assert.Contains(t, err.Error(), "idx_internal_bank_account_account_id", "Error should reference unique index")
}

// TestSchema_CorrespondentBankFields verifies correspondent bank fields are nullable.
func TestSchema_CorrespondentBankFields(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping correspondent bank test in short mode")
	}

	migrationsDir := getMigrationsDir(t)

	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	migrationSQL, err := readMigrationFile(filepath.Join(migrationsDir, "20260112000001_initial.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(migrationSQL).Error)

	// Insert without correspondent bank (CLEARING account)
	err = db.Exec(`
		INSERT INTO internal_bank_account (
			account_id, account_code, name, account_type,
			instrument_code, dimension, created_by, updated_by
		) VALUES (
			'ACC-NO-CORR', 'NO-CORR', 'No Correspondent', 'CLEARING',
			'GBP', 'CURRENCY', 'test', 'test'
		)
	`).Error
	assert.NoError(t, err, "Account without correspondent should succeed")

	// Insert with correspondent bank (NOSTRO account)
	err = db.Exec(`
		INSERT INTO internal_bank_account (
			account_id, account_code, name, account_type,
			instrument_code, dimension,
			correspondent_bank_id, correspondent_bank_name, correspondent_external_ref,
			created_by, updated_by
		) VALUES (
			'ACC-NOSTRO', 'NOSTRO-USD', 'USD Nostro at JPM', 'NOSTRO',
			'USD', 'CURRENCY',
			'JPMORGAN', 'JPMorgan Chase Bank', 'REF-123456',
			'test', 'test'
		)
	`).Error
	assert.NoError(t, err, "Nostro account with correspondent should succeed")

	// Verify correspondent fields
	var bankID, bankName, extRef *string
	err = db.Raw(`
		SELECT correspondent_bank_id, correspondent_bank_name, correspondent_external_ref
		FROM internal_bank_account WHERE account_id = 'ACC-NOSTRO'
	`).Row().Scan(&bankID, &bankName, &extRef)
	require.NoError(t, err)
	assert.NotNil(t, bankID)
	assert.Equal(t, "JPMORGAN", *bankID)
	assert.Equal(t, "JPMorgan Chase Bank", *bankName)
	assert.Equal(t, "REF-123456", *extRef)
}
