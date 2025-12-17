package provisioner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormPG "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testContainer holds the PostgreSQL container and database connection for tests.
type testContainer struct {
	container *postgres.PostgresContainer
	db        *gorm.DB
	connStr   string // Connection string for service databases
	migDir    string // Temporary directory for test migrations
}

// setupTestContainer creates a PostgreSQL container with the platform schema.
func setupTestContainer(t *testing.T) *testContainer {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_tenant"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(30*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Create GORM connection
	db, err := gorm.Open(gormPG.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "Failed to create GORM connection")

	// Create platform schema and tenant_provisioning table
	setupPlatformSchema(t, db)

	// Create temporary directory for test migrations
	migDir, err := os.MkdirTemp("", "test_migrations_*")
	require.NoError(t, err, "Failed to create temp directory")

	return &testContainer{
		container: pgContainer,
		db:        db,
		connStr:   connStr,
		migDir:    migDir,
	}
}

// cleanup terminates the container and cleans up resources.
func (tc *testContainer) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	// Remove temp directory
	if tc.migDir != "" {
		os.RemoveAll(tc.migDir)
	}

	// Close DB connection
	if tc.db != nil {
		sqlDB, _ := tc.db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}

	// Terminate container
	if tc.container != nil {
		require.NoError(t, tc.container.Terminate(ctx), "Failed to terminate container")
	}
}

// setupPlatformSchema creates the platform schema and required tables.
func setupPlatformSchema(t *testing.T, db *gorm.DB) {
	t.Helper()

	// Create tenant table (singular, unqualified - matches migration)
	err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tenant (
			id VARCHAR(50) PRIMARY KEY,
			display_name VARCHAR(255) NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'active',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`).Error
	require.NoError(t, err)

	// Create tenant_provisioning table (singular, unqualified - matches migration)
	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS tenant_provisioning (
			tenant_id VARCHAR(50) PRIMARY KEY REFERENCES tenant(id) ON DELETE RESTRICT,
			state VARCHAR(20) NOT NULL DEFAULT 'pending',
			service_schemas JSONB NOT NULL DEFAULT '[]',
			error_message TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deprovisioned_at TIMESTAMPTZ,
			version INTEGER NOT NULL DEFAULT 1,
			CONSTRAINT valid_provisioning_state CHECK (state IN ('pending', 'in_progress', 'active', 'failed', 'deprovisioned'))
		)
	`).Error
	require.NoError(t, err)
}

// createTestTenant creates a tenant record for testing.
func createTestTenant(t *testing.T, db *gorm.DB, tenantID string) {
	t.Helper()
	err := db.Exec(
		"INSERT INTO tenant (id, display_name) VALUES (?, ?)",
		tenantID, "Test Tenant "+tenantID,
	).Error
	require.NoError(t, err)
}

// createTestMigration creates a migration file in the temp directory.
func createTestMigration(t *testing.T, dir, filename, content string) {
	t.Helper()
	path := filepath.Join(dir, filename)
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err)
}

