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

// TestE2E_ProvisioningOverrideMigration exercises the full lifecycle:
// 1. Sync platform defaults
// 2. Provision a new tenant (seeder creates platform_ref entries)
// 3. Verify platform fallback resolution works
// 4. Override a saga with custom script
// 5. Verify override takes priority over platform default
// 6. Provision another tenant with old-style copies
// 7. Migrate old tenant to platform refs
func TestE2E_ProvisioningOverrideMigration(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Step 1: Sync platform defaults
	platformSync := NewPlatformSync(pool)
	err := platformSync.SyncPlatformDefaults(ctx)
	require.NoError(t, err)

	// Step 2: Provision tenant alpha (new-style with platform_ref)
	alphaID := tenant.TenantID("alpha_corp")
	alphaSchema := alphaID.SchemaName()
	setupTenantSchemaForSeeder(t, pool, ctx, alphaSchema)
	createSagaReferenceTable(t, pool, ctx, alphaSchema)

	seeder := NewSeeder(pool)
	err = seeder.SeedTenant(ctx, alphaID)
	require.NoError(t, err)

	// Step 3: Verify platform fallback resolution
	alphaCtx := tenant.WithTenant(ctx, alphaID)
	registry := NewPostgresRegistry(pool, nil)

	activeDef, err := registry.GetActive(alphaCtx, "current_account_withdrawal")
	require.NoError(t, err)
	assert.True(t, activeDef.IsSystem, "should resolve system saga")
	assert.True(t, activeDef.UsedPlatformFallback, "should use platform fallback")
	assert.NotEmpty(t, activeDef.ResolvedScript, "resolved script should not be empty")
	assert.NotNil(t, activeDef.PlatformRef, "platform_ref should be set")

	// Step 4: Create tenant override
	overrideSvc := NewOverrideService(pool, registry)

	customScript := `# Version: 1.0.0
# Alpha Corp custom withdrawal
def posting_rules(ctx):
    """Alpha Corp's specialized withdrawal saga."""
    step(
        action = "alpha_debit",
        params = {"account": ctx.source, "amount": ctx.amount},
    )
    step(
        action = "alpha_compliance_check",
        params = {"amount": ctx.amount, "jurisdiction": "UK"},
    )
    step(
        action = "alpha_credit",
        params = {"account": ctx.target, "amount": ctx.amount},
    )
    return {"status": "completed", "compliance_checked": True}`

	overrideResult, err := overrideSvc.CreateTenantOverride(alphaCtx, OverrideRequest{
		SagaName:       "current_account_withdrawal",
		Script:         customScript,
		OverrideReason: "Alpha Corp requires UK compliance checks on all withdrawals",
	})
	require.NoError(t, err)
	assert.Equal(t, StatusDraft, overrideResult.OverrideDefinition.Status)
	assert.NotEmpty(t, overrideResult.PlatformVersion)

	// Activate the override
	err = registry.ActivateSaga(alphaCtx, overrideResult.OverrideDefinition.ID)
	require.NoError(t, err)

	// Step 5: Verify override takes priority
	activeDef, err = registry.GetActive(alphaCtx, "current_account_withdrawal")
	require.NoError(t, err)
	assert.False(t, activeDef.IsSystem, "should resolve tenant override, not system saga")
	assert.False(t, activeDef.UsedPlatformFallback, "should use tenant script, not platform")
	assert.Contains(t, activeDef.Script, "alpha_compliance_check",
		"should use the custom override script")
	assert.Equal(t, "Alpha Corp requires UK compliance checks on all withdrawals",
		activeDef.OverrideReason)

	// Non-overridden sagas should still use platform fallback
	depositDef, err := registry.GetActive(alphaCtx, "current_account_deposit")
	require.NoError(t, err)
	assert.True(t, depositDef.IsSystem, "deposit should still use system saga")
	assert.True(t, depositDef.UsedPlatformFallback, "deposit should use platform fallback")

	// Step 6: Provision tenant beta (old-style with script copies)
	betaID := tenant.TenantID("beta_corp")
	betaSchema := betaID.SchemaName()
	setupTenantSchemaForSeeder(t, pool, ctx, betaSchema)

	// Insert old-style script-copied sagas
	scripts, err := GetEmbeddedScripts()
	require.NoError(t, err)

	for _, meta := range PlatformDefaults() {
		script := scripts[meta.Filename+".star"]
		_, err := pool.Exec(ctx, `
			INSERT INTO `+betaSchema+`.saga_definition (
				name, version, script, status, is_system,
				display_name, description, created_at, updated_at, activated_at
			) VALUES ($1, 1, $2, 'ACTIVE', true, $3, $4, now(), now(), now())`,
			meta.Name, script, meta.DisplayName, meta.Description)
		require.NoError(t, err)
	}

	// Step 7: Migrate beta to platform refs
	migrationResults, err := overrideSvc.MigrateToPlatformRef(ctx, betaID, false)
	require.NoError(t, err)

	migratedCount := 0
	for _, r := range migrationResults {
		if r.Action == "migrated" {
			migratedCount++
		}
	}
	assert.Equal(t, 3, migratedCount, "all 3 sagas should be migrated")

	// Verify beta's sagas now use platform_ref
	betaCtx := tenant.WithTenant(ctx, betaID)
	betaWithdrawal, err := registry.GetActive(betaCtx, "current_account_withdrawal")
	require.NoError(t, err)
	assert.True(t, betaWithdrawal.IsSystem)
	assert.NotNil(t, betaWithdrawal.PlatformRef, "should now have platform_ref")
	assert.True(t, betaWithdrawal.UsedPlatformFallback, "should use platform fallback after migration")
}

