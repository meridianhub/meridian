package accounttype_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedPlatformBlueprints_Success(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "tenant-blueprint-seed-1")

	instruments := []string{"GBP", "USD", "EUR", "TONNE_CO2E", "KWH"}
	for _, code := range instruments {
		seedInstrument(t, pool, ctx, code)
	}

	err := accounttype.SeedPlatformBlueprints(ctx, reg)
	require.NoError(t, err)

	// Verify all 12 blueprints are ACTIVE
	active, err := reg.ListActive(ctx)
	require.NoError(t, err)
	assert.Len(t, active, 12, "expected 12 ACTIVE platform blueprints")

	// Verify all are system definitions
	for _, def := range active {
		assert.True(t, def.IsSystem, "blueprint %s should be a system definition", def.Code)
		assert.Equal(t, accounttype.StatusActive, def.Status)
	}
}

func TestSeedPlatformBlueprints_Idempotent(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "tenant-blueprint-idempotent")

	instruments := []string{"GBP", "USD", "EUR", "TONNE_CO2E", "KWH"}
	for _, code := range instruments {
		seedInstrument(t, pool, ctx, code)
	}

	// First seed
	err := accounttype.SeedPlatformBlueprints(ctx, reg)
	require.NoError(t, err)

	// Second seed should be idempotent
	err = accounttype.SeedPlatformBlueprints(ctx, reg)
	require.NoError(t, err)

	// Count should still be 12
	active, err := reg.ListActive(ctx)
	require.NoError(t, err)
	assert.Len(t, active, 12, "idempotent seed should not create duplicates")
}

func TestSeedPlatformBlueprints_DeterministicUUIDs(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "tenant-blueprint-uuids")

	instruments := []string{"GBP", "USD", "EUR", "TONNE_CO2E", "KWH"}
	for _, code := range instruments {
		seedInstrument(t, pool, ctx, code)
	}

	err := accounttype.SeedPlatformBlueprints(ctx, reg)
	require.NoError(t, err)

	// Deterministic UUID namespace (must match seeder.go)
	ns := uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")

	// Verify specific blueprints have deterministic UUIDs
	codes := []string{
		"CURRENT_ACCOUNT_GBP", "CURRENT_ACCOUNT_USD", "CURRENT_ACCOUNT_EUR",
		"CLEARING_GBP", "INVENTORY_KWH", "CARBON_CREDIT_HOLDING",
	}
	for _, code := range codes {
		expected := uuid.NewSHA1(ns, []byte(code))
		def, err := reg.GetDefinition(ctx, code, 1)
		require.NoError(t, err, "blueprint %s should exist", code)
		assert.Equal(t, expected, def.ID, "blueprint %s should have deterministic UUID", code)
	}
}

func TestSeedPlatformBlueprints_InventoryKWH_Attributes(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "tenant-blueprint-kwh")

	instruments := []string{"GBP", "USD", "EUR", "TONNE_CO2E", "KWH"}
	for _, code := range instruments {
		seedInstrument(t, pool, ctx, code)
	}

	err := accounttype.SeedPlatformBlueprints(ctx, reg)
	require.NoError(t, err)

	def, err := reg.GetDefinition(ctx, "INVENTORY_KWH", 1)
	require.NoError(t, err)

	assert.Equal(t, accounttype.BehaviorClassInventory, def.BehaviorClass)
	assert.Equal(t, accounttype.NormalBalanceDebit, def.NormalBalance)
	assert.Equal(t, "KWH", def.InstrumentCode)
	assert.True(t, def.IsSystem)
}

func TestSeedPlatformBlueprints_SystemProtection_UpdateRejected(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "tenant-blueprint-update-guard")

	instruments := []string{"GBP", "USD", "EUR", "TONNE_CO2E", "KWH"}
	for _, code := range instruments {
		seedInstrument(t, pool, ctx, code)
	}

	err := accounttype.SeedPlatformBlueprints(ctx, reg)
	require.NoError(t, err)

	// Attempt to update a system blueprint's display name - should fail
	updates := &accounttype.Definition{DisplayName: "Hijacked Name"}
	err = reg.UpdateDefinition(ctx, "CURRENT_ACCOUNT_GBP", 1, updates)
	require.ErrorIs(t, err, accounttype.ErrSystemAccountTypeReadOnly)
}

func TestSeedPlatformBlueprints_SystemProtection_DeprecateRejected(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "tenant-blueprint-deprecate-guard")

	instruments := []string{"GBP", "USD", "EUR", "TONNE_CO2E", "KWH"}
	for _, code := range instruments {
		seedInstrument(t, pool, ctx, code)
	}

	err := accounttype.SeedPlatformBlueprints(ctx, reg)
	require.NoError(t, err)

	// Attempt to deprecate a system blueprint - should fail
	err = reg.DeprecateAccountType(ctx, "CLEARING_GBP", 1, nil)
	require.ErrorIs(t, err, accounttype.ErrSystemAccountTypeReadOnly)
}
