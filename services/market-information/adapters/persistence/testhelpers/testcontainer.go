// Package testhelpers provides shared utilities for repository integration tests.
package testhelpers

import (
	"context"
	_ "embed"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

//go:embed schema.sql
var schemaDDL string

// TestContainer holds the test database container, connection pool, and repository instances.
type TestContainer struct {
	container      *postgres.PostgresContainer
	Pool           *pgxpool.Pool
	Repos          *persistence.Repositories
	MasterTenantID tenant.TenantID // The master tenant used for shared/hierarchical data lookups
}

// SetupTestContainer creates a PostgreSQL testcontainer with the market_information schema loaded.
func SetupTestContainer(t *testing.T) *TestContainer {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_market_information"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(30*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to create connection pool")

	loadSchema(t, pool)
	pool = setupMasterSchema(ctx, t, pool, poolConfig)

	masterTenantID := "test_master"
	repos := persistence.NewRepositories(pool, masterTenantID)
	masterTID, err := tenant.NewTenantID(masterTenantID)
	require.NoError(t, err, "Failed to parse master tenant ID")

	return &TestContainer{
		container:      pgContainer,
		Pool:           pool,
		Repos:          repos,
		MasterTenantID: masterTID,
	}
}

// setupMasterSchema creates the master tenant schema with tables and sets it as the
// database default search_path. Returns a reconnected pool that uses the new default.
func setupMasterSchema(ctx context.Context, t *testing.T, pool *pgxpool.Pool, poolConfig *pgxpool.Config) *pgxpool.Pool {
	t.Helper()
	masterSchema := "org_test_master"
	quoted := pq.QuoteIdentifier(masterSchema)

	_, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quoted))
	require.NoError(t, err, "Failed to create master tenant schema")
	err = loadSchemaInSchema(ctx, pool, masterSchema)
	require.NoError(t, err, "Failed to load schema into master tenant schema")

	// Set default search_path so non-tenant-scoped queries use the master schema
	_, err = pool.Exec(ctx, fmt.Sprintf("ALTER DATABASE test_market_information SET search_path TO %s", quoted))
	require.NoError(t, err, "Failed to set default search_path")

	// Reconnect to pick up the new default search_path
	pool.Close()
	pool, err = pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to reconnect with new search_path")
	return pool
}

// Cleanup closes the connection pool and terminates the container.
func (tc *TestContainer) Cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if tc.Pool != nil {
		tc.Pool.Close()
	}

	if tc.container != nil {
		require.NoError(t, tc.container.Terminate(ctx), "Failed to terminate container")
	}
}

// loadSchema loads the complete market_information schema into the test database
// from the embedded schema.sql file.
func loadSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), schemaDDL)
	require.NoError(t, err, "Failed to load embedded schema DDL")
}

// CreateTenantSchema creates a tenant-specific schema for testing multi-tenant scenarios.
func (tc *TestContainer) CreateTenantSchema(tenantIDStr string) (tenant.TenantID, error) {
	ctx := context.Background()
	tenantID, err := tenant.NewTenantID(tenantIDStr)
	if err != nil {
		return tenant.TenantID(""), fmt.Errorf("invalid tenant ID: %w", err)
	}

	schemaName := tenantID.SchemaName()
	quotedSchema := pgx.Identifier{schemaName}.Sanitize()

	if _, err = tc.Pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quotedSchema); err != nil {
		return tenant.TenantID(""), fmt.Errorf("failed to create schema %s: %w", schemaName, err)
	}
	if err = loadSchemaInSchema(ctx, tc.Pool, schemaName); err != nil {
		return tenant.TenantID(""), fmt.Errorf("failed to load schema: %w", err)
	}

	// Copy reference data from master schema (where repos save it) to tenant schema
	masterSchema := pgx.Identifier{tc.MasterTenantID.SchemaName()}.Sanitize()
	if err = tc.copyReferenceData(ctx, quotedSchema, masterSchema); err != nil {
		return tenant.TenantID(""), err
	}

	return tenantID, nil
}

