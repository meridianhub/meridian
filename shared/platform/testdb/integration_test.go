package testdb

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/logger"
)

// Integration tests that require testcontainers (PostgreSQL).
// Skipped when running with -short flag.
// Tests are consolidated to minimize container startup overhead.

func TestPostgresSetupFunctions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Single container for multiple subtests via SetupPostgres.
	db, cleanup := SetupPostgres(t, nil, WithLogLevel(logger.Silent))
	defer cleanup()

	t.Run("BasicQuery", func(t *testing.T) {
		var result int
		err := db.Raw("SELECT 1").Scan(&result).Error
		require.NoError(t, err)
		assert.Equal(t, 1, result)
	})

	t.Run("CreateSchemas", func(t *testing.T) {
		createSchemas(t, db, map[string]bool{"test_schema": true})
		var count int64
		err := db.Raw("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = 'test_schema'").Scan(&count).Error
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})

	t.Run("SetupTenantSchema", func(t *testing.T) {
		tc := SetupTenantSchema(t, db, "my_tenant")
		defer tc.Cleanup()

		tid, ok := tenant.FromContext(tc.Ctx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("my_tenant"), tid)
	})

	t.Run("CreateTable", func(t *testing.T) {
		// Reset search_path for clean slate
		require.NoError(t, db.Exec("SET search_path TO public").Error)
		tc := SetupTenantSchema(t, db, "tbl_tenant")
		defer tc.Cleanup()

		CreateTable(t, tc.DB, tc.Tenant, `CREATE TABLE IF NOT EXISTS %s.test_items (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(100) NOT NULL
		)`)

		schemaName := tc.Tenant.SchemaName()
		err := db.Exec(fmt.Sprintf("INSERT INTO %s.test_items (id, name) VALUES (gen_random_uuid(), 'hello')", pq.QuoteIdentifier(schemaName))).Error
		require.NoError(t, err)
	})

	t.Run("CreateAuditTables", func(t *testing.T) {
		require.NoError(t, db.Exec("SET search_path TO public").Error)
		CreateAuditTables(t, db)

		err := db.Exec(`INSERT INTO audit_outbox (table_name, operation, record_id) VALUES ('test', 'INSERT', '1')`).Error
		require.NoError(t, err)

		err = db.Exec(`INSERT INTO audit_log (table_name, operation, record_id) VALUES ('test', 'UPDATE', '2')`).Error
		require.NoError(t, err)
	})
}

func TestSetupTestDB_AllOptions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	type TestEntity struct {
		ID   uint   `gorm:"primaryKey"`
		Name string `gorm:"size:100"`
	}

	// Test with tenant, models, and audit tables all at once to minimize containers.
	db, ctx, cleanup := SetupTestDB(t,
		WithModels(&TestEntity{}),
		WithTenant("acme_bank"),
		WithAuditTables(),
		WithSetupLogLevel(logger.Silent),
	)
	defer cleanup()

	// Context should have tenant
	tid, ok := tenant.FromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, tenant.TenantID("acme_bank"), tid)

	// Should be able to write to tenant schema
	err := db.Create(&TestEntity{ID: 1, Name: "acme"}).Error
	require.NoError(t, err)

	// Audit tables should exist
	var result int
	err = db.Raw("SELECT 1 FROM audit_outbox LIMIT 1").Scan(&result).Error
	require.NoError(t, err)
}

