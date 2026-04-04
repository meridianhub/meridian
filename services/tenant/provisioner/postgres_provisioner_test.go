package provisioner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/tenant/observability"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
	quotedSchema1 := pq.QuoteIdentifier(tenant1.SchemaName())
	quotedSchema2 := pq.QuoteIdentifier(tenant2.SchemaName())
	tc.db.Exec(fmt.Sprintf(`INSERT INTO %s.isolated_data (value) VALUES ('tenant1_data')`, quotedSchema1))

	// Insert different data into tenant2's schema
	tc.db.Exec(fmt.Sprintf(`INSERT INTO %s.isolated_data (value) VALUES ('tenant2_data')`, quotedSchema2))

	// Verify isolation - each tenant should only see their own data
	var count1, count2 int64
	tc.db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM %s.isolated_data WHERE value = 'tenant1_data'`, quotedSchema1)).Scan(&count1)
	tc.db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM %s.isolated_data WHERE value = 'tenant1_data'`, quotedSchema2)).Scan(&count2)

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

// =============================================================================
// Circuit Breaker Integration Tests
// =============================================================================

// TestPostgresProvisioner_CircuitBreaker_AllServicesHealthy tests that all services
// are provisioned successfully when all are healthy.
func TestPostgresProvisioner_CircuitBreaker_AllServicesHealthy(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("cb_healthy")
	createTestTenant(t, tc.db, tenantID.String())

	// Create migrations for two services
	partyDir := filepath.Join(tc.migDir, "party")
	require.NoError(t, os.MkdirAll(partyDir, 0o755))
	createTestMigration(t, partyDir, "20251201000000_initial.sql", `
		CREATE TABLE parties (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	accountDir := filepath.Join(tc.migDir, "current-account")
	require.NoError(t, os.MkdirAll(accountDir, 0o755))
	createTestMigration(t, accountDir, "20251201000000_initial.sql", `
		CREATE TABLE accounts (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
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

	// Provision tenant - all services should succeed
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify both services were migrated
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
	assert.Len(t, status.Services, 2)
	for _, svc := range status.Services {
		assert.Equal(t, ServiceStateMigrated, svc.State)
	}
}

// TestPostgresProvisioner_CircuitBreaker_FailingServiceTripsBreaker tests that
// consecutive failures trip the circuit breaker for that service.
func TestPostgresProvisioner_CircuitBreaker_FailingServiceTripsBreaker(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Create migrations - party has broken migration, account is fine
	partyDir := filepath.Join(tc.migDir, "party")
	require.NoError(t, os.MkdirAll(partyDir, 0o755))
	createTestMigration(t, partyDir, "20251201000000_broken.sql", `
		INVALID SQL SYNTAX PARTY;
	`)

	accountDir := filepath.Join(tc.migDir, "current-account")
	require.NoError(t, os.MkdirAll(accountDir, 0o755))
	createTestMigration(t, accountDir, "20251201000000_initial.sql", `
		CREATE TABLE accounts (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
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

	// Create multiple tenants to trip the circuit breaker
	// BreakerMinRequests = 5, BreakerFailureRatio = 0.6 (60%)
	// We need 5 failures to potentially trip the breaker
	for i := 0; i < 5; i++ {
		tenantID := tenant.MustNewTenantID(fmt.Sprintf("cb_fail_%d", i))
		createTestTenant(t, tc.db, tenantID.String())

		err = prov.ProvisionSchemas(context.Background(), tenantID)
		// Should fail due to broken party migration
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrMigrationFailed)
	}

	// Now the circuit breaker for party should be open
	// Next tenant provisioning should skip party (circuit open) but continue with account
	tenantID := tenant.MustNewTenantID("cb_after_trip")
	createTestTenant(t, tc.db, tenantID.String())

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	// Should return circuit breaker error because party is skipped
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCircuitBreakerOpen)

	// Verify the tenant status shows party with circuit_open state
	// and the overall state is failed
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateFailed, status.State)

	// Check party service status has circuit_open state and includes retry timing
	for _, svc := range status.Services {
		if svc.ServiceName == "party" {
			assert.Equal(t, ServiceStateCircuitOpen, svc.State, "Party service should have circuit_open state")
			assert.Contains(t, svc.ErrorMessage, "circuit breaker open")
			assert.Contains(t, svc.ErrorMessage, "Retry after", "Error message should include retry timing")
		}
	}
}

// TestPostgresProvisioner_CircuitBreaker_OtherServicesContinue tests that when
// one service's circuit breaker is open, other services continue provisioning.
func TestPostgresProvisioner_CircuitBreaker_OtherServicesContinue(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Create migrations - party has broken migration, customer and account are fine
	partyDir := filepath.Join(tc.migDir, "party")
	require.NoError(t, os.MkdirAll(partyDir, 0o755))
	createTestMigration(t, partyDir, "20251201000000_broken.sql", `
		INVALID SQL;
	`)

	customerDir := filepath.Join(tc.migDir, "customer")
	require.NoError(t, os.MkdirAll(customerDir, 0o755))
	createTestMigration(t, customerDir, "20251201000000_initial.sql", `
		CREATE TABLE customers (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	accountDir := filepath.Join(tc.migDir, "current-account")
	require.NoError(t, os.MkdirAll(accountDir, 0o755))
	createTestMigration(t, accountDir, "20251201000000_initial.sql", `
		CREATE TABLE accounts (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services: []ServiceConfig{
			{Name: "party", MigrationPath: partyDir, DatabaseURL: tc.connStr},
			{Name: "customer", MigrationPath: customerDir, DatabaseURL: tc.connStr},
			{Name: "current-account", MigrationPath: accountDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Trip the circuit breaker for party with 5 failures
	for i := 0; i < 5; i++ {
		tenantID := tenant.MustNewTenantID(fmt.Sprintf("trip_cb_%d", i))
		createTestTenant(t, tc.db, tenantID.String())
		_ = prov.ProvisionSchemas(context.Background(), tenantID)
	}

	// Now provision a new tenant - party circuit is open, but customer and account should work
	tenantID := tenant.MustNewTenantID("continue_others")
	createTestTenant(t, tc.db, tenantID.String())

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	// Overall fails because party was skipped
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCircuitBreakerOpen)

	// But verify that customer and current-account tables were created
	var customerExists, accountExists bool
	tc.db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = ? AND table_name = 'customers')`, tenantID.SchemaName()).Scan(&customerExists)
	tc.db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = ? AND table_name = 'accounts')`, tenantID.SchemaName()).Scan(&accountExists)

	assert.True(t, customerExists, "customer table should exist - service continued despite party failure")
	assert.True(t, accountExists, "accounts table should exist - service continued despite party failure")
}

// TestPostgresProvisioner_CircuitBreaker_BreakerStaysClosedBelowThreshold tests that
// the circuit breaker stays closed when failure rate is below threshold.
func TestPostgresProvisioner_CircuitBreaker_BreakerStaysClosedBelowThreshold(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Create a migration that sometimes succeeds
	svcDir := filepath.Join(tc.migDir, "mixed-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_initial.sql", `
		CREATE TABLE mixed_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services: []ServiceConfig{
			{Name: "mixed-service", MigrationPath: svcDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Create 10 successful provisioning attempts
	// With 0% failure rate, breaker should stay closed
	for i := 0; i < 10; i++ {
		tenantID := tenant.MustNewTenantID(fmt.Sprintf("success_%d", i))
		createTestTenant(t, tc.db, tenantID.String())

		err = prov.ProvisionSchemas(context.Background(), tenantID)
		require.NoError(t, err)

		status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, StateActive, status.State)
	}

	// 11th attempt should also succeed - breaker is closed
	tenantID := tenant.MustNewTenantID("final_success")
	createTestTenant(t, tc.db, tenantID.String())

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)
}

// TestPostgresProvisioner_CircuitBreaker_StatusTracking tests that circuit breaker
// state is properly reflected in provisioning_status with descriptive error messages.
func TestPostgresProvisioner_CircuitBreaker_StatusTracking(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Create migrations - party has broken migration, account and customer are fine
	partyDir := filepath.Join(tc.migDir, "party")
	require.NoError(t, os.MkdirAll(partyDir, 0o755))
	createTestMigration(t, partyDir, "20251201000000_broken.sql", `
		INVALID SQL PARTY;
	`)

	accountDir := filepath.Join(tc.migDir, "current-account")
	require.NoError(t, os.MkdirAll(accountDir, 0o755))
	createTestMigration(t, accountDir, "20251201000000_initial.sql", `
		CREATE TABLE accounts (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	customerDir := filepath.Join(tc.migDir, "customer")
	require.NoError(t, os.MkdirAll(customerDir, 0o755))
	createTestMigration(t, customerDir, "20251201000000_initial.sql", `
		CREATE TABLE customers (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services: []ServiceConfig{
			{Name: "party", MigrationPath: partyDir, DatabaseURL: tc.connStr},
			{Name: "current-account", MigrationPath: accountDir, DatabaseURL: tc.connStr},
			{Name: "customer", MigrationPath: customerDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Trip the circuit breaker for party service with 5 failures
	for i := 0; i < 5; i++ {
		tenantID := tenant.MustNewTenantID(fmt.Sprintf("trip_%d", i))
		createTestTenant(t, tc.db, tenantID.String())
		_ = prov.ProvisionSchemas(context.Background(), tenantID)
	}

	// Provision a new tenant - party circuit is open
	tenantID := tenant.MustNewTenantID("status_tracking")
	createTestTenant(t, tc.db, tenantID.String())

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCircuitBreakerOpen)

	// Get provisioning status and verify circuit breaker state is visible
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateFailed, status.State, "Overall state should be failed due to circuit breaker")

	// Verify per-service status
	serviceStates := make(map[string]ServiceSchemaStatus)
	for _, svc := range status.Services {
		serviceStates[svc.ServiceName] = svc
	}

	// Party should have circuit_open state
	partyStatus := serviceStates["party"]
	assert.Equal(t, ServiceStateCircuitOpen, partyStatus.State, "Party should be in circuit_open state")
	assert.Contains(t, partyStatus.ErrorMessage, "circuit breaker open", "Error should indicate circuit breaker is open")
	assert.Contains(t, partyStatus.ErrorMessage, "Retry after", "Error should include next retry time")

	// Other services should still be migrated (since they come after party in the list)
	// Note: This depends on processing order - services after an open circuit still get processed
	accountStatus := serviceStates["current-account"]
	assert.Equal(t, ServiceStateMigrated, accountStatus.State, "current-account should be migrated")

	customerStatus := serviceStates["customer"]
	assert.Equal(t, ServiceStateMigrated, customerStatus.State, "customer should be migrated")
}

// TestPostgresProvisioner_CircuitBreaker_PartialSuccessWithCircuitOpen tests that
// when one service has open circuit, others still complete and status reflects this.
func TestPostgresProvisioner_CircuitBreaker_PartialSuccessWithCircuitOpen(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Setup: party fails, others succeed
	partyDir := filepath.Join(tc.migDir, "party")
	require.NoError(t, os.MkdirAll(partyDir, 0o755))
	createTestMigration(t, partyDir, "20251201000000_broken.sql", `INVALID;`)

	accountDir := filepath.Join(tc.migDir, "current-account")
	require.NoError(t, os.MkdirAll(accountDir, 0o755))
	createTestMigration(t, accountDir, "20251201000000_ok.sql", `
		CREATE TABLE accounts (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
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

	// Trip the circuit breaker
	for i := 0; i < 5; i++ {
		tid := tenant.MustNewTenantID(fmt.Sprintf("partial_%d", i))
		createTestTenant(t, tc.db, tid.String())
		_ = prov.ProvisionSchemas(context.Background(), tid)
	}

	// New tenant with open circuit for party
	tenantID := tenant.MustNewTenantID("partial_final")
	createTestTenant(t, tc.db, tenantID.String())

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.Error(t, err)

	// Verify status shows partial success pattern
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)

	var circuitOpenCount, migratedCount int
	for _, svc := range status.Services {
		switch svc.State {
		case ServiceStateCircuitOpen:
			circuitOpenCount++
		case ServiceStateMigrated:
			migratedCount++
		case ServiceStatePending, ServiceStateCreated, ServiceStateFailed:
			// Not expected in this test, but required for exhaustive switch
		}
	}

	assert.Equal(t, 1, circuitOpenCount, "One service should have circuit_open state")
	assert.Equal(t, 1, migratedCount, "One service should be migrated despite other's circuit open")
}

// =============================================================================
// Circuit Breaker Observability Tests
// =============================================================================

// TestPostgresProvisioner_GetCircuitBreakerState tests the observability method
// for monitoring circuit breaker state.
func TestPostgresProvisioner_GetCircuitBreakerState(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("cb_observe")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "observe-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_initial.sql", `
		CREATE TABLE observe_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services: []ServiceConfig{
			{Name: "observe-service", MigrationPath: svcDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Before any provisioning, circuit breaker should not exist
	state := prov.GetCircuitBreakerState("observe-service")
	assert.Nil(t, state, "circuit breaker should not exist before first provisioning")

	// Provision a tenant to trigger circuit breaker creation
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Now circuit breaker should exist and be closed
	state = prov.GetCircuitBreakerState("observe-service")
	require.NotNil(t, state)
	assert.Equal(t, "observe-service", state.ServiceName)
	assert.Equal(t, "closed", state.State)
	assert.Equal(t, uint32(1), state.Counts.TotalSuccesses)
}

// TestPostgresProvisioner_GetCircuitBreakerStates tests getting all circuit breaker states.
func TestPostgresProvisioner_GetCircuitBreakerStates(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("cb_all_states")
	createTestTenant(t, tc.db, tenantID.String())

	// Create multiple services
	svc1Dir := filepath.Join(tc.migDir, "svc1")
	require.NoError(t, os.MkdirAll(svc1Dir, 0o755))
	createTestMigration(t, svc1Dir, "20251201000000_initial.sql", `
		CREATE TABLE svc1_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	svc2Dir := filepath.Join(tc.migDir, "svc2")
	require.NoError(t, os.MkdirAll(svc2Dir, 0o755))
	createTestMigration(t, svc2Dir, "20251201000000_initial.sql", `
		CREATE TABLE svc2_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services: []ServiceConfig{
			{Name: "svc1", MigrationPath: svc1Dir, DatabaseURL: tc.connStr},
			{Name: "svc2", MigrationPath: svc2Dir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Before provisioning
	states := prov.GetCircuitBreakerStates()
	assert.Empty(t, states)

	// After provisioning
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	states = prov.GetCircuitBreakerStates()
	assert.Len(t, states, 2)

	// Verify both services are present
	serviceNames := make(map[string]bool)
	for _, state := range states {
		serviceNames[state.ServiceName] = true
		assert.Equal(t, "closed", state.State)
	}
	assert.True(t, serviceNames["svc1"])
	assert.True(t, serviceNames["svc2"])
}

// TestPostgresProvisioner_CircuitBreaker_OpenStateVisible tests that open circuit
// breaker state is visible through the observability API.
func TestPostgresProvisioner_CircuitBreaker_OpenStateVisible(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Create migrations - broken service
	brokenDir := filepath.Join(tc.migDir, "broken-service")
	require.NoError(t, os.MkdirAll(brokenDir, 0o755))
	createTestMigration(t, brokenDir, "20251201000000_broken.sql", `INVALID SQL;`)

	config := &Config{
		Services: []ServiceConfig{
			{Name: "broken-service", MigrationPath: brokenDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Trip the circuit breaker with 5 failures
	for i := 0; i < 5; i++ {
		tenantID := tenant.MustNewTenantID(fmt.Sprintf("cb_trip_%d", i))
		createTestTenant(t, tc.db, tenantID.String())
		_ = prov.ProvisionSchemas(context.Background(), tenantID)
	}

	// Verify circuit breaker is open
	state := prov.GetCircuitBreakerState("broken-service")
	require.NotNil(t, state)
	assert.Equal(t, "open", state.State)
	// Note: Counts are reset when breaker transitions to open state
	// The key assertion is that the state is "open"
}

// =============================================================================
// FilterMigrationsAfter Helper Tests
// =============================================================================

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

// =============================================================================
// Service Failure Observability Tests
// =============================================================================

// TestPostgresProvisioner_MigrationFailure_IncrementsServiceFailureMetric verifies that
// when a service migration fails, the observability.IncrementServiceFailure metric is called.
func TestPostgresProvisioner_MigrationFailure_IncrementsServiceFailureMetric(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	// Create tenant
	tenantID := tenant.MustNewTenantID("metric_test_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	// Create a migration directory with invalid SQL that will fail
	failingDir := filepath.Join(tc.migDir, "failing-service")
	require.NoError(t, os.MkdirAll(failingDir, 0o755))
	createTestMigration(t, failingDir, "20251201000000_broken.sql", `
		THIS IS NOT VALID SQL AND WILL FAIL;
	`)

	config := &Config{
		Services: []ServiceConfig{
			{Name: "failing-service", MigrationPath: failingDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Get initial metric value for the specific service label
	counter := observability.GetServiceProvisioningFailuresMetric().WithLabelValues("failing-service")
	initialVal := testutil.ToFloat64(counter)

	// Attempt provisioning - should fail
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.Error(t, err, "Provisioning should fail with invalid SQL")
	assert.ErrorIs(t, err, ErrMigrationFailed)

	// Verify the service failure metric was incremented
	finalVal := testutil.ToFloat64(counter)
	assert.Equal(t, initialVal+1, finalVal, "Service failure metric should be incremented on migration failure")
}

func TestPostgresProvisioner_InitializeProvisioningStatus(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("init_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "init-svc")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20260101000000_init.sql", "CREATE TABLE init_t (id UUID PRIMARY KEY);")

	config := &Config{
		Services: []ServiceConfig{
			{Name: "init-svc", MigrationPath: svcDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Initialize status
	err = prov.InitializeProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify status is pending
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StatePending, status.State)
	assert.Len(t, status.Services, 1)

	// Idempotent: calling again should be a no-op
	err = prov.InitializeProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)

	status2, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StatePending, status2.State)
}

func TestPostgresProvisioner_Close(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	svcDir := filepath.Join(tc.migDir, "close-svc")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20260101000000_init.sql", "CREATE TABLE close_t (id UUID PRIMARY KEY);")

	config := &Config{
		Services: []ServiceConfig{
			{Name: "close-svc", MigrationPath: svcDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)

	// Close should succeed
	err = prov.Close()
	require.NoError(t, err)
}

func TestPostgresProvisioner_GetRequiredSchemas(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	svcDir1 := filepath.Join(tc.migDir, "svc-a")
	svcDir2 := filepath.Join(tc.migDir, "svc-b")
	require.NoError(t, os.MkdirAll(svcDir1, 0o755))
	require.NoError(t, os.MkdirAll(svcDir2, 0o755))
	createTestMigration(t, svcDir1, "20260101000000_init.sql", "CREATE TABLE a_t (id UUID PRIMARY KEY);")
	createTestMigration(t, svcDir2, "20260101000000_init.sql", "CREATE TABLE b_t (id UUID PRIMARY KEY);")

	config := &Config{
		Services: []ServiceConfig{
			{Name: "svc-a", MigrationPath: svcDir1, DatabaseURL: tc.connStr},
			{Name: "svc-b", MigrationPath: svcDir2, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	schemas := prov.GetRequiredSchemas()
	assert.Equal(t, []string{"svc-a", "svc-b"}, schemas)
}

func TestPostgresProvisioner_PostProvisioningHooks(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("hook_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "hook-svc")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20260101000000_init.sql", "CREATE TABLE hook_t (id UUID PRIMARY KEY);")

	var hookCalled bool
	config := &Config{
		Services: []ServiceConfig{
			{Name: "hook-svc", MigrationPath: svcDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
		PostProvisioningHooks: []PostProvisioningHook{
			func(_ context.Context, tid tenant.TenantID) error {
				hookCalled = true
				assert.Equal(t, tenantID, tid)
				return nil
			},
		},
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)
	assert.True(t, hookCalled, "Post-provisioning hook should be called")
}

func TestPostgresProvisioner_PostProvisioningHooks_FailureDoesNotFailProvisioning(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("hook_fail_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "hookfail-svc")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20260101000000_init.sql", "CREATE TABLE hookfail_t (id UUID PRIMARY KEY);")

	config := &Config{
		Services: []ServiceConfig{
			{Name: "hookfail-svc", MigrationPath: svcDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
		PostProvisioningHooks: []PostProvisioningHook{
			func(_ context.Context, _ tenant.TenantID) error {
				return fmt.Errorf("hook failure")
			},
		},
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provisioning should succeed despite hook failure
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
}

func TestPostgresProvisioner_PostProvisioningHooks_PanicRecovery(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("hook_panic_tenant")
	createTestTenant(t, tc.db, tenantID.String())

	svcDir := filepath.Join(tc.migDir, "hookpanic-svc")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20260101000000_init.sql", "CREATE TABLE hookpanic_t (id UUID PRIMARY KEY);")

	config := &Config{
		Services: []ServiceConfig{
			{Name: "hookpanic-svc", MigrationPath: svcDir, DatabaseURL: tc.connStr},
		},
		ProvisioningTimeout: 30 * time.Second,
		PostProvisioningHooks: []PostProvisioningHook{
			func(_ context.Context, _ tenant.TenantID) error {
				panic("unexpected panic in hook")
			},
		},
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provisioning should succeed despite hook panic
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
}

func TestPostgresProvisioner_VerifySchemaProvisioned_SentinelTable(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("sentinel_test")
	createTestTenant(t, tc.db, tenantID.String())

	// Create migration that creates a specific table
	svcDir := filepath.Join(tc.migDir, "sentinel-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_init.sql", `
		CREATE TABLE my_sentinel (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services: []ServiceConfig{
			{
				Name:          "sentinel-service",
				MigrationPath: svcDir,
				DatabaseURL:   tc.connStr,
				SentinelTable: "my_sentinel",
			},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision should succeed - sentinel table is created by migration
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
}

func TestPostgresProvisioner_VerifySchemaProvisioned_MissingSentinelTable(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("missing_sentinel")
	createTestTenant(t, tc.db, tenantID.String())

	// Create migration that creates a table with a DIFFERENT name than the sentinel
	svcDir := filepath.Join(tc.migDir, "wrong-table-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_init.sql", `
		CREATE TABLE some_other_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services: []ServiceConfig{
			{
				Name:          "wrong-table-service",
				MigrationPath: svcDir,
				DatabaseURL:   tc.connStr,
				SentinelTable: "expected_table_that_doesnt_exist",
			},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Provision should fail verification - sentinel table doesn't exist
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSchemaVerificationFailed)

	// Status should be failed
	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateFailed, status.State)
	assert.Contains(t, status.ErrorMessage, "expected_table_that_doesnt_exist")
}

func TestPostgresProvisioner_VerifySchemaProvisioned_NoSentinelWithTables(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("no_sentinel_ok")
	createTestTenant(t, tc.db, tenantID.String())

	// Service with no sentinel table configured but has migrations
	svcDir := filepath.Join(tc.migDir, "no-sentinel-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	createTestMigration(t, svcDir, "20251201000000_init.sql", `
		CREATE TABLE any_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`)

	config := &Config{
		Services: []ServiceConfig{
			{
				Name:          "no-sentinel-service",
				MigrationPath: svcDir,
				DatabaseURL:   tc.connStr,
				// SentinelTable intentionally empty
			},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Should succeed - no sentinel check, tables exist
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
}

func TestPostgresProvisioner_VerifySchemaProvisioned_EmptySchemaNoSentinel(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("empty_schema")
	createTestTenant(t, tc.db, tenantID.String())

	// Service with no sentinel and no migrations - should succeed (e.g., internal-account)
	svcDir := filepath.Join(tc.migDir, "empty-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	// No migration files

	config := &Config{
		Services: []ServiceConfig{
			{
				Name:          "empty-service",
				MigrationPath: svcDir,
				DatabaseURL:   tc.connStr,
				// SentinelTable intentionally empty
			},
		},
		ProvisioningTimeout: 30 * time.Second,
	}

	prov, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer prov.Close()

	// Should succeed - empty service with no sentinel is OK
	err = prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
}

func TestNewPostgresProvisioner_NilPlatformDB(t *testing.T) {
	config := &Config{
		Services: []ServiceConfig{
			{Name: "svc", MigrationPath: "/tmp", DatabaseURL: "postgres://localhost/test"},
		},
	}
	_, err := NewPostgresProvisioner(nil, config)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilPlatformDB)
}