// copyReferenceData copies shared datasets and data sources from master to tenant schema.
func (tc *TestContainer) copyReferenceData(ctx context.Context, tenantSchema, masterSchema string) error {
	_, err := tc.Pool.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s.dataset_definition SELECT * FROM %s.dataset_definition WHERE is_shared = TRUE`,
		tenantSchema, masterSchema))
	if err != nil {
		return fmt.Errorf("failed to copy shared datasets: %w", err)
	}

	_, err = tc.Pool.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s.data_source SELECT * FROM %s.data_source`,
		tenantSchema, masterSchema))
	if err != nil {
		return fmt.Errorf("failed to copy data sources: %w", err)
	}
	return nil
}

// WithTenant wraps a context with tenant information.
func (tc *TestContainer) WithTenant(ctx context.Context, tenantID tenant.TenantID) context.Context {
	return tenant.WithTenant(ctx, tenantID)
}

// MasterContext returns a context scoped to the master tenant.
// Use this when saving reference data (datasets, data sources) that the observation
// repository's hierarchical lookup will query via the master tenant schema.
func (tc *TestContainer) MasterContext(ctx context.Context) context.Context {
	return tenant.WithTenant(ctx, tc.MasterTenantID)
}

// GrantTenantEntitlement grants access to a dataset for a tenant.
// Uses public.tenant_data_entitlements explicitly because the production code's
// checkTenantAccess queries public.tenant_data_entitlements directly.
func (tc *TestContainer) GrantTenantEntitlement(ctx context.Context, tenantID tenant.TenantID, datasetCode string, expiresAt *time.Time) error {
	query := `
		INSERT INTO public.tenant_data_entitlements (tenant_id, dataset_code, is_active, expires_at)
		VALUES ($1, $2, TRUE, $3)
		ON CONFLICT (tenant_id, dataset_code)
		DO UPDATE SET is_active = TRUE, expires_at = EXCLUDED.expires_at`

	_, err := tc.Pool.Exec(ctx, query, tenantID.String(), datasetCode, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to grant entitlement: %w", err)
	}
	return nil
}

// RevokeTenantEntitlement revokes access to a dataset for a tenant.
// Uses public.tenant_data_entitlements explicitly to match checkTenantAccess.
func (tc *TestContainer) RevokeTenantEntitlement(ctx context.Context, tenantID tenant.TenantID, datasetCode string) error {
	query := `
		UPDATE public.tenant_data_entitlements
		SET is_active = FALSE
		WHERE tenant_id = $1 AND dataset_code = $2`

	_, err := tc.Pool.Exec(ctx, query, tenantID.String(), datasetCode)
	if err != nil {
		return fmt.Errorf("failed to revoke entitlement: %w", err)
	}
	return nil
}

// loadSchemaInSchema creates tables within a specific schema for multi-tenant testing.
func loadSchemaInSchema(ctx context.Context, pool *pgxpool.Pool, schemaName string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	quotedSchema := pgx.Identifier{schemaName}.Sanitize()
	_, err = conn.Exec(ctx, "SET search_path TO "+quotedSchema)
	if err != nil {
		return fmt.Errorf("failed to set search_path: %w", err)
	}

	if err := createSchemaDataSourceTable(ctx, conn); err != nil {
		return err
	}
	if err := createSchemaDatasetDefinitionTable(ctx, conn); err != nil {
		return err
	}
	if err := createSchemaObservationTable(ctx, conn); err != nil {
		return err
	}
	if err := createSchemaIndexes(ctx, conn); err != nil {
		return err
	}

	_, err = conn.Exec(ctx, "SET search_path TO public")
	if err != nil {
		return fmt.Errorf("failed to reset search_path: %w", err)
	}

	return nil
}