func TestPostgresProvisioner_ProvisionSchemas(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Create test tenant
	tenantID := tenant.MustNewTenantID("acme_corp")
	createTestTenant(t, tc.db, tenantID.String())

	// Create test migrations
	svcDir := filepath.Join(tc.migDir, "test-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_initial.sql", `
		CREATE TABLE test_table (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(100) NOT NULL
		);
		CREATE INDEX idx_test_table_name ON test_table(name);
	`)

	config := &Config{
		Services: []ServiceConfig{
			{Name: "test-service", MigrationPath: svcDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision schemas
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify schema was created
	var schemaExists bool
	tc.db.Raw("SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = ?)", tenantID.SchemaName()).Scan(&schemaExists)
	assert.True(t, schemaExists, "Schema should exist")

	// Verify table was created in tenant schema
	var tableExists bool
	tc.db.Raw(`
		SELECT EXISTS(
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = ? AND table_name = 'test_table'
		)
	`, tenantID.SchemaName()).Scan(&tableExists)
	assert.True(t, tableExists, "Table should exist in tenant schema")

	// Verify provisioning status
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
	assert.Len(t, status.Services, 1)
	assert.Equal(t, ServiceStateMigrated, status.Services[0].State)
	assert.Equal(t, "20251201000000", status.Services[0].MigrationVersion)
}

func TestPostgresProvisioner_ProvisionSchemas_Idempotent(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("beta_inc")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "simple-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_tables.sql", `
		CREATE TABLE items (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "simple-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision twice
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err, "Second call should succeed (idempotent)")

	// Status should still be active
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
}

func TestPostgresProvisioner_ProvisionSchemas_MultipleServices(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("gamma_ltd")
	createTestTenant(t, tc.db, tenantID.String())

	// Create migrations for two services
	partyDir := filepath.Join(tc.migDir, "party")
	require.NoError(t, os.MkdirAll(partyDir, 0o755))
	createTestMigration(t, partyDir, "20251201000000_initial.sql", `
		CREATE TABLE parties (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(255) NOT NULL
		);
	`)

	accountDir := filepath.Join(tc.migDir, "current-account")
	require.NoError(t, os.MkdirAll(accountDir, 0o755))
	createTestMigration(t, accountDir, "20251201000000_initial.sql", `
		CREATE TABLE accounts (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			account_number VARCHAR(34) NOT NULL
		);
	`)

	config := &Config{
		Services: []ServiceConfig{
			{Name: "party", MigrationPath: partyDir, DatabaseURL: tc.connStr},
			{Name: "current-account", MigrationPath: accountDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify both tables exist
	var partiesExists, accountsExists bool
	tc.db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = ? AND table_name = 'parties')`, tenantID.SchemaName()).Scan(&partiesExists)
	tc.db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = ? AND table_name = 'accounts')`, tenantID.SchemaName()).Scan(&accountsExists)

	assert.True(t, partiesExists, "parties table should exist")
	assert.True(t, accountsExists, "accounts table should exist")

	// Verify status shows both services
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Len(t, status.Services, 2)
	for _, svc := range status.Services {
		assert.Equal(t, ServiceStateMigrated, svc.State)
	}
}

func TestPostgresProvisioner_ProvisionSchemas_MigrationFailure(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("failing_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "bad-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_broken.sql", `
		CREATE TABLE good_table (id UUID PRIMARY KEY);
		INVALID SQL SYNTAX HERE;
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "bad-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrMigrationFailed)

	// Status should be failed
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateFailed, status.State)
	assert.NotEmpty(t, status.ErrorMessage)
}

