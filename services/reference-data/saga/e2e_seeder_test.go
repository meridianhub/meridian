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

// TestE2E_ProvisioningOverride exercises the full lifecycle:
// 1. Sync platform defaults
// 2. Provision a new tenant (seeder copies scripts directly into tenant schema)
// 3. Verify scripts are resolved from tenant schema (no public fallback)
// 4. Override a saga with custom script
// 5. Verify override takes priority over system default
func TestE2E_ProvisioningOverride(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Step 1: Sync platform defaults
	platformSync := NewPlatformSync(pool)
	err := platformSync.SyncPlatformDefaults(ctx)
	require.NoError(t, err)

	// Step 2: Provision tenant alpha (scripts copied directly into tenant schema)
	alphaID := tenant.TenantID("alpha_corp")
	alphaSchema := alphaID.SchemaName()
	setupTenantSchemaForSeeder(t, pool, ctx, alphaSchema)
	createSagaReferenceTable(t, pool, ctx, alphaSchema)

	seeder := NewSeeder(pool)
	err = seeder.SeedTenant(ctx, alphaID)
	require.NoError(t, err)

	// Step 3: Verify scripts are self-contained in tenant schema
	alphaCtx := tenant.WithTenant(ctx, alphaID)
	registry := NewPostgresRegistry(pool, nil)

	activeDef, err := registry.GetActive(alphaCtx, "current_account_withdrawal")
	require.NoError(t, err)
	assert.True(t, activeDef.IsSystem, "should resolve system saga")
	assert.False(t, activeDef.UsedPlatformFallback,
		"should not use platform fallback - scripts copied directly")
	assert.NotEmpty(t, activeDef.ResolvedScript, "resolved script should not be empty")
	assert.NotEmpty(t, activeDef.Script, "script should be copied directly into tenant")
	assert.Nil(t, activeDef.PlatformRef,
		"platform_ref should be nil - scripts are self-contained")

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

	// Non-overridden sagas should resolve from tenant's own script copy
	depositDef, err := registry.GetActive(alphaCtx, "current_account_deposit")
	require.NoError(t, err)
	assert.True(t, depositDef.IsSystem, "deposit should still use system saga")
	assert.False(t, depositDef.UsedPlatformFallback,
		"deposit should use copied script, not platform fallback")
	assert.NotEmpty(t, depositDef.ResolvedScript, "deposit should have script content")
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
	quoted := pq.QuoteIdentifier(schemaName)
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	seeder := NewSeeder(pool)
	err = seeder.SeedTenant(ctx, tenantID)
	require.NoError(t, err)

	// Verify all seeded sagas are ACTIVE with scripts copied directly
	rows, err := pool.Query(ctx,
		fmt.Sprintf("SELECT name, status, script FROM %s.saga_definition WHERE is_system = true ORDER BY name", quoted))
	require.NoError(t, err)
	defer rows.Close()

	var count int
	for rows.Next() {
		var name, status string
		var script *string
		err := rows.Scan(&name, &status, &script)
		require.NoError(t, err)
		assert.Equal(t, "ACTIVE", status, "seeded saga %s should be ACTIVE", name)
		assert.NotNil(t, script, "seeded saga %s should have script content (not NULL)", name)
		if script != nil {
			assert.NotEmpty(t, *script, "seeded saga %s should have non-empty script", name)
		}
		count++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 8, count, "should have 8 seeded sagas")
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
	quoted := pq.QuoteIdentifier(schemaName)
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

	// Count should still be exactly 8
	var count int
	err = pool.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s.saga_definition WHERE is_system = true", quoted)).
		Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 8, count, "re-seeding should not create duplicates")
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
	quoted := pq.QuoteIdentifier(schemaName)
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	// Get a platform saga ID
	var platformRefID uuid.UUID
	err = pool.QueryRow(ctx,
		"SELECT id FROM public.platform_saga_definition LIMIT 1").
		Scan(&platformRefID)
	require.NoError(t, err)

	// Try to insert with BOTH platform_ref and script - should fail
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.saga_definition (
			name, version, script, platform_ref, status, is_system,
			created_at, updated_at
		) VALUES ('bad_saga', 1, 'some script', $1, 'DRAFT', false, now(), now())`, quoted),
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

	_, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.saga_reference (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			saga_definition_id UUID NOT NULL,
			reference_type VARCHAR(32) NOT NULL,
			reference_key VARCHAR(256) NOT NULL,
			instrument_code VARCHAR(64),
			attribute_key VARCHAR(128),
			line_number INT NOT NULL DEFAULT 0,
			extracted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT uq_saga_ref UNIQUE (saga_definition_id, reference_type, reference_key)
		)`, pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)
}