// setupTempMigrations creates a temporary services/<name>/migrations/ directory
// with simple SQL migration files for testing pgx migration functions.
func setupTempMigrations(t *testing.T) (baseDir string) {
	t.Helper()

	tmpDir := t.TempDir()
	migrationsDir := filepath.Join(tmpDir, "services", "test-svc", "migrations")
	require.NoError(t, os.MkdirAll(migrationsDir, 0o755))

	migration1 := `CREATE TABLE IF NOT EXISTS test_items (
		id SERIAL PRIMARY KEY,
		name VARCHAR(100) NOT NULL
	);`
	require.NoError(t, os.WriteFile(
		filepath.Join(migrationsDir, "001_create_test_items.sql"),
		[]byte(migration1), 0o644))

	// Non-SQL file that should be skipped
	require.NoError(t, os.WriteFile(
		filepath.Join(migrationsDir, "README.md"),
		[]byte("# Migrations"), 0o644))

	// Subdirectory that should be skipped
	require.NoError(t, os.MkdirAll(filepath.Join(migrationsDir, "subdir"), 0o755))

	migration2 := `ALTER TABLE test_items ADD COLUMN IF NOT EXISTS description TEXT;`
	require.NoError(t, os.WriteFile(
		filepath.Join(migrationsDir, "002_add_description.sql"),
		[]byte(migration2), 0o644))

	return tmpDir
}

func TestSetupTestDB_NoOptions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, ctx, cleanup := SetupTestDB(t)
	defer cleanup()

	var result int
	err := db.Raw("SELECT 1").Scan(&result).Error
	require.NoError(t, err)
	assert.Equal(t, 1, result)

	// No tenant in context
	_, ok := tenant.FromContext(ctx)
	assert.False(t, ok)
}

func TestStartSharedPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	connStr, cleanup := StartSharedPostgres()
	defer cleanup()

	assert.NotEmpty(t, connStr)
	assert.Contains(t, connStr, "postgres")
}

func TestCockroachDBSetup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	type CRDBModel struct {
		ID   uint   `gorm:"primaryKey"`
		Name string `gorm:"size:100"`
	}

	// Test StartCockroachContainer + CockroachDSN
	t.Run("StartContainerAndDSN", func(t *testing.T) {
		container, cleanup := StartCockroachContainer(t, "crdb_dsn_test")
		defer cleanup()

		dsn := CockroachDSN(t, container)
		assert.Contains(t, dsn, "postgres://")
		assert.Contains(t, dsn, "crdb_dsn_test")
	})

	// Test SetupCockroachDB end-to-end
	t.Run("SetupWithModels", func(t *testing.T) {
		db, dbCleanup := SetupCockroachDB(t, []interface{}{&CRDBModel{}}, WithLogLevel(logger.Silent))
		defer dbCleanup()

		err := db.Create(&CRDBModel{ID: 1, Name: "crdb"}).Error
		require.NoError(t, err)

		var found CRDBModel
		err = db.First(&found, 1).Error
		require.NoError(t, err)
		assert.Equal(t, "crdb", found.Name)
	})
}

func TestPgxMigrationFunctions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	baseDir := setupTempMigrations(t)

	origDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(baseDir))
	defer func() { _ = os.Chdir(origDir) }()

	// Single pool for migration tests
	pool := NewTestPool(t, WithMigrations("test-svc"))

	t.Run("MigrationsApplied", func(t *testing.T) {
		var count int
		err := pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM test_items").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)

		// Second migration added column
		_, err = pool.Exec(t.Context(), "INSERT INTO test_items (name, description) VALUES ('a', 'b')")
		require.NoError(t, err)
	})

	t.Run("SetupTenantSchemaForPgx", func(t *testing.T) {
		ctx, cleanup := SetupTenantSchemaForPgx(t, pool, "pgx-tenant", "test-svc")
		defer cleanup()

		tid, ok := tenant.FromContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("pgx-tenant"), tid)

		// Table should exist in tenant schema
		schemaName := tenant.TenantID("pgx-tenant").SchemaName()
		var exists bool
		err := pool.QueryRow(t.Context(),
			"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'test_items')",
			schemaName).Scan(&exists)
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("SetupTenantSchemaForPgx_NoMigrations", func(t *testing.T) {
		ctx, cleanup := SetupTenantSchemaForPgx(t, pool, "empty-tenant", "")
		defer cleanup()

		tid, ok := tenant.FromContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("empty-tenant"), tid)
	})
}
