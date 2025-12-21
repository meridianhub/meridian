// Package migrations provides integration tests for tenant service database migrations.
package migrations

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// testContext holds shared test resources.
type testContext struct {
	ctx     context.Context
	db      *sql.DB
	cleanup func()
}

// setupTestDatabase creates a PostgreSQL testcontainer and applies all migrations.
func setupTestDatabase(t *testing.T) *testContext {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("tenant_test"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	db, err := sql.Open("pgx", connStr)
	require.NoError(t, err, "Failed to connect to database")

	// Apply migrations
	applyMigrations(ctx, t, db)

	// Create a long-running context for test operations
	testCtx := context.Background()

	cleanup := func() {
		cancel()
		db.Close()
		if err := pgContainer.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}

	return &testContext{ctx: testCtx, db: db, cleanup: cleanup}
}

// applyMigrations applies all SQL migration files in the migrations directory.
func applyMigrations(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()

	// Get the directory containing this test file
	migrationDir, err := findMigrationDir()
	require.NoError(t, err, "Failed to find migration directory")

	entries, err := os.ReadDir(migrationDir)
	require.NoError(t, err, "Failed to read migration directory")

	// Collect and sort migration files
	var migrationFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			migrationFiles = append(migrationFiles, entry.Name())
		}
	}
	sort.Strings(migrationFiles) // Ensure timestamp order

	// Apply each migration
	for _, filename := range migrationFiles {
		content, err := os.ReadFile(filepath.Join(migrationDir, filename))
		require.NoError(t, err, "Failed to read migration %s", filename)

		_, err = db.ExecContext(ctx, string(content))
		require.NoError(t, err, "Failed to apply migration %s", filename)

		t.Logf("Applied migration: %s", filename)
	}
}

// findMigrationDir locates the migrations directory relative to the test file.
func findMigrationDir() (string, error) {
	// Start from current working directory and look for migrations
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// If we're already in the migrations directory
	if filepath.Base(dir) == "migrations" {
		return dir, nil
	}

	// Look for services/tenant/migrations relative to project root
	for {
		migrationPath := filepath.Join(dir, "services", "tenant", "migrations")
		if _, err := os.Stat(migrationPath); err == nil {
			return migrationPath, nil
		}

		// Also check if we're at a go.mod level
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			migrationPath := filepath.Join(dir, "services", "tenant", "migrations")
			if _, err := os.Stat(migrationPath); err == nil {
				return migrationPath, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", os.ErrNotExist
}

// createTestTenant inserts a test tenant record for foreign key testing.
func createTestTenant(t *testing.T, tc *testContext, tenantID string) {
	t.Helper()

	_, err := tc.db.ExecContext(tc.ctx, `
		INSERT INTO tenant (id, display_name, settlement_asset, status)
		VALUES ($1, $2, $3, $4)
	`, tenantID, "Test Tenant "+tenantID, "GBP", "active")
	require.NoError(t, err, "Failed to create test tenant")
}

// TestProvisioningStatusTableExists verifies the table was created.
func TestProvisioningStatusTableExists(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	var tableName string
	err := tc.db.QueryRowContext(tc.ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = 'tenant_provisioning_status'
	`).Scan(&tableName)

	require.NoError(t, err, "tenant_provisioning_status table should exist")
	assert.Equal(t, "tenant_provisioning_status", tableName)
}

// TestProvisioningStatusTableColumns verifies the table has all expected columns with correct types.
func TestProvisioningStatusTableColumns(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	expectedColumns := map[string]string{
		"id":                "integer",
		"tenant_id":         "character varying",
		"service_name":      "character varying",
		"status":            "character varying",
		"migration_version": "character varying",
		"error_message":     "text",
		"started_at":        "timestamp with time zone",
		"completed_at":      "timestamp with time zone",
		"created_at":        "timestamp with time zone",
		"updated_at":        "timestamp with time zone",
	}

	rows, err := tc.db.QueryContext(tc.ctx, `
		SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'tenant_provisioning_status'
	`)
	require.NoError(t, err, "Failed to query columns")
	defer rows.Close()

	foundColumns := make(map[string]string)
	for rows.Next() {
		var colName, dataType string
		require.NoError(t, rows.Scan(&colName, &dataType))
		foundColumns[colName] = dataType
	}
	require.NoError(t, rows.Err())

	for colName, expectedType := range expectedColumns {
		actualType, exists := foundColumns[colName]
		assert.True(t, exists, "Column %s should exist", colName)
		assert.Equal(t, expectedType, actualType, "Column %s should have type %s, got %s", colName, expectedType, actualType)
	}
}

// TestProvisioningStatusIndexes verifies the expected indexes exist.
func TestProvisioningStatusIndexes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	expectedIndexes := []string{
		"idx_tenant_provisioning_status_tenant_id",
		"idx_tenant_provisioning_status_status",
		"idx_tenant_provisioning_status_service_name",
	}

	for _, indexName := range expectedIndexes {
		var exists bool
		err := tc.db.QueryRowContext(tc.ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_indexes
				WHERE schemaname = 'public'
				AND tablename = 'tenant_provisioning_status'
				AND indexname = $1
			)
		`, indexName).Scan(&exists)

		require.NoError(t, err, "Failed to check index %s", indexName)
		assert.True(t, exists, "Index %s should exist", indexName)
	}
}

// TestProvisioningStatusUniqueConstraint tests the UNIQUE(tenant_id, service_name) constraint.
func TestProvisioningStatusUniqueConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	// Create a test tenant
	createTestTenant(t, tc, "unique_test_tenant")

	// Insert first record
	_, err := tc.db.ExecContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
		VALUES ('unique_test_tenant', 'party', 'pending')
	`)
	require.NoError(t, err, "First insert should succeed")

	// Try to insert duplicate (same tenant_id, same service_name)
	_, err = tc.db.ExecContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
		VALUES ('unique_test_tenant', 'party', 'in_progress')
	`)
	assert.Error(t, err, "Duplicate (tenant_id, service_name) should fail")
	assert.Contains(t, err.Error(), "unique", "Error should mention unique constraint violation")
}