// createSchemaDataSourceTable creates the data_source table in the current search_path.
func createSchemaDataSourceTable(ctx context.Context, conn *pgxpool.Conn) error {
	_, err := conn.Exec(ctx, `
		CREATE TABLE data_source (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			code character varying(50) NOT NULL,
			name character varying(255) NOT NULL,
			description text NULL,
			trust_level integer NOT NULL DEFAULT 50,
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
			deleted_at timestamptz NULL,
			version bigint NOT NULL DEFAULT 1,
			status character varying(20) NOT NULL DEFAULT 'ACTIVE',
			deprecated_at timestamptz NULL,
			PRIMARY KEY (id),
			CONSTRAINT uq_data_source_code UNIQUE (code),
			CONSTRAINT chk_data_source_trust_level CHECK (trust_level >= 0 AND trust_level <= 100),
			CONSTRAINT chk_data_source_status CHECK (status IN ('ACTIVE', 'DEPRECATED'))
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create data_source table: %w", err)
	}
	return nil
}

// createSchemaDatasetDefinitionTable creates the dataset_definition table in the current search_path.
func createSchemaDatasetDefinitionTable(ctx context.Context, conn *pgxpool.Conn) error {
	_, err := conn.Exec(ctx, `
		CREATE TABLE dataset_definition (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			code character varying(50) NOT NULL,
			version integer NOT NULL DEFAULT 1,
			name character varying(255) NOT NULL,
			description text NULL,
			data_category character varying(50) NULL,
			validation_expression text NULL,
			resolution_key_expression text NOT NULL,
			error_message_expression text NULL,
			attribute_schema jsonb NULL,
			status character varying(20) NOT NULL DEFAULT 'DRAFT',
			is_shared BOOLEAN NOT NULL DEFAULT FALSE,
			access_level VARCHAR(50) NOT NULL DEFAULT 'PRIVATE',
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
			deleted_at timestamptz NULL,
			activated_at timestamptz NULL,
			deprecated_at timestamptz NULL,
			PRIMARY KEY (id),
			CONSTRAINT uq_dataset_definition_code_version UNIQUE (code, version),
			CONSTRAINT chk_dataset_definition_status CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
			CONSTRAINT chk_dataset_definition_access_level CHECK (access_level IN ('PUBLIC', 'PRIVATE', 'RESTRICTED'))
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create dataset_definition table: %w", err)
	}
	return nil
}

// createSchemaObservationTable creates the market_price_observation table in the current search_path.
func createSchemaObservationTable(ctx context.Context, conn *pgxpool.Conn) error {
	_, err := conn.Exec(ctx, `
		CREATE TABLE market_price_observation (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			dataset_definition_id uuid NOT NULL,
			data_source_id uuid NOT NULL,
			resolution_key character varying(255) NOT NULL,
			observed_at timestamptz NOT NULL,
			valid_from timestamptz NULL,
			valid_to timestamptz NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
			quality integer NOT NULL,
			revision integer NOT NULL DEFAULT 0,
			observation_context jsonb NOT NULL DEFAULT '{}'::jsonb,
			numeric_value numeric NULL,
			text_value text NULL,
			superseded_by uuid NULL,
			causation_id uuid NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_observation_dataset_definition
				FOREIGN KEY (dataset_definition_id) REFERENCES dataset_definition(id) ON DELETE RESTRICT,
			CONSTRAINT fk_observation_data_source
				FOREIGN KEY (data_source_id) REFERENCES data_source(id) ON DELETE RESTRICT,
			CONSTRAINT fk_observation_superseded_by
				FOREIGN KEY (superseded_by) REFERENCES market_price_observation(id) ON DELETE SET NULL,
			-- Quality is Axis A (confidence): four-level ladder IN (1,2,3,4) per ADR-0017,
			-- mirroring migration 20260608000002_quality_level_verified.sql. revision is
			-- Axis B (lifecycle), mirroring migration 20260608000003_add_revision_column.sql.
			CONSTRAINT chk_observation_quality CHECK (quality IN (1, 2, 3, 4)),
			CONSTRAINT chk_observation_value_present CHECK (numeric_value IS NOT NULL OR text_value IS NOT NULL)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create market_price_observation table: %w", err)
	}
	return nil
}

// createSchemaIndexes creates indexes for the tenant schema tables.
func createSchemaIndexes(ctx context.Context, conn *pgxpool.Conn) error {
	_, err := conn.Exec(ctx, `
		CREATE INDEX idx_dataset_definition_code_active ON dataset_definition (code) WHERE status = 'ACTIVE';
		CREATE INDEX idx_observation_resolution_bitemporal
			ON market_price_observation (resolution_key, quality DESC, observed_at DESC, created_at DESC)
			WHERE superseded_by IS NULL;
		CREATE INDEX idx_data_source_trust_level ON data_source (trust_level DESC);
	`)
	if err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}
	return nil
}
