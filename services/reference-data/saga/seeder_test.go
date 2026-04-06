package saga

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
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
		"stripe_payment/v1.0.0.star",
		"dunning_escalation/v1.0.0.star",
		"dunning_unfreeze/v1.0.0.star",
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
		"stripe_payment.star",
		"dunning_escalation.star",
		"dunning_unfreeze.star",
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

	assert.Len(t, defaults, 8)

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
	assert.Contains(t, names, "stripe_payment")
	assert.Contains(t, names, "dunning_escalation")
	assert.Contains(t, names, "dunning_unfreeze")
	assert.Contains(t, names, "dividend_distribution")
}

func TestSeeder_SeedTenant(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create tenant schema and saga_definition table
	tenantID := tenant.TenantID("test_tenant")
	schemaName := tenantID.SchemaName()
	quoted := pq.QuoteIdentifier(schemaName)
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	// Create seeder and seed - no PlatformSync prerequisite needed
	seeder := NewSeeder(pool)

	t.Run("initial seed copies scripts directly", func(t *testing.T) {
		err := seeder.SeedTenant(ctx, tenantID)
		require.NoError(t, err)

		// Verify sagas were seeded
		var count int
		err = pool.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s.saga_definition WHERE is_system = true", quoted)).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 8, count, "expected 8 system sagas")

		// Verify each saga has script content (not platform_ref)
		rows, err := pool.Query(ctx, fmt.Sprintf(`
			SELECT name, version, status, is_system, script, display_name, activated_at
			FROM %s.saga_definition
			WHERE is_system = true
			ORDER BY name`, quoted))
		require.NoError(t, err)
		defer rows.Close()

		for rows.Next() {
			var name string
			var version int
			var status string
			var isSystem bool
			var script *string
			var displayName *string
			var activatedAt interface{}

			err := rows.Scan(&name, &version, &status, &isSystem, &script, &displayName, &activatedAt)
			require.NoError(t, err)

			assert.Equal(t, 1, version, "platform default should have version 1")
			assert.Equal(t, "ACTIVE", status, "platform default should be ACTIVE")
			assert.True(t, isSystem, "platform default should be is_system=true")
			assert.NotNil(t, script, "platform default should have script content")
			assert.NotEmpty(t, *script, "platform default script should not be empty")
			assert.NotNil(t, activatedAt, "platform default should have activated_at")
		}
		require.NoError(t, rows.Err())
	})

	t.Run("idempotent seed", func(t *testing.T) {
		// Seed again - should be idempotent
		err := seeder.SeedTenant(ctx, tenantID)
		require.NoError(t, err)

		// Count should still be 8
		var count int
		err = pool.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s.saga_definition WHERE is_system = true", quoted)).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 8, count, "idempotent seed should not create duplicates")
	})

	t.Run("deterministic UUIDs", func(t *testing.T) {
		// Verify UUIDs are deterministic based on saga name
		var ids []uuid.UUID
		rows, err := pool.Query(ctx, fmt.Sprintf("SELECT id FROM %s.saga_definition WHERE is_system = true ORDER BY name", quoted))
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
			uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian.dividend_distribution")),
			uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian.dunning_escalation")),
			uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian.dunning_unfreeze")),
			uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian.payment_execution")),
			uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian.reconciliation_adjustment")),
			uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian.stripe_payment")),
		}

		assert.Equal(t, expectedIDs, ids, "UUIDs should be deterministic")
	})

	t.Run("seeded sagas have script content", func(t *testing.T) {
		var script string
		err := pool.QueryRow(ctx, fmt.Sprintf(`
			SELECT script
			FROM %s.saga_definition
			WHERE name = 'current_account_withdrawal' AND is_system = true
		`, quoted)).Scan(&script)
		require.NoError(t, err)

		assert.NotEmpty(t, script, "script should not be empty")
		assert.Contains(t, script, "current_account_withdrawal",
			"script should contain withdrawal saga content")
	})
}

func TestSeeder_SeedTenant_SelfContained(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Do NOT sync platform defaults - seeder should be self-contained
	// (reads from embedded filesystem, not public.platform_saga_definition)
	tenantID := tenant.TenantID("no_sync_tenant")
	schemaName := tenantID.SchemaName()
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	seeder := NewSeeder(pool)
	err := seeder.SeedTenant(ctx, tenantID)
	require.NoError(t, err, "seeding should succeed without PlatformSync")

	// Verify sagas were seeded with script content
	var count int
	err = pool.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s.saga_definition WHERE is_system = true AND script IS NOT NULL AND script != ''", pq.QuoteIdentifier(schemaName))).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 8, count, "expected 8 system sagas with script content")
}

// setupTenantSchemaForSeeder creates the tenant schema and saga_definition table
// for seeder integration tests.
func setupTenantSchemaForSeeder(t *testing.T, pool *pgxpool.Pool, ctx context.Context, schemaName string) {
	t.Helper()

	quoted := pq.QuoteIdentifier(schemaName)
	_, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quoted))
	require.NoError(t, err)

	// Create saga_definition table matching the full migrated schema
	// Note: no FK reference to public.platform_saga_definition (tenant isolation)
	createTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.saga_definition (
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
			platform_ref uuid NULL,
			override_reason text NULL,
			platform_version_at_override varchar(16) NULL,
			validation_status text NOT NULL DEFAULT 'UNVALIDATED',
			complexity_score integer NULL,
			handler_call_count integer NULL,
			validated_at timestamptz NULL,
			PRIMARY KEY (id),
			CONSTRAINT uq_saga_definition_name_version UNIQUE (name, version),
			CONSTRAINT chk_saga_definition_script_source CHECK (
				NOT (platform_ref IS NOT NULL AND script IS NOT NULL AND script != '')
			)
		)`, quoted)
	_, err = pool.Exec(ctx, createTableSQL)
	require.NoError(t, err)
}