func TestPostgresProvisioner_ProvisionSchemas_ConcurrentBlocked(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("concurrent_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	// Pre-create an in_progress status to simulate concurrent provisioning
	tc.db.Exec(`
		INSERT INTO tenant_provisioning (tenant_id, state, service_schemas)
		VALUES (?, 'in_progress', '[]')
	`, tenantID.String())

	config := &Config{
		Services:            []ServiceConfig{{Name: "test", MigrationPath: tc.migDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrProvisioningInProgress)
}

func TestPostgresProvisioner_DeprovisionSchemas(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("deprov_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "deprov-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_init.sql", `
		CREATE TABLE data (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "deprov-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision first
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Deprovision
	err = prov.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify status is deprovisioned (soft delete)
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateDeprovisioned, status.State)
	assert.NotNil(t, status.DeprovisionedAt)

	// Schema should still exist (soft delete)
	var schemaExists bool
	tc.db.Raw("SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = ?)", tenantID.SchemaName()).Scan(&schemaExists)
	assert.True(t, schemaExists, "Schema should still exist after soft delete")
}

func TestPostgresProvisioner_DeprovisionSchemas_Idempotent(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("idem_deprov")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "idem-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))

	config := &Config{
		Services:            []ServiceConfig{{Name: "idem-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision and deprovision
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	err = prov.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Second deprovision should succeed (idempotent)
	err = prov.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateDeprovisioned, status.State)
}

func TestPostgresProvisioner_ProvisionSchemas_AfterDeprovisioned(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("reprov_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "reprov-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))

	config := &Config{
		Services:            []ServiceConfig{{Name: "reprov-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision and then deprovision
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	err = prov.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Attempting to re-provision a deprovisioned tenant should fail
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrAlreadyDeprovisioned)

	// Status should remain deprovisioned
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateDeprovisioned, status.State)
}

func TestPostgresProvisioner_PurgeSchemas(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("purge_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "purge-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_init.sql", `
		CREATE TABLE to_be_purged (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "purge-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
		DataRetentionPeriod: 0, // No retention for test
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Deprovision
	err = prov.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Purge
	err = prov.PurgeSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Schema should be gone
	var schemaExists bool
	tc.db.Raw("SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = ?)", tenantID.SchemaName()).Scan(&schemaExists)
	assert.False(t, schemaExists, "Schema should be dropped after purge")

	// Status record should be gone
	_, err = prov.GetProvisioningStatus(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrProvisioningStatusNotFound)
}

func TestPostgresProvisioner_PurgeSchemas_NotDeprovisioned(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("active_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "active-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))

	config := &Config{
		Services:            []ServiceConfig{{Name: "active-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision but don't deprovision
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Purge should fail
	err = prov.PurgeSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrNotDeprovisioned)
}

func TestPostgresProvisioner_PurgeSchemas_RetentionNotElapsed(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("retained_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "retained-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))

	config := &Config{
		Services:            []ServiceConfig{{Name: "retained-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
		DataRetentionPeriod: 7 * 24 * time.Hour, // 7 days
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision and deprovision
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	err = prov.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Purge should fail - retention period not elapsed
	err = prov.PurgeSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrRetentionPeriodNotElapsed)
}

func TestPostgresProvisioner_GetProvisioningStatus_NotFound(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	svcDir := filepath.Join(tc.migDir, "dummy-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))

	config := &Config{
		Services:            []ServiceConfig{{Name: "dummy-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	tenantID := tenant.MustNewTenantID("unknown_tenant")
	_, err = prov.GetProvisioningStatus(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrProvisioningStatusNotFound)
}

func TestPostgresProvisioner_ProvisionSchemas_Timeout(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("timeout_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	// Create a slow migration using pg_sleep
	svcDir := filepath.Join(tc.migDir, "slow-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_slow.sql", `
		SELECT pg_sleep(5);
		CREATE TABLE never_created (id UUID PRIMARY KEY);
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "slow-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 500 * time.Millisecond, // Short timeout to trigger before pg_sleep(5) completes
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = prov.ProvisionSchemas(ctx, tenantID)
	assert.Error(t, err) // Should timeout or get context error
}

func TestPostgresProvisioner_ProvisionSchemas_NoMigrationDirectory(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("no_mig_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	config := &Config{
		Services:            []ServiceConfig{{Name: "missing-service", MigrationPath: "/nonexistent/path", DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Should succeed - missing migrations directory is valid (creates empty schema)
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Schema should still be created
	var schemaExists bool
	tc.db.Raw("SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = ?)", tenantID.SchemaName()).Scan(&schemaExists)
	assert.True(t, schemaExists)
}

func TestPostgresProvisioner_ProvisionSchemas_MultipleMigrationFiles(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("multi_mig_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "multi-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))

	// Create multiple migrations
	createTestMigration(t, svcDir, "20251201000000_first.sql", `
		CREATE TABLE first_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)
	createTestMigration(t, svcDir, "20251202000000_second.sql", `
		CREATE TABLE second_table (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			first_id UUID REFERENCES first_table(id)
		);
	`)
	createTestMigration(t, svcDir, "20251203000000_third.sql", `
		ALTER TABLE second_table ADD COLUMN name VARCHAR(100);
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "multi-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify all migrations applied
	var firstExists, secondExists bool
	var hasNameColumn bool
	tc.db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = ? AND table_name = 'first_table')`, tenantID.SchemaName()).Scan(&firstExists)
	tc.db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = ? AND table_name = 'second_table')`, tenantID.SchemaName()).Scan(&secondExists)
	tc.db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = ? AND table_name = 'second_table' AND column_name = 'name')`, tenantID.SchemaName()).Scan(&hasNameColumn)

	assert.True(t, firstExists)
	assert.True(t, secondExists)
	assert.True(t, hasNameColumn)

	// Verify version is the last migration
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "20251203000000", status.Services[0].MigrationVersion)
}

func TestPostgresProvisioner_ProvisionSchemas_SchemaIsolation(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Create two tenants
	tenant1 := tenant.MustNewTenantID("tenant_1")
	tenant2 := tenant.MustNewTenantID("tenant_2")
	createTestTenant(t, tc.db, tenant1.String())
	createTestTenant(t, tc.db, tenant2.String())

	svcDir := filepath.Join(tc.migDir, "isolated-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_init.sql", `
		CREATE TABLE isolated_data (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			value TEXT NOT NULL
		);
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "isolated-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision both tenants
	err = prov.ProvisionSchemas(context.Background(), tenant1)
	require.NoError(t, err)

	err = prov.ProvisionSchemas(context.Background(), tenant2)
	require.NoError(t, err)

	// Insert data into tenant1's schema
	tc.db.Exec(fmt.Sprintf(`INSERT INTO %s.isolated_data (value) VALUES ('tenant1_data')`, tenant1.SchemaName()))

	// Insert different data into tenant2's schema
	tc.db.Exec(fmt.Sprintf(`INSERT INTO %s.isolated_data (value) VALUES ('tenant2_data')`, tenant2.SchemaName()))

	// Verify isolation - each tenant should only see their own data
	var count1, count2 int64
	tc.db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM %s.isolated_data WHERE value = 'tenant1_data'`, tenant1.SchemaName())).Scan(&count1)
	tc.db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM %s.isolated_data WHERE value = 'tenant1_data'`, tenant2.SchemaName())).Scan(&count2)

	assert.Equal(t, int64(1), count1, "tenant1 should have their data")
	assert.Equal(t, int64(0), count2, "tenant2 should not see tenant1's data")
}

func TestPostgresProvisioner_RetryAfterFailure(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("retry_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "retry-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))

	// First, create a broken migration
	createTestMigration(t, svcDir, "20251201000000_broken.sql", `
		CREATE TABLE retry_table (id UUID PRIMARY KEY);
		INVALID SYNTAX;
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "retry-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// First attempt fails
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	assert.Error(t, err)

	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateFailed, status.State)

	// Fix the migration
	createTestMigration(t, svcDir, "20251201000000_broken.sql", `
		CREATE TABLE IF NOT EXISTS retry_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	// Retry should succeed
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	status, err = prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
}

func TestNewPostgresProvisioner_InvalidConfig(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tests := []struct {
		name        string
		config      *Config
		expectedErr error
	}{
		{
			name: "no services configured",
			config: &Config{
				Services:            []ServiceConfig{},
				ProvisioningTimeout: 30 * time.Second,
			},
			expectedErr: ErrNoServicesConfigured,
		},
		{
			name: "invalid provisioning timeout",
			config: &Config{
				Services:            []ServiceConfig{{Name: "test", MigrationPath: "/tmp"}},
				ProvisioningTimeout: 0,
			},
			expectedErr: ErrInvalidProvisioningTimeout,
		},
		{
			name: "empty service name",
			config: &Config{
				Services:            []ServiceConfig{{Name: "", MigrationPath: "/tmp"}},
				ProvisioningTimeout: 30 * time.Second,
			},
			expectedErr: ErrEmptyServiceName,
		},
		{
			name: "empty migration path",
			config: &Config{
				Services:            []ServiceConfig{{Name: "test", MigrationPath: ""}},
				ProvisioningTimeout: 30 * time.Second,
			},
			expectedErr: ErrEmptyMigrationPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPostgresProvisioner(tc.db, tt.config)
			assert.Error(t, err)
			assert.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestQuoteIdentifier(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", `"simple"`},
		{"with_underscore", `"with_underscore"`},
		{"org_acme_bank", `"org_acme_bank"`},
		{`with"quote`, `"with""quote"`},
		{`multi"ple"quotes`, `"multi""ple""quotes"`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := quoteIdentifier(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestPostgresProvisioner_ReconcileMigrations tests the migration reconciliation feature.
func TestPostgresProvisioner_ReconcileMigrations(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Create test tenant
	tenantID := tenant.MustNewTenantID("reconcile_test")
	createTestTenant(t, tc.db, tenantID.String())

	// Create initial migrations
	svcDir := filepath.Join(tc.migDir, "reconcile-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_initial.sql", `
		CREATE TABLE initial_table (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(100) NOT NULL
		);
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "reconcile-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Initial provisioning
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify initial state
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "20251201000000", status.Services[0].MigrationVersion)

	// Add a new migration
	createTestMigration(t, svcDir, "20251202000000_add_column.sql", `
		ALTER TABLE initial_table ADD COLUMN description TEXT;
	`)

	// Reconcile migrations
	count, errs := prov.ReconcileMigrations(context.Background(), &tenantID)
	assert.Empty(t, errs, "Reconciliation should succeed without errors")
	assert.Equal(t, 1, count, "Should have reconciled 1 tenant")

	// Verify new migration version
	status, err = prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "20251202000000", status.Services[0].MigrationVersion)

	// Verify column was added
	var columnExists bool
	tc.db.Raw(`
		SELECT EXISTS(
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = ? AND table_name = 'initial_table' AND column_name = 'description'
		)
	`, tenantID.SchemaName()).Scan(&columnExists)
	assert.True(t, columnExists, "Column should exist after reconciliation")
}

// TestPostgresProvisioner_ReconcileMigrations_NoNewMigrations tests that reconciliation
// is a no-op when there are no new migrations.
func TestPostgresProvisioner_ReconcileMigrations_NoNewMigrations(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("no_changes")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "noop-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_only.sql", `
		CREATE TABLE only_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "noop-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Initial provisioning
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Reconcile - should be a no-op
	count, errs := prov.ReconcileMigrations(context.Background(), &tenantID)
	assert.Empty(t, errs)
	assert.Equal(t, 0, count, "No tenants should have been reconciled")

	// Version should remain unchanged
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "20251201000000", status.Services[0].MigrationVersion)
}

// TestPostgresProvisioner_ReconcileMigrations_AllTenants tests reconciling all active tenants.
func TestPostgresProvisioner_ReconcileMigrations_AllTenants(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Create multiple tenants
	tenant1 := tenant.MustNewTenantID("tenant_one")
	tenant2 := tenant.MustNewTenantID("tenant_two")
	createTestTenant(t, tc.db, tenant1.String())
	createTestTenant(t, tc.db, tenant2.String())

	svcDir := filepath.Join(tc.migDir, "multi-tenant-svc")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_base.sql", `
		CREATE TABLE base_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "multi-tenant-svc", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision both tenants
	err = prov.ProvisionSchemas(context.Background(), tenant1)
	require.NoError(t, err)
	err = prov.ProvisionSchemas(context.Background(), tenant2)
	require.NoError(t, err)

	// Add a new migration
	createTestMigration(t, svcDir, "20251203000000_add_index.sql", `
		CREATE INDEX idx_base_table_id ON base_table(id);
	`)

	// Reconcile all tenants (pass nil for tenantID)
	count, errs := prov.ReconcileMigrations(context.Background(), nil)
	assert.Empty(t, errs)
	assert.Equal(t, 2, count, "Should have reconciled 2 tenants")

	// Verify both tenants have new version
	status1, err := prov.GetProvisioningStatus(context.Background(), tenant1)
	require.NoError(t, err)
	assert.Equal(t, "20251203000000", status1.Services[0].MigrationVersion)

	status2, err := prov.GetProvisioningStatus(context.Background(), tenant2)
	require.NoError(t, err)
	assert.Equal(t, "20251203000000", status2.Services[0].MigrationVersion)
}

// TestPostgresProvisioner_ReconcileMigrations_SkipsNonActive tests that reconciliation
// skips tenants that are not in active state.
func TestPostgresProvisioner_ReconcileMigrations_SkipsNonActive(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("deprovisioned_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "skip-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_init.sql", `
		CREATE TABLE skip_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services:            []ServiceConfig{{Name: "skip-service", MigrationPath: svcDir, DatabaseURL: tc.connStr}},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision tenant
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Deprovision tenant
	err = prov.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Add new migration
	createTestMigration(t, svcDir, "20251202000000_new.sql", `
		ALTER TABLE skip_table ADD COLUMN foo TEXT;
	`)

	// Reconcile - should skip deprovisioned tenant
	count, errs := prov.ReconcileMigrations(context.Background(), &tenantID)
	assert.Empty(t, errs)
	assert.Equal(t, 0, count, "Deprovisioned tenant should be skipped")

	// Version should remain at initial
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "20251201000000", status.Services[0].MigrationVersion)
}

// TestFilterMigrationsAfter tests the filterMigrationsAfter helper function.
func TestFilterMigrationsAfter(t *testing.T) {
	tests := []struct {
		name           string
		migrations     []migration
		currentVersion string
		expectedCount  int
		expectedFirst  string
	}{
		{
			name: "filters older migrations",
			migrations: []migration{
				{Version: "20251201000000", Filename: "20251201000000_a.sql"},
				{Version: "20251202000000", Filename: "20251202000000_b.sql"},
				{Version: "20251203000000", Filename: "20251203000000_c.sql"},
			},
			currentVersion: "20251201000000",
			expectedCount:  2,
			expectedFirst:  "20251202000000",
		},
		{
			name: "returns empty when no newer migrations",
			migrations: []migration{
				{Version: "20251201000000", Filename: "20251201000000_a.sql"},
			},
			currentVersion: "20251201000000",
			expectedCount:  0,
		},
		{
			name: "returns empty for empty current version",
			migrations: []migration{
				{Version: "20251201000000", Filename: "20251201000000_a.sql"},
			},
			currentVersion: "",
			expectedCount:  0,
		},
		{
			name:           "handles empty migrations list",
			migrations:     []migration{},
			currentVersion: "20251201000000",
			expectedCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterMigrationsAfter(tt.migrations, tt.currentVersion)
			assert.Len(t, result, tt.expectedCount)
			if tt.expectedCount > 0 {
				assert.Equal(t, tt.expectedFirst, result[0].Version)
			}
		})
	}
}
