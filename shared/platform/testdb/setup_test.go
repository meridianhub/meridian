package testdb_test

import (
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetupTestDB_NoOptions verifies basic setup with no options.
func TestSetupTestDB_NoOptions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}

	db, ctx, cleanup := testdb.SetupTestDB(t)
	defer cleanup()

	// Verify connection works
	var version string
	err := db.Raw("SELECT version()").Scan(&version).Error
	require.NoError(t, err)
	require.Contains(t, version, "PostgreSQL")

	// Context should not have tenant
	_, ok := tenant.FromContext(ctx)
	assert.False(t, ok, "Context should not have tenant without WithTenant")
}

// TestSetupTestDB_WithTenant verifies tenant schema setup.
func TestSetupTestDB_WithTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}

	db, ctx, cleanup := testdb.SetupTestDB(t,
		testdb.WithTenant("test_tenant"),
	)
	defer cleanup()

	// Context should have tenant
	tid, ok := tenant.FromContext(ctx)
	require.True(t, ok, "Context should have tenant")
	assert.Equal(t, tenant.TenantID("test_tenant"), tid)

	// Verify tenant schema exists
	var schemaCount int
	err := db.Raw("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?",
		tid.SchemaName()).Scan(&schemaCount).Error
	require.NoError(t, err)
	assert.Equal(t, 1, schemaCount, "Tenant schema should exist")
}

type testEntity struct {
	ID   string `gorm:"primaryKey"`
	Name string `gorm:"size:255"`
}

func (testEntity) TableName() string { return "test_entity" }

// TestSetupTestDB_WithModelsAndTenant verifies model migration in tenant schema.
func TestSetupTestDB_WithModelsAndTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}

	db, _, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&testEntity{}),
		testdb.WithTenant("test_tenant"),
	)
	defer cleanup()

	// Verify table exists in tenant schema
	tid := tenant.TenantID("test_tenant")
	var tableCount int
	err := db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'test_entity'",
		tid.SchemaName()).Scan(&tableCount).Error
	require.NoError(t, err)
	assert.Equal(t, 1, tableCount, "Table should exist in tenant schema")

	// Verify table also exists in public schema (for cross-tenant fallback)
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'test_entity'").
		Scan(&tableCount).Error
	require.NoError(t, err)
	assert.Equal(t, 1, tableCount, "Table should also exist in public schema")

	// Verify we can insert and read
	err = db.Create(&testEntity{ID: "1", Name: "test"}).Error
	require.NoError(t, err)

	var result testEntity
	err = db.First(&result, "id = ?", "1").Error
	require.NoError(t, err)
	assert.Equal(t, "test", result.Name)
}

// TestSetupTestDB_WithAuditTables verifies audit table creation.
func TestSetupTestDB_WithAuditTables(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}

	db, _, cleanup := testdb.SetupTestDB(t,
		testdb.WithAuditTables(),
	)
	defer cleanup()

	// Verify audit_outbox table exists
	var tableCount int
	err := db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'audit_outbox'").
		Scan(&tableCount).Error
	require.NoError(t, err)
	assert.Equal(t, 1, tableCount, "audit_outbox table should exist")

	// Verify audit_log table exists
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'audit_log'").
		Scan(&tableCount).Error
	require.NoError(t, err)
	assert.Equal(t, 1, tableCount, "audit_log table should exist")
}
