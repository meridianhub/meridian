package saga

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEmbeddedScripts(t *testing.T) {
	scripts, err := GetEmbeddedScripts()
	require.NoError(t, err)

	// Verify versioned scripts are embedded with full path keys
	expectedVersioned := []string{
		"deposit/v1.0.0.star",
		"withdrawal/v1.0.0.star",
		"payment_execution/v1.0.0.star",
		"reconciliation_adjustment/v1.0.0.star",
	}

	for _, expected := range expectedVersioned {
		script, ok := scripts[expected]
		assert.True(t, ok, "expected script %s to be embedded", expected)
		assert.NotEmpty(t, script, "script %s should not be empty", expected)
	}

	// Verify backward-compatible flat keys exist
	expectedFlat := []string{
		"withdrawal.star",
		"deposit.star",
		"payment_execution.star",
		"reconciliation_adjustment.star",
	}

	for _, expected := range expectedFlat {
		script, ok := scripts[expected]
		assert.True(t, ok, "expected backward-compatible script %s to be embedded", expected)
		assert.NotEmpty(t, script, "script %s should not be empty", expected)
	}
}

func TestPlatformDefaults(t *testing.T) {
	defaults, err := PlatformDefaults()
	require.NoError(t, err)

	assert.Len(t, defaults, 4)

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
	assert.Contains(t, names, "reconciliation_adjustment")
}

func TestSeeder_SeedTenant(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// First, sync platform defaults (prerequisite for seeder)
	platformSync := NewPlatformSync(pool)
	err := platformSync.SyncPlatformDefaults(ctx)
	require.NoError(t, err)

	// Create tenant schema and saga_definition table
	tenantID := tenant.TenantID("test_tenant")
	schemaName := tenantID.SchemaName()
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	// Create seeder and seed
	seeder := NewSeeder(pool)

	t.Run("initial seed creates platform_ref entries", func(t *testing.T) {
		err := seeder.SeedTenant(ctx, tenantID)
		require.NoError(t, err)

		// Verify sagas were seeded
		var count int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+schemaName+".saga_definition WHERE is_system = true").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 4, count, "expected 4 system sagas")

		// Verify each saga has platform_ref and no script
		rows, err := pool.Query(ctx, `
			SELECT name, version, status, is_system, platform_ref, script, display_name, activated_at
			FROM `+schemaName+`.saga_definition
			WHERE is_system = true
			ORDER BY name`)
		require.NoError(t, err)
		defer rows.Close()

		for rows.Next() {
			var name string
			var version int
			var status string
			var isSystem bool
			var platformRef *uuid.UUID
			var script *string
			var displayName *string
			var activatedAt interface{}

			err := rows.Scan(&name, &version, &status, &isSystem, &platformRef, &script, &displayName, &activatedAt)
			require.NoError(t, err)

			assert.Equal(t, 1, version, "platform default should have version 1")
			assert.Equal(t, "ACTIVE", status, "platform default should be ACTIVE")
			assert.True(t, isSystem, "platform default should be is_system=true")
			assert.NotNil(t, platformRef, "platform default should have platform_ref set")
			assert.True(t, script == nil || *script == "", "platform default should NOT have a copied script")
			assert.NotNil(t, activatedAt, "platform default should have activated_at")

			// Verify platform_ref points to actual platform saga
			var platformName string
			err = pool.QueryRow(ctx,
				"SELECT name FROM public.platform_saga_definition WHERE id = $1",
				*platformRef,
			).Scan(&platformName)
			require.NoError(t, err, "platform_ref should point to existing platform saga")
			assert.Equal(t, name, platformName, "platform_ref should point to same-named platform saga")
		}
		require.NoError(t, rows.Err())
	})

	t.Run("idempotent seed", func(t *testing.T) {
		// Seed again - should be idempotent
		err := seeder.SeedTenant(ctx, tenantID)
		require.NoError(t, err)

		// Count should still be 3
		var count int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+schemaName+".saga_definition WHERE is_system = true").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 4, count, "idempotent seed should not create duplicates")
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
			uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian.reconciliation_adjustment")),
		}

		assert.Equal(t, expectedIDs, ids, "UUIDs should be deterministic")
	})

	t.Run("seeded sagas resolve script via platform fallback", func(t *testing.T) {
		// Query using LEFT JOIN to resolve the script, matching how postgres_registry.go works
		var resolvedScript string
		var usedFallback bool
		err := pool.QueryRow(ctx, `
			SELECT
				COALESCE(NULLIF(sd.script, ''), psd.script, '') AS resolved_script,
				psd.script IS NOT NULL AND (sd.script IS NULL OR sd.script = '') AS used_platform_fallback
			FROM `+schemaName+`.saga_definition sd
			LEFT JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
			WHERE sd.name = 'current_account_withdrawal' AND sd.is_system = true
		`).Scan(&resolvedScript, &usedFallback)
		require.NoError(t, err)

		assert.NotEmpty(t, resolvedScript, "resolved script should not be empty")
		assert.True(t, usedFallback, "script should come from platform fallback")
		assert.Contains(t, resolvedScript, "current_account_withdrawal",
			"resolved script should contain withdrawal saga content")
	})
}

func TestSeeder_SeedTenant_FailsWithoutPlatformSync(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Do NOT sync platform defaults
	// Create tenant schema
	tenantID := tenant.TenantID("no_sync_tenant")
	schemaName := tenantID.SchemaName()
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	seeder := NewSeeder(pool)
	err := seeder.SeedTenant(ctx, tenantID)
	require.Error(t, err, "seeding should fail when platform sagas are not synced")
	assert.ErrorIs(t, err, ErrPlatformSagaNotSynced)
}

// setupTenantSchemaForSeeder creates the tenant schema and saga_definition table
// for seeder integration tests. It applies the relevant migrations.
func setupTenantSchemaForSeeder(t *testing.T, pool *pgxpool.Pool, ctx context.Context, schemaName string) {
	t.Helper()

	_, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName)
	require.NoError(t, err)

	// Create saga_definition table matching the full migrated schema
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS ` + schemaName + `.saga_definition (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			name varchar(64) NOT NULL,
			version integer NOT NULL DEFAULT 1,
			script text NULL,
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
			platform_ref uuid NULL REFERENCES public.platform_saga_definition(id) ON DELETE SET NULL,
			override_reason text NULL,
			platform_version_at_override varchar(16) NULL,
			validation_status text NOT NULL DEFAULT 'UNVALIDATED',
			complexity_score integer NULL,
			handler_call_count integer NULL,
			validated_at timestamptz NULL,
			PRIMARY KEY (id),
			CONSTRAINT uq_saga_definition_name_version UNIQUE (name, version),
			CONSTRAINT chk_saga_definition_script_source
				CHECK (NOT (platform_ref IS NOT NULL AND script IS NOT NULL AND script != ''))
		)`
	_, err = pool.Exec(ctx, createTableSQL)
	require.NoError(t, err)
}
