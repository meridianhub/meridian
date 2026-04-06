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

// TestE2E_SeededSagasAreActive verifies that seeded sagas are immediately ACTIVE.
func TestE2E_SeededSagasAreActive(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	tenantID := tenant.TenantID("active_check")
	schemaName := tenantID.SchemaName()
	quoted := pq.QuoteIdentifier(schemaName)
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	seeder := NewSeeder(pool)
	err := seeder.SeedTenant(ctx, tenantID)
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

	tenantID := tenant.TenantID("reseed_test")
	schemaName := tenantID.SchemaName()
	quoted := pq.QuoteIdentifier(schemaName)
	setupTenantSchemaForSeeder(t, pool, ctx, schemaName)

	seeder := NewSeeder(pool)

	// Seed once
	err := seeder.SeedTenant(ctx, tenantID)
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