// TestProvisioningStatusCheckConstraint tests the CHECK constraint on status column.
func TestProvisioningStatusCheckConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	createTestTenant(t, tc, "check_test_tenant")

	// Valid status values should succeed
	validStatuses := []string{"pending", "in_progress", "completed", "failed"}
	for i, status := range validStatuses {
		_, err := tc.db.ExecContext(tc.ctx, `
			INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
			VALUES ($1, $2, $3)
		`, "check_test_tenant", "service_"+string(rune('a'+i)), status)
		assert.NoError(t, err, "Valid status %q should succeed", status)
	}

	// Invalid status should fail
	_, err := tc.db.ExecContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
		VALUES ('check_test_tenant', 'invalid_service', 'invalid_status')
	`)
	assert.Error(t, err, "Invalid status should fail")
	assert.Contains(t, err.Error(), "check", "Error should mention check constraint violation")
}

// TestProvisioningStatusForeignKeyConstraint tests the FK to tenant table.
func TestProvisioningStatusForeignKeyConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	// Try to insert with non-existent tenant_id
	_, err := tc.db.ExecContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
		VALUES ('nonexistent_tenant', 'party', 'pending')
	`)
	assert.Error(t, err, "Insert with non-existent tenant_id should fail")
	assert.Contains(t, err.Error(), "foreign key", "Error should mention foreign key violation")
}

// TestProvisioningStatusForeignKeyDeleteRestrict tests ON DELETE RESTRICT behavior.
func TestProvisioningStatusForeignKeyDeleteRestrict(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	// Create tenant and provisioning status
	createTestTenant(t, tc, "delete_test_tenant")
	_, err := tc.db.ExecContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
		VALUES ('delete_test_tenant', 'party', 'completed')
	`)
	require.NoError(t, err)

	// Try to delete the tenant - should fail due to ON DELETE RESTRICT
	_, err = tc.db.ExecContext(tc.ctx, `DELETE FROM tenant WHERE id = 'delete_test_tenant'`)
	assert.Error(t, err, "Deleting tenant with provisioning status should fail")
	assert.Contains(t, err.Error(), "foreign key", "Error should mention foreign key constraint")
}

// TestProvisioningStatusConcurrentInserts tests that concurrent inserts with
// same tenant_id but different service_names succeed.
func TestProvisioningStatusConcurrentInserts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	createTestTenant(t, tc, "concurrent_test_tenant")

	services := []string{"party", "account", "transaction", "position", "payment"}
	var wg sync.WaitGroup
	errChan := make(chan error, len(services))

	for _, service := range services {
		wg.Add(1)
		go func(svc string) {
			defer wg.Done()
			_, err := tc.db.ExecContext(tc.ctx, `
				INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
				VALUES ('concurrent_test_tenant', $1, 'pending')
			`, svc)
			if err != nil {
				errChan <- err
			}
		}(service)
	}

	wg.Wait()
	close(errChan)

	// Collect any errors
	errs := make([]error, 0, len(services))
	for err := range errChan {
		errs = append(errs, err)
	}

	assert.Empty(t, errs, "All concurrent inserts should succeed: %v", errs)

	// Verify all records were inserted
	var count int
	err := tc.db.QueryRowContext(tc.ctx, `
		SELECT COUNT(*) FROM tenant_provisioning_status
		WHERE tenant_id = 'concurrent_test_tenant'
	`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, len(services), count, "All services should be inserted")
}

// TestProvisioningStatusInsertAndUpdate tests basic CRUD operations.
func TestProvisioningStatusInsertAndUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	createTestTenant(t, tc, "crud_test_tenant")

	// Insert
	_, err := tc.db.ExecContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status
		(tenant_id, service_name, status, started_at)
		VALUES ('crud_test_tenant', 'party', 'in_progress', NOW())
	`)
	require.NoError(t, err)

	// Update status to completed
	_, err = tc.db.ExecContext(tc.ctx, `
		UPDATE tenant_provisioning_status
		SET status = 'completed',
		    completed_at = NOW(),
		    migration_version = '20251218000001',
		    updated_at = NOW()
		WHERE tenant_id = 'crud_test_tenant' AND service_name = 'party'
	`)
	require.NoError(t, err)

	// Verify update
	var status, migrationVersion string
	var completedAt time.Time
	err = tc.db.QueryRowContext(tc.ctx, `
		SELECT status, migration_version, completed_at
		FROM tenant_provisioning_status
		WHERE tenant_id = 'crud_test_tenant' AND service_name = 'party'
	`).Scan(&status, &migrationVersion, &completedAt)
	require.NoError(t, err)

	assert.Equal(t, "completed", status)
	assert.Equal(t, "20251218000001", migrationVersion)
	assert.False(t, completedAt.IsZero(), "completed_at should be set")
}

