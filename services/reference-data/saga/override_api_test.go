package saga

import (
	"context"
	"fmt"
	"testing"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverrideService_CreateTenantOverride(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Sync platform defaults
	platformSync := NewPlatformSync(pool)
	err := platformSync.SyncPlatformDefaults(ctx)
	require.NoError(t, err)

	// Create tenant schema with saga_definition table
	tenantID := tenant.TenantID("override_tenant")
	schemaName := tenantID.SchemaName()
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	// Seed tenant with platform refs
	seeder := NewSeeder(pool)
	err = seeder.SeedTenant(ctx, tenantID)
	require.NoError(t, err)

	// Create registry and override service
	tenantCtx := tenant.WithTenant(ctx, tenantID)
	registry := NewPostgresRegistry(pool, nil)
	overrideSvc := NewOverrideService(pool, registry)

	t.Run("95% similar script is rejected", func(t *testing.T) {
		// Get the platform script for withdrawal
		platformDef, err := registry.GetPlatformSagaByName(ctx, "current_account_withdrawal")
		require.NoError(t, err)

		// Create a script that's nearly identical (change just one character)
		nearlyIdentical := platformDef.Script + " "

		_, err = overrideSvc.CreateTenantOverride(tenantCtx, OverrideRequest{
			SagaName:       "current_account_withdrawal",
			Script:         nearlyIdentical,
			OverrideReason: "Testing similarity rejection",
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrScriptTooSimilar)
	})

	t.Run("50% similar script is accepted", func(t *testing.T) {
		customScript := `# Version: 1.0.0
# Custom withdrawal saga with completely different logic
def posting_rules(ctx):
    """Custom withdrawal saga for override_tenant."""
    step(
        action = "custom_debit_handler",
        params = {
            "account": ctx.source_account,
            "amount": ctx.amount,
            "custom_field": "custom_value",
        },
    )
    step(
        action = "custom_credit_handler",
        params = {
            "account": ctx.target_account,
            "amount": ctx.amount,
            "routing": "express",
        },
    )
    step(
        action = "custom_notification_handler",
        params = {
            "template": "withdrawal_complete",
            "recipient": ctx.customer_email,
        },
    )
    return {"status": "completed", "custom": True}`

		result, err := overrideSvc.CreateTenantOverride(tenantCtx, OverrideRequest{
			SagaName:       "current_account_withdrawal",
			Script:         customScript,
			OverrideReason: "Need custom routing logic for express withdrawals",
		})

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.OverrideDefinition)
		assert.Equal(t, "current_account_withdrawal", result.OverrideDefinition.Name)
		assert.Equal(t, StatusDraft, result.OverrideDefinition.Status)
		assert.NotEmpty(t, result.PlatformVersion)
		assert.True(t, result.SimilarityRatio < DefaultSimilarityThreshold,
			"similarity ratio should be below threshold")
	})

	t.Run("already overridden saga is rejected", func(t *testing.T) {
		// Try to override again - should fail because there's now a non-system override
		anotherScript := `# Another completely different script
def posting_rules(ctx):
    """Another override attempt."""
    return {"error": "should not work"}`

		_, err := overrideSvc.CreateTenantOverride(tenantCtx, OverrideRequest{
			SagaName:       "current_account_withdrawal",
			Script:         anotherScript,
			OverrideReason: "Second override attempt",
		})

		// This should either get ErrAlreadyOverridden or ErrNotPlatformReferenced
		// depending on whether the first override's DRAFT takes priority in GetActive
		require.Error(t, err)
	})

	t.Run("missing override reason is rejected", func(t *testing.T) {
		_, err := overrideSvc.CreateTenantOverride(tenantCtx, OverrideRequest{
			SagaName: "current_account_deposit",
			Script:   "def posting_rules(ctx): return {}",
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverrideReasonRequired)
	})

	t.Run("empty script is rejected", func(t *testing.T) {
		_, err := overrideSvc.CreateTenantOverride(tenantCtx, OverrideRequest{
			SagaName:       "current_account_deposit",
			OverrideReason: "Testing",
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverrideScriptEmpty)
	})
}

func TestOverrideService_MigrateToPlatformRef(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Sync platform defaults
	platformSync := NewPlatformSync(pool)
	err := platformSync.SyncPlatformDefaults(ctx)
	require.NoError(t, err)

	// Create tenant schema with saga_definition table
	tenantID := tenant.TenantID("migrate_tenant")
	schemaName := tenantID.SchemaName()
	quoted := pq.QuoteIdentifier(schemaName)
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	// Manually insert script-copied sagas (old style, without platform_ref)
	scripts, err := GetEmbeddedScripts()
	require.NoError(t, err)

	overrideDefaults, overrideDefaultsErr := PlatformDefaults()
	require.NoError(t, overrideDefaultsErr)
	for _, meta := range overrideDefaults {
		script, ok := scripts[meta.Filename+".star"]
		require.True(t, ok, "expected embedded script %s.star", meta.Filename)
		require.NotEmpty(t, script)
		_, err := pool.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s.saga_definition (
				name, version, script, status, is_system,
				display_name, description, created_at, updated_at, activated_at
			) VALUES ($1, 1, $2, 'ACTIVE', true, $3, $4, now(), now(), now())`, quoted),
			meta.Name, script, meta.DisplayName, meta.Description)
		require.NoError(t, err)
	}

	// Create override service
	registry := NewPostgresRegistry(pool, nil)
	overrideSvc := NewOverrideService(pool, registry)

	t.Run("dry run shows what would be migrated", func(t *testing.T) {
		results, err := overrideSvc.MigrateToPlatformRef(ctx, tenantID, true)
		require.NoError(t, err)

		// Should find all 4 platform sagas as "would_migrate"
		wouldMigrateCount := 0
		for _, r := range results {
			if r.Action == "would_migrate" {
				wouldMigrateCount++
				assert.NotNil(t, r.PlatformRefID)
				assert.True(t, r.SimilarityRatio >= 0.95,
					"identical scripts should have >= 95%% similarity")
			}
		}
		assert.Equal(t, 8, wouldMigrateCount, "all 8 platform sagas should be candidates")

		// Verify nothing actually changed
		var scriptCount int
		err = pool.QueryRow(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM %s.saga_definition WHERE script IS NOT NULL AND script != ''", quoted)).
			Scan(&scriptCount)
		require.NoError(t, err)
		assert.Equal(t, 8, scriptCount, "dry run should not modify data")
	})

	t.Run("apply migration converts to platform refs", func(t *testing.T) {
		results, err := overrideSvc.MigrateToPlatformRef(ctx, tenantID, false)
		require.NoError(t, err)

		migratedCount := 0
		for _, r := range results {
			if r.Action == "migrated" {
				migratedCount++
			}
		}
		assert.Equal(t, 8, migratedCount, "all 8 platform sagas should be migrated")

		// Verify scripts are now NULL and platform_ref is set
		var refCount int
		err = pool.QueryRow(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM %s.saga_definition WHERE platform_ref IS NOT NULL AND (script IS NULL OR script = '')", quoted)).
			Scan(&refCount)
		require.NoError(t, err)
		assert.Equal(t, 8, refCount, "all sagas should now use platform_ref")
	})

	t.Run("re-running migration skips already-migrated sagas", func(t *testing.T) {
		results, err := overrideSvc.MigrateToPlatformRef(ctx, tenantID, false)
		require.NoError(t, err)

		for _, r := range results {
			assert.Equal(t, "skipped", r.Action,
				"all sagas should be skipped on re-run")
			assert.Equal(t, "already has platform_ref", r.Reason)
		}
	})
}
