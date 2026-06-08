package registry_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countSystemInstruments returns the number of system instrument definitions
// in the tenant schema for the given context.
func countSystemInstruments(t *testing.T, pool *pgxpool.Pool, ctx context.Context) int {
	t.Helper()

	tenantID, ok := tenant.FromContext(ctx)
	require.True(t, ok)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	var count int
	err = tx.QueryRow(ctx, "SELECT COUNT(*) FROM instrument_definition WHERE is_system = true").Scan(&count)
	require.NoError(t, err)

	return count
}

func TestNewInstrumentSeeder(t *testing.T) {
	pool := &pgxpool.Pool{}
	seeder := registry.NewInstrumentSeeder(pool)
	require.NotNil(t, seeder)
}

func TestInstrumentSeeder_SeedTenant(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	_ = reg
	ctx := setupTenantContext(t, pool, "seed-tenant-1")

	seeder := registry.NewInstrumentSeeder(pool)

	t.Run("seeds all platform instruments", func(t *testing.T) {
		tenantID, ok := tenant.FromContext(ctx)
		require.True(t, ok)

		require.Equal(t, 0, countSystemInstruments(t, pool, ctx))

		err := seeder.SeedTenant(ctx, tenantID)
		require.NoError(t, err)

		// platformInstruments defines GBP, USD, EUR, TONNE_CO2E, KWH.
		assert.Equal(t, 5, countSystemInstruments(t, pool, ctx))
	})

	t.Run("seeded instruments carry expected attributes", func(t *testing.T) {
		schemaName, _ := tenant.FromContext(ctx)

		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName.SchemaName())))
		require.NoError(t, err)

		var (
			dimension string
			precision int
			status    string
			isSystem  bool
			version   int
		)
		err = tx.QueryRow(ctx, `
			SELECT dimension, precision, status, is_system, version
			FROM instrument_definition WHERE code = 'KWH'`).
			Scan(&dimension, &precision, &status, &isSystem, &version)
		require.NoError(t, err)

		assert.Equal(t, string(registry.DimensionEnergy), dimension)
		assert.Equal(t, 3, precision)
		assert.Equal(t, "ACTIVE", status)
		assert.True(t, isSystem)
		assert.Equal(t, 1, version)
	})

	t.Run("re-seed is idempotent", func(t *testing.T) {
		tenantID, ok := tenant.FromContext(ctx)
		require.True(t, ok)

		// Already seeded above; re-running must not error or duplicate rows.
		err := seeder.SeedTenant(ctx, tenantID)
		require.NoError(t, err)

		assert.Equal(t, 5, countSystemInstruments(t, pool, ctx))
	})
}

func TestInstrumentSeeder_SeedTenant_FreshTenant(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	_ = reg
	ctx := setupTenantContext(t, pool, "seed-tenant-2")

	seeder := registry.NewInstrumentSeeder(pool)
	tenantID, ok := tenant.FromContext(ctx)
	require.True(t, ok)

	err := seeder.SeedTenant(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, 5, countSystemInstruments(t, pool, ctx))
}

func TestInstrumentSeeder_SeedTenant_MissingSchema(t *testing.T) {
	_, pool := setupTestRegistry(t)

	seeder := registry.NewInstrumentSeeder(pool)

	// A tenant whose schema was never provisioned: the instrument_definition
	// table does not exist, so the seed insert must surface an error.
	tenantID := tenant.TenantID("nonexistent-tenant")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	err := seeder.SeedTenant(ctx, tenantID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seed")
}

func TestInstrumentSeeder_AsPostProvisioningHook(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	_ = reg
	ctx := setupTenantContext(t, pool, "seed-tenant-hook")

	seeder := registry.NewInstrumentSeeder(pool)
	tenantID, ok := tenant.FromContext(ctx)
	require.True(t, ok)

	hook := seeder.AsPostProvisioningHook()
	require.NotNil(t, hook)

	err := hook(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, 5, countSystemInstruments(t, pool, ctx))
}
