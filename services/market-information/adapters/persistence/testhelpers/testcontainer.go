// Package testhelpers provides shared utilities for repository integration tests.
package testhelpers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestContainer holds the test database container, connection pool, and repository instances.
type TestContainer struct {
	container *postgres.PostgresContainer
	Pool      *pgxpool.Pool
	Repos     *persistence.Repositories
}

// SetupTestContainer creates a PostgreSQL testcontainer with the market_information schema loaded.
func SetupTestContainer(t *testing.T) *TestContainer {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container with explicit wait strategy
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

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Create connection pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to create connection pool")

	// Load schema
	loadSchema(t, pool)

	// Create repositories with test master tenant
	repos := persistence.NewRepositories(pool, "test_master")

	return &TestContainer{
		container: pgContainer,
		Pool:      pool,
		Repos:     repos,
	}
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

// loadSchema loads the complete market_information schema into the test database.
func loadSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Create data_source table
	_, err := pool.Exec(ctx, `
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
			PRIMARY KEY (id),
			CONSTRAINT uq_data_source_code UNIQUE (code),
			CONSTRAINT chk_data_source_trust_level CHECK (trust_level >= 0 AND trust_level <= 100)
		)
	`)
	require.NoError(t, err, "Failed to create data_source table")

	// Create dataset_definition table
	_, err = pool.Exec(ctx, `
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
	require.NoError(t, err, "Failed to create dataset_definition table")

	// Create indexes for dataset_definition
	_, err = pool.Exec(ctx, `
		CREATE INDEX idx_dataset_definition_code_active ON dataset_definition (code) WHERE status = 'ACTIVE';
		CREATE INDEX idx_dataset_definition_status ON dataset_definition (status);
		CREATE INDEX idx_dataset_definition_created_at ON dataset_definition (created_at);
		CREATE INDEX idx_dataset_definition_deleted_at ON dataset_definition (deleted_at);
	`)
	require.NoError(t, err, "Failed to create dataset_definition indexes")

	// Create market_price_observation table
	_, err = pool.Exec(ctx, `
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
			CONSTRAINT chk_observation_quality CHECK (quality IN (1, 2, 3)),
			CONSTRAINT chk_observation_value_present CHECK (numeric_value IS NOT NULL OR text_value IS NOT NULL)
		)
	`)
	require.NoError(t, err, "Failed to create market_price_observation table")

	// Create indexes for market_price_observation
	_, err = pool.Exec(ctx, `
		CREATE INDEX idx_observation_resolution_bitemporal
			ON market_price_observation (resolution_key, quality DESC, observed_at DESC, created_at DESC)
			WHERE superseded_by IS NULL;
		CREATE INDEX idx_observation_dataset
			ON market_price_observation (dataset_definition_id, observed_at DESC);
		CREATE INDEX idx_observation_source
			ON market_price_observation (data_source_id, created_at DESC);
		CREATE INDEX idx_observation_created_at
			ON market_price_observation (created_at DESC)
			WHERE superseded_by IS NULL;
		CREATE INDEX idx_observation_superseded_by
			ON market_price_observation (superseded_by)
			WHERE superseded_by IS NOT NULL;
		CREATE INDEX idx_observation_causation
			ON market_price_observation (causation_id)
			WHERE causation_id IS NOT NULL;
	`)
	require.NoError(t, err, "Failed to create market_price_observation indexes")

	// Create data source index
	_, err = pool.Exec(ctx, `
		CREATE INDEX idx_data_source_trust_level ON data_source (trust_level DESC);
		CREATE INDEX idx_data_source_deleted_at ON data_source (deleted_at);
	`)
	require.NoError(t, err, "Failed to create data_source indexes")

	// Create tenant_data_entitlements table
	_, err = pool.Exec(ctx, `
		CREATE TABLE tenant_data_entitlements (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id VARCHAR(255) NOT NULL,
			dataset_code VARCHAR(255) NOT NULL,
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_by VARCHAR(100) NOT NULL DEFAULT 'SYSTEM',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_by VARCHAR(100) NOT NULL DEFAULT 'SYSTEM',
			CONSTRAINT uq_tenant_dataset UNIQUE (tenant_id, dataset_code)
		)
	`)
	require.NoError(t, err, "Failed to create tenant_data_entitlements table")

	// Create tenant_data_entitlements indexes
	_, err = pool.Exec(ctx, `
		CREATE INDEX idx_entitlements_tenant_dataset
			ON tenant_data_entitlements(tenant_id, dataset_code, is_active)
			WHERE is_active = TRUE;
		CREATE INDEX idx_entitlements_expires_at
			ON tenant_data_entitlements(expires_at)
			WHERE expires_at IS NOT NULL AND is_active = TRUE;
	`)
	require.NoError(t, err, "Failed to create tenant_data_entitlements indexes")
}

// CreateTenantSchema creates a tenant-specific schema for testing multi-tenant scenarios.
func (tc *TestContainer) CreateTenantSchema(tenantIDStr string) (tenant.TenantID, error) {
	ctx := context.Background()

	tenantID, err := tenant.NewTenantID(tenantIDStr)
	if err != nil {
		return tenant.TenantID(""), fmt.Errorf("invalid tenant ID: %w", err)
	}

	schemaName := tenantID.SchemaName()
	// Use pgx.Identifier for proper SQL identifier quoting to prevent injection
	quotedSchema := pgx.Identifier{schemaName}.Sanitize()

	// Create schema
	_, err = tc.Pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quotedSchema)
	if err != nil {
		return tenant.TenantID(""), fmt.Errorf("failed to create schema %s: %w", schemaName, err)
	}

	// Set search path and create tables in tenant schema
	_, err = tc.Pool.Exec(ctx, "SET search_path TO "+quotedSchema)
	if err != nil {
		return tenant.TenantID(""), fmt.Errorf("failed to set search_path: %w", err)
	}

	// Create tenant-specific tables (same structure as public schema)
	err = loadSchemaInSchema(ctx, tc.Pool, schemaName)
	if err != nil {
		return tenant.TenantID(""), fmt.Errorf("failed to load schema: %w", err)
	}

	// Copy shared datasets from public schema to tenant schema
	// This allows tenant observations to reference shared dataset definitions
	_, err = tc.Pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.dataset_definition
		SELECT * FROM public.dataset_definition
		WHERE is_shared = TRUE
	`, quotedSchema))
	if err != nil {
		return tenant.TenantID(""), fmt.Errorf("failed to copy shared datasets: %w", err)
	}

	// Copy data sources from public schema to tenant schema
	// Data sources are needed for observations to have valid foreign keys
	_, err = tc.Pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.data_source
		SELECT * FROM public.data_source
	`, quotedSchema))
	if err != nil {
		return tenant.TenantID(""), fmt.Errorf("failed to copy data sources: %w", err)
	}

	// Reset search path to default
	_, err = tc.Pool.Exec(ctx, "SET search_path TO public")
	if err != nil {
		return tenant.TenantID(""), fmt.Errorf("failed to reset search_path: %w", err)
	}

	return tenantID, nil
}

// WithTenant wraps a context with tenant information.
func (tc *TestContainer) WithTenant(ctx context.Context, tenantID tenant.TenantID) context.Context {
	return tenant.WithTenant(ctx, tenantID)
}

// GrantTenantEntitlement grants access to a dataset for a tenant.
func (tc *TestContainer) GrantTenantEntitlement(ctx context.Context, tenantID tenant.TenantID, datasetCode string, expiresAt *time.Time) error {
	query := `
		INSERT INTO tenant_data_entitlements (tenant_id, dataset_code, is_active, expires_at)
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
func (tc *TestContainer) RevokeTenantEntitlement(ctx context.Context, tenantID tenant.TenantID, datasetCode string) error {
	query := `
		UPDATE tenant_data_entitlements
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
	// Set search path to tenant schema (use pgx.Identifier for proper quoting)
	quotedSchema := pgx.Identifier{schemaName}.Sanitize()
	_, err := pool.Exec(ctx, "SET search_path TO "+quotedSchema)
	if err != nil {
		return fmt.Errorf("failed to set search_path: %w", err)
	}

	// Create tables (same as public schema)
	_, err = pool.Exec(ctx, `
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
			PRIMARY KEY (id),
			CONSTRAINT uq_data_source_code UNIQUE (code),
			CONSTRAINT chk_data_source_trust_level CHECK (trust_level >= 0 AND trust_level <= 100)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create data_source table: %w", err)
	}

	_, err = pool.Exec(ctx, `
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

	_, err = pool.Exec(ctx, `
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
			CONSTRAINT chk_observation_quality CHECK (quality IN (1, 2, 3)),
			CONSTRAINT chk_observation_value_present CHECK (numeric_value IS NOT NULL OR text_value IS NOT NULL)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create market_price_observation table: %w", err)
	}

	// Create indexes
	_, err = pool.Exec(ctx, `
		CREATE INDEX idx_dataset_definition_code_active ON dataset_definition (code) WHERE status = 'ACTIVE';
		CREATE INDEX idx_observation_resolution_bitemporal
			ON market_price_observation (resolution_key, quality DESC, observed_at DESC, created_at DESC)
			WHERE superseded_by IS NULL;
		CREATE INDEX idx_data_source_trust_level ON data_source (trust_level DESC);
	`)
	if err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	// Reset search path
	_, err = pool.Exec(ctx, "SET search_path TO public")
	if err != nil {
		return fmt.Errorf("failed to reset search_path: %w", err)
	}

	return nil
}
