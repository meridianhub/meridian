package saga

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestGetEmbeddedScripts(t *testing.T) {
	scripts, err := GetEmbeddedScripts()
	require.NoError(t, err)

	// Verify all expected scripts are embedded
	expectedScripts := []string{
		"withdrawal.star",
		"deposit.star",
		"payment_execution.star",
	}

	for _, expected := range expectedScripts {
		script, ok := scripts[expected]
		assert.True(t, ok, "expected script %s to be embedded", expected)
		assert.NotEmpty(t, script, "script %s should not be empty", expected)
	}
}

func TestPlatformDefaults(t *testing.T) {
	defaults := PlatformDefaults()

	assert.Len(t, defaults, 3)

	// Verify each default has required fields
	for _, meta := range defaults {
		assert.NotEmpty(t, meta.Name, "name should not be empty")
		assert.NotEmpty(t, meta.DisplayName, "display name should not be empty")
		assert.NotEmpty(t, meta.Description, "description should not be empty")
		assert.NotEmpty(t, meta.Filename, "filename should not be empty")
	}

	// Verify specific sagas
	names := make([]string, len(defaults))
	for i, meta := range defaults {
		names[i] = meta.Name
	}
	assert.Contains(t, names, "current_account_withdrawal")
	assert.Contains(t, names, "current_account_deposit")
	assert.Contains(t, names, "payment_execution")
}

func TestSeeder_SeedTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)
	defer func() {
		_ = pgContainer.Terminate(ctx)
	}()

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Create pool
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	defer pool.Close()

	// Create tenant schema and saga_definition table
	tenantID, err := tenant.NewTenantID("test_tenant")
	require.NoError(t, err)

	schemaName := tenantID.SchemaName()
	_, err = pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName)
	require.NoError(t, err)

	// Create saga_definition table in tenant schema
	createTableSQL := `
		CREATE TABLE ` + schemaName + `.saga_definition (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			name varchar(64) NOT NULL,
			version integer NOT NULL DEFAULT 1,
			script text NOT NULL,
			status varchar(16) NOT NULL DEFAULT 'DRAFT',
			is_system boolean NOT NULL DEFAULT FALSE,
			preconditions_expression text NULL,
			display_name varchar(128) NULL,
			description text NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			activated_at timestamptz NULL,
			deprecated_at timestamptz NULL,
			successor_id uuid NULL,
			PRIMARY KEY (id),
			CONSTRAINT uq_saga_definition_name_version UNIQUE (name, version)
		)`
	_, err = pool.Exec(ctx, createTableSQL)
	require.NoError(t, err)

	// Create seeder and seed
	seeder := NewSeeder(pool)

	t.Run("initial seed", func(t *testing.T) {
		err := seeder.SeedTenant(ctx, tenantID)
		require.NoError(t, err)

		// Verify sagas were seeded
		var count int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+schemaName+".saga_definition WHERE is_system = true").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 3, count, "expected 3 system sagas")

		// Verify each saga has correct attributes
		rows, err := pool.Query(ctx, `
			SELECT name, version, status, is_system, display_name, activated_at
			FROM `+schemaName+`.saga_definition
			WHERE is_system = true
			ORDER BY name`)
		require.NoError(t, err)
		defer rows.Close()

		var sagas []struct {
			name        string
			version     int
			status      string
			isSystem    bool
			displayName *string
			activatedAt *time.Time
		}

		for rows.Next() {
			var s struct {
				name        string
				version     int
				status      string
				isSystem    bool
				displayName *string
				activatedAt *time.Time
			}
			err := rows.Scan(&s.name, &s.version, &s.status, &s.isSystem, &s.displayName, &s.activatedAt)
			require.NoError(t, err)
			sagas = append(sagas, s)
		}
		require.NoError(t, rows.Err())

		for _, s := range sagas {
			assert.Equal(t, 1, s.version, "platform default should have version 1")
			assert.Equal(t, "ACTIVE", s.status, "platform default should be ACTIVE")
			assert.True(t, s.isSystem, "platform default should be is_system=true")
			assert.NotNil(t, s.activatedAt, "platform default should have activated_at")
		}
	})

	t.Run("idempotent seed", func(t *testing.T) {
		// Seed again - should be idempotent
		err := seeder.SeedTenant(ctx, tenantID)
		require.NoError(t, err)

		// Count should still be 3
		var count int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+schemaName+".saga_definition WHERE is_system = true").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 3, count, "idempotent seed should not create duplicates")
	})

	t.Run("deterministic UUIDs", func(t *testing.T) {
		// Verify UUIDs are deterministic based on saga name
		var ids []uuid.UUID
		rows, err := pool.Query(ctx, "SELECT id FROM "+schemaName+".saga_definition WHERE is_system = true ORDER BY name")
		require.NoError(t, err)
		defer rows.Close()

		for rows.Next() {
			var id uuid.UUID
			err := rows.Scan(&id)
			require.NoError(t, err)
			ids = append(ids, id)
		}
		require.NoError(t, rows.Err())

		// Expected IDs based on SHA1(NameSpaceDNS, "saga.meridian.<name>")
		expectedIDs := []uuid.UUID{
			uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian.current_account_deposit")),
			uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian.current_account_withdrawal")),
			uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian.payment_execution")),
		}

		assert.Equal(t, expectedIDs, ids, "UUIDs should be deterministic")
	})
}

