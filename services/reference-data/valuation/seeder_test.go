package valuation_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/reference-data/valuation"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSeederContext(t *testing.T, pool *pgxpool.Pool, tenantID string) (context.Context, tenant.TenantID) {
	t.Helper()
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, tenantID, "reference-data")
	t.Cleanup(cleanup)
	tid := tenant.TenantID(tenantID)
	return ctx, tid
}

func TestSeederSeedTenant(t *testing.T) {
	pool := testdb.NewTestPool(t)
	_, tid := setupSeederContext(t, pool, "seeder-test")
	seeder := valuation.NewSeeder(pool)

	t.Run("seeds all system defaults", func(t *testing.T) {
		err := seeder.SeedTenant(context.Background(), tid)
		require.NoError(t, err)

		// Verify methods were seeded
		schemaName := tid.SchemaName()
		var methodCount int
		err = pool.QueryRow(context.Background(),
			fmt.Sprintf("SELECT COUNT(*) FROM %s.valuation_method WHERE is_system = true", pq.QuoteIdentifier(schemaName)),
		).Scan(&methodCount)
		require.NoError(t, err)
		assert.Equal(t, len(valuation.DefaultMethods()), methodCount)

		// Verify policies were seeded
		var policyCount int
		err = pool.QueryRow(context.Background(),
			fmt.Sprintf("SELECT COUNT(*) FROM %s.valuation_policy WHERE is_system = true", pq.QuoteIdentifier(schemaName)),
		).Scan(&policyCount)
		require.NoError(t, err)
		assert.Equal(t, len(valuation.DefaultPolicies()), policyCount)
	})

	t.Run("seeding is idempotent", func(t *testing.T) {
		// Seed again - should not error or create duplicates
		err := seeder.SeedTenant(context.Background(), tid)
		require.NoError(t, err)

		schemaName := tid.SchemaName()
		var methodCount int
		err = pool.QueryRow(context.Background(),
			fmt.Sprintf("SELECT COUNT(*) FROM %s.valuation_method WHERE is_system = true", pq.QuoteIdentifier(schemaName)),
		).Scan(&methodCount)
		require.NoError(t, err)
		assert.Equal(t, len(valuation.DefaultMethods()), methodCount)
	})

	t.Run("system defaults are ACTIVE", func(t *testing.T) {
		schemaName := tid.SchemaName()

		var activeMethods int
		err := pool.QueryRow(context.Background(),
			fmt.Sprintf("SELECT COUNT(*) FROM %s.valuation_method WHERE is_system = true AND lifecycle_status = 'ACTIVE'", pq.QuoteIdentifier(schemaName)),
		).Scan(&activeMethods)
		require.NoError(t, err)
		assert.Equal(t, len(valuation.DefaultMethods()), activeMethods)

		var activePolicies int
		err = pool.QueryRow(context.Background(),
			fmt.Sprintf("SELECT COUNT(*) FROM %s.valuation_policy WHERE is_system = true AND lifecycle_status = 'ACTIVE'", pq.QuoteIdentifier(schemaName)),
		).Scan(&activePolicies)
		require.NoError(t, err)
		assert.Equal(t, len(valuation.DefaultPolicies()), activePolicies)
	})

	t.Run("identity methods have correct instruments", func(t *testing.T) {
		schemaName := tid.SchemaName()

		var usdInput, usdOutput string
		err := pool.QueryRow(context.Background(),
			fmt.Sprintf("SELECT input_instrument, output_instrument FROM %s.valuation_method WHERE name = 'SYSTEM_IDENTITY_USD'", pq.QuoteIdentifier(schemaName)),
		).Scan(&usdInput, &usdOutput)
		require.NoError(t, err)
		assert.Equal(t, "USD", usdInput)
		assert.Equal(t, "USD", usdOutput)
	})
}