// TestE2E_SeededSagasAreActive verifies that seeded sagas are immediately ACTIVE.
func TestE2E_SeededSagasAreActive(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	platformSync := NewPlatformSync(pool)
	err := platformSync.SyncPlatformDefaults(ctx)
	require.NoError(t, err)

	tenantID := tenant.TenantID("active_check")
	schemaName := tenantID.SchemaName()
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	seeder := NewSeeder(pool)
	err = seeder.SeedTenant(ctx, tenantID)
	require.NoError(t, err)

	// Verify all seeded sagas are ACTIVE
	rows, err := pool.Query(ctx,
		"SELECT name, status FROM "+schemaName+".saga_definition WHERE is_system = true ORDER BY name")
	require.NoError(t, err)
	defer rows.Close()

	var count int
	for rows.Next() {
		var name, status string
		err := rows.Scan(&name, &status)
		require.NoError(t, err)
		assert.Equal(t, "ACTIVE", status, "seeded saga %s should be ACTIVE", name)
		count++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 3, count, "should have 3 seeded sagas")
}

// TestE2E_ReseedingDoesNotDuplicate verifies ON CONFLICT idempotency.
func TestE2E_ReseedingDoesNotDuplicate(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	platformSync := NewPlatformSync(pool)
	err := platformSync.SyncPlatformDefaults(ctx)
	require.NoError(t, err)

	tenantID := tenant.TenantID("reseed_test")
	schemaName := tenantID.SchemaName()
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	seeder := NewSeeder(pool)

	// Seed once
	err = seeder.SeedTenant(ctx, tenantID)
	require.NoError(t, err)

	// Seed again
	err = seeder.SeedTenant(ctx, tenantID)
	require.NoError(t, err)

	// Seed a third time
	err = seeder.SeedTenant(ctx, tenantID)
	require.NoError(t, err)

	// Count should still be exactly 3
	var count int
	err = pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM "+schemaName+".saga_definition WHERE is_system = true").
		Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 3, count, "re-seeding should not create duplicates")
}

// TestE2E_PlatformRefIntegrityConstraint verifies the CHECK constraint:
// platform_ref and script cannot both be set.
func TestE2E_PlatformRefIntegrityConstraint(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	platformSync := NewPlatformSync(pool)
	err := platformSync.SyncPlatformDefaults(ctx)
	require.NoError(t, err)

	tenantID := tenant.TenantID("constraint_test")
	schemaName := tenantID.SchemaName()
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	// Get a platform saga ID
	var platformRefID uuid.UUID
	err = pool.QueryRow(ctx,
		"SELECT id FROM public.platform_saga_definition LIMIT 1").
		Scan(&platformRefID)
	require.NoError(t, err)

	// Try to insert with BOTH platform_ref and script - should fail
	_, err = pool.Exec(ctx, `
		INSERT INTO `+schemaName+`.saga_definition (
			name, version, script, platform_ref, status, is_system,
			created_at, updated_at
		) VALUES ('bad_saga', 1, 'some script', $1, 'DRAFT', false, now(), now())`,
		platformRefID)
	require.Error(t, err, "should reject saga with both platform_ref and script")
	assert.Contains(t, err.Error(), "23514", "should be a CHECK constraint violation")
}

// TestE2E_MigratorReport verifies the FormatReport function produces readable output.
func TestE2E_MigratorReport(t *testing.T) {
	summaries := []TenantMigrationSummary{
		{
			TenantID: "acme_corp",
			Results: []MigrationResult{
				{SagaName: "withdrawal", Action: "migrated", Reason: "100% similar"},
				{SagaName: "deposit", Action: "migrated", Reason: "99% similar"},
				{SagaName: "custom_saga", Action: "skipped", Reason: "no matching platform saga"},
			},
		},
		{
			TenantID: "beta_corp",
			Results: []MigrationResult{
				{SagaName: "withdrawal", Action: "skipped", Reason: "already has platform_ref"},
			},
		},
	}

	report := FormatReport(summaries, false)
	assert.Contains(t, report, "acme_corp")
	assert.Contains(t, report, "beta_corp")
	assert.Contains(t, report, "[migrated]")
	assert.Contains(t, report, "[skipped]")
	assert.Contains(t, report, "Migrated: 2")
	assert.Contains(t, report, "Skipped: 2")
}

// createSagaReferenceTable creates the saga_reference table needed by the validator.
func createSagaReferenceTable(t *testing.T, pool *pgxpool.Pool, ctx context.Context, schemaName string) {
	t.Helper()

	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+schemaName+`.saga_reference (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			saga_definition_id UUID NOT NULL,
			reference_type VARCHAR(32) NOT NULL,
			reference_key VARCHAR(256) NOT NULL,
			instrument_code VARCHAR(64),
			attribute_key VARCHAR(128),
			line_number INT NOT NULL DEFAULT 0,
			extracted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT uq_saga_ref UNIQUE (saga_definition_id, reference_type, reference_key)
		)`)
	require.NoError(t, err)
}