// TestProvisioningStatusErrorMessage tests error_message field for failed status.
func TestProvisioningStatusErrorMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	createTestTenant(t, tc, "error_test_tenant")

	errorMsg := "Connection refused: Unable to reach party service at 10.0.0.1:5432"

	_, err := tc.db.ExecContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status
		(tenant_id, service_name, status, error_message, completed_at)
		VALUES ('error_test_tenant', 'party', 'failed', $1, NOW())
	`, errorMsg)
	require.NoError(t, err)

	var storedError string
	err = tc.db.QueryRowContext(tc.ctx, `
		SELECT error_message FROM tenant_provisioning_status
		WHERE tenant_id = 'error_test_tenant' AND service_name = 'party'
	`).Scan(&storedError)
	require.NoError(t, err)
	assert.Equal(t, errorMsg, storedError)
}

// TestProvisioningStatusDefaultTimestamps tests created_at and updated_at defaults.
func TestProvisioningStatusDefaultTimestamps(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	createTestTenant(t, tc, "timestamp_test_tenant")
	beforeInsert := time.Now().Add(-time.Second) // Buffer for timing

	_, err := tc.db.ExecContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
		VALUES ('timestamp_test_tenant', 'party', 'pending')
	`)
	require.NoError(t, err)

	var createdAt, updatedAt time.Time
	err = tc.db.QueryRowContext(tc.ctx, `
		SELECT created_at, updated_at FROM tenant_provisioning_status
		WHERE tenant_id = 'timestamp_test_tenant' AND service_name = 'party'
	`).Scan(&createdAt, &updatedAt)
	require.NoError(t, err)

	assert.True(t, createdAt.After(beforeInsert), "created_at should be set to current time")
	assert.True(t, updatedAt.After(beforeInsert), "updated_at should be set to current time")
}

// TestProvisioningStatusPrimaryKeyAutoIncrement tests the SERIAL primary key.
func TestProvisioningStatusPrimaryKeyAutoIncrement(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupTestDatabase(t)
	defer tc.cleanup()

	createTestTenant(t, tc, "autoincrement_test_tenant")

	var id1, id2, id3 int

	err := tc.db.QueryRowContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
		VALUES ('autoincrement_test_tenant', 'service_a', 'pending')
		RETURNING id
	`).Scan(&id1)
	require.NoError(t, err)

	err = tc.db.QueryRowContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
		VALUES ('autoincrement_test_tenant', 'service_b', 'pending')
		RETURNING id
	`).Scan(&id2)
	require.NoError(t, err)

	err = tc.db.QueryRowContext(tc.ctx, `
		INSERT INTO tenant_provisioning_status (tenant_id, service_name, status)
		VALUES ('autoincrement_test_tenant', 'service_c', 'pending')
		RETURNING id
	`).Scan(&id3)
	require.NoError(t, err)

	assert.Greater(t, id2, id1, "IDs should auto-increment")
	assert.Greater(t, id3, id2, "IDs should auto-increment")
}
