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

	// Create platform schema
	err := db.Exec("CREATE SCHEMA IF NOT EXISTS platform").Error
	require.NoError(t, err)

	// Create tenants table (simplified for tests)
	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS platform.tenants (
			id VARCHAR(50) PRIMARY KEY,
			display_name VARCHAR(255) NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'active',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`).Error
	require.NoError(t, err)

	// Create tenant_provisioning table
	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS platform.tenant_provisioning (
			tenant_id VARCHAR(50) PRIMARY KEY REFERENCES platform.tenants(id) ON DELETE RESTRICT,
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
		"INSERT INTO platform.tenants (id, display_name) VALUES (?, ?)",
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
			{Name: "test-service", MigrationPath: svcDir},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Provision schemas
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
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
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "simple-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Provision twice
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err, "Second call should succeed (idempotent)")

	// Status should still be active
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
			{Name: "party", MigrationPath: partyDir},
			{Name: "current-account", MigrationPath: accountDir},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify both tables exist
	var partiesExists, accountsExists bool
	tc.db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = ? AND table_name = 'parties')`, tenantID.SchemaName()).Scan(&partiesExists)
	tc.db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = ? AND table_name = 'accounts')`, tenantID.SchemaName()).Scan(&accountsExists)

	assert.True(t, partiesExists, "parties table should exist")
	assert.True(t, accountsExists, "accounts table should exist")

	// Verify status shows both services
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "bad-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrMigrationFailed)

	// Status should be failed
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
		INSERT INTO platform.tenant_provisioning (tenant_id, state, service_schemas)
		VALUES (?, 'in_progress', '[]')
	`, tenantID.String())

	config := &Config{
		Services:            []ServiceConfig{{Name: "test", MigrationPath: tc.migDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "deprov-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Provision first
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Deprovision
	err = provisioner.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify status is deprovisioned (soft delete)
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "idem-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Provision and deprovision
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	err = provisioner.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Second deprovision should succeed (idempotent)
	err = provisioner.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "reprov-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Provision and then deprovision
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	err = provisioner.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Attempting to re-provision a deprovisioned tenant should fail
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrAlreadyDeprovisioned)

	// Status should remain deprovisioned
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "purge-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
		DataRetentionPeriod: 0, // No retention for test
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Provision
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Deprovision
	err = provisioner.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Purge
	err = provisioner.PurgeSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Schema should be gone
	var schemaExists bool
	tc.db.Raw("SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = ?)", tenantID.SchemaName()).Scan(&schemaExists)
	assert.False(t, schemaExists, "Schema should be dropped after purge")

	// Status record should be gone
	_, err = provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "active-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Provision but don't deprovision
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Purge should fail
	err = provisioner.PurgeSchemas(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "retained-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
		DataRetentionPeriod: 7 * 24 * time.Hour, // 7 days
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Provision and deprovision
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	err = provisioner.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Purge should fail - retention period not elapsed
	err = provisioner.PurgeSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrRetentionPeriodNotElapsed)
}

func TestPostgresProvisioner_GetProvisioningStatus_NotFound(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	svcDir := filepath.Join(tc.migDir, "dummy-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))

	config := &Config{
		Services:            []ServiceConfig{{Name: "dummy-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	tenantID := tenant.MustNewTenantID("unknown_tenant")
	_, err = provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "slow-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 500 * time.Millisecond, // Short timeout to trigger before pg_sleep(5) completes
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = provisioner.ProvisionSchemas(ctx, tenantID)
	assert.Error(t, err) // Should timeout or get context error
}

func TestPostgresProvisioner_ProvisionSchemas_NoMigrationDirectory(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("no_mig_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	config := &Config{
		Services:            []ServiceConfig{{Name: "missing-service", MigrationPath: "/nonexistent/path"}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Should succeed - missing migrations directory is valid (creates empty schema)
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "multi-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
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
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
		Services:            []ServiceConfig{{Name: "isolated-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Provision both tenants
	err = provisioner.ProvisionSchemas(context.Background(), tenant1)
	require.NoError(t, err)

	err = provisioner.ProvisionSchemas(context.Background(), tenant2)
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
		Services:            []ServiceConfig{{Name: "retry-service", MigrationPath: svcDir}},
		ProvisioningTimeout: 30 * time.Second,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// First attempt fails
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	assert.Error(t, err)

	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateFailed, status.State)

	// Fix the migration
	createTestMigration(t, svcDir, "20251201000000_broken.sql", `
		CREATE TABLE IF NOT EXISTS retry_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	// Retry should succeed
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	status, err = provisioner.GetProvisioningStatus(context.Background(), tenantID)
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