func TestSeeder_SeedTenant_TenantOverride(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)
	defer func() {
		_ = pgContainer.Terminate(ctx)
	}()

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Create pool
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	defer pool.Close()

	// Create tenant schema
	tenantID, err := tenant.NewTenantID("override_test")
	require.NoError(t, err)

	schemaName := tenantID.SchemaName()
	_, err = pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName)
	require.NoError(t, err)

	// Create saga_definition table
	createTableSQL := `
		CREATE TABLE ` + schemaName + `.saga_definition (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			name varchar(64) NOT NULL,
			version integer NOT NULL DEFAULT 1,
			script text NOT NULL,
			status varchar(16) NOT NULL DEFAULT 'DRAFT',
			is_system boolean NOT NULL DEFAULT FALSE,
			preconditions_expression text NULL,
			display_name varchar(128) NULL,
			description text NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			activated_at timestamptz NULL,
			deprecated_at timestamptz NULL,
			successor_id uuid NULL,
			PRIMARY KEY (id),
			CONSTRAINT uq_saga_definition_name_version UNIQUE (name, version)
		)`
	_, err = pool.Exec(ctx, createTableSQL)
	require.NoError(t, err)

	// Seed platform defaults
	seeder := NewSeeder(pool)
	err = seeder.SeedTenant(ctx, tenantID)
	require.NoError(t, err)

	// Create a tenant override saga (is_system=false) with a different version
	overrideScript := `
# Custom tenant withdrawal saga
withdrawal_saga = saga(name="current_account_withdrawal")
# Custom logic here
`
	_, err = pool.Exec(ctx, `
		INSERT INTO `+schemaName+`.saga_definition
			(name, version, script, status, is_system, activated_at)
		VALUES
			('current_account_withdrawal', 2, $1, 'ACTIVE', false, now())`,
		overrideScript)
	require.NoError(t, err)

	// Test that both platform default and tenant override exist
	var systemCount, tenantCount int
	err = pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE is_system = true) as system_count,
			COUNT(*) FILTER (WHERE is_system = false) as tenant_count
		FROM `+schemaName+`.saga_definition
		WHERE name = 'current_account_withdrawal'`).
		Scan(&systemCount, &tenantCount)
	require.NoError(t, err)

	assert.Equal(t, 1, systemCount, "should have 1 system saga")
	assert.Equal(t, 1, tenantCount, "should have 1 tenant override saga")

	// Verify tenant override has higher version
	var systemVersion, tenantVersion int
	err = pool.QueryRow(ctx, `
		SELECT
			MAX(version) FILTER (WHERE is_system = true),
			MAX(version) FILTER (WHERE is_system = false)
		FROM `+schemaName+`.saga_definition
		WHERE name = 'current_account_withdrawal'`).
		Scan(&systemVersion, &tenantVersion)
	require.NoError(t, err)

	assert.Equal(t, 1, systemVersion, "system saga should be version 1")
	assert.Equal(t, 2, tenantVersion, "tenant override should be version 2")
}
