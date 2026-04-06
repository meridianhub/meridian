package saga

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// setupPlatformTestDB creates a CockroachDB test database with the full
// platform saga definition schema for integration tests.
func setupPlatformTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Create a unique database per test for isolation (tests write to public schema tables).
	suffix := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	suffix = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, suffix)
	if len(suffix) > 30 {
		suffix = suffix[:30]
	}
	dbName := fmt.Sprintf("t_%s_%s", suffix, strings.ReplaceAll(uuid.New().String(), "-", "")[:8])

	// Connect to shared CockroachDB container to create per-test database
	adminPool, err := pgxpool.New(ctx, sharedCrdbDSN)
	require.NoError(t, err)
	t.Cleanup(func() { adminPool.Close() })

	_, err = adminPool.Exec(ctx, "CREATE DATABASE "+dbName)
	require.NoError(t, err)

	// Build DSN for the per-test database
	testDSN := replaceDatabaseInDSN(sharedCrdbDSN, dbName)
	pool, err := pgxpool.New(ctx, testDSN)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	// Apply migrations in order
	migrations := []string{
		"20260125000001_platform_saga_definition.sql",
		"20260127000001_fix_platform_saga_unique_constraint.sql",
		"20260128000001_versioned_platform_sagas.sql",
		"20260128000002_versioned_platform_sagas_constraints.sql",
		"20260129000001_bitemporal_platform_sagas.sql",
		"20260129000002_bitemporal_platform_sagas_constraints.sql",
	}

	for _, migration := range migrations {
		migrationPath := filepath.Join("..", "migrations", migration)
		migrationSQL, err := os.ReadFile(migrationPath)
		require.NoError(t, err, "failed to read migration %s", migration)

		_, err = pool.Exec(ctx, string(migrationSQL))
		require.NoError(t, err, "failed to apply migration %s", migration)
	}

	return pool, func() {}
}

// replaceDatabaseInDSN swaps the database name in a PostgreSQL DSN.
func replaceDatabaseInDSN(dsn, newDB string) string {
	// DSN format: postgres://user@host:port/database?params
	// Find the last / before ? and replace the database name
	qIdx := strings.Index(dsn, "?")
	base := dsn
	query := ""
	if qIdx >= 0 {
		base = dsn[:qIdx]
		query = dsn[qIdx:]
	}
	lastSlash := strings.LastIndex(base, "/")
	if lastSlash >= 0 {
		return base[:lastSlash+1] + newDB + query
	}
	return dsn
}
