package migrations_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/meridianhub/meridian/internal/migrations"
	"github.com/meridianhub/meridian/services"
)

// TestRealMigrationsApplyCleanlyToCockroachDB runs every production SQL
// migration file (services.MigrationFS - the exact embed.FS the unified
// binary uses for `--migrate`) against a real CockroachDB testcontainer.
//
// shared/platform/testdb/schema_validation_test.go exercises these files
// against PostgreSQL only, and internal/migrations/runner_test.go exercises
// RunMigrations against CockroachDB with synthetic fixtures only. Neither
// catches CockroachDB-specific DDL violations (PL/pgSQL, partial indexes on
// same-transaction columns, COMMENT ON INDEX syntax, etc. - see the
// CockroachDB migration rules in CLAUDE.md) because neither runs the real
// files against the real engine. This test closes that gap.
func TestRealMigrationsApplyCleanlyToCockroachDB(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("skipping integration test; use -short=false or set CI=true")
	}

	dsn, cleanup := setupTestCockroachDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	// DriverFromEnv defaults to CockroachDB when DB_DRIVER is unset, so the
	// real migration SQL is applied verbatim - no PostgreSQL DDL adaptation.
	if err := migrations.RunMigrations(ctx, services.MigrationFS, dsn, logger); err != nil {
		t.Fatalf("production migration files failed to apply cleanly to CockroachDB: %v", err)
	}

	// Spot-check that a representative service actually recorded its
	// migrations, confirming RunMigrations did real work rather than a
	// silent no-op (e.g. discoverMigrations finding zero files).
	caDSN := replaceDSNDatabase(t, dsn, "meridian_current_account")
	caConn, err := pgx.Connect(ctx, caDSN)
	if err != nil {
		t.Fatalf("connect to meridian_current_account: %v", err)
	}
	defer func() { _ = caConn.Close(ctx) }()

	var count int
	if err := caConn.QueryRow(ctx, `SELECT count(*) FROM _meridian_migrations`).Scan(&count); err != nil {
		t.Fatalf("count applied migrations in current_account: %v", err)
	}
	if count == 0 {
		t.Error("expected current-account migrations to be recorded, got 0")
	}
}

// TestRealMigrations_CockroachDBInvalidMigrationFails proves the guard in
// TestRealMigrationsApplyCleanlyToCockroachDB actually catches CockroachDB
// incompatibilities rather than trivially passing. It applies a migration
// that creates a partial index on a column added earlier in the same
// migration file - forbidden by rule 1 of the CockroachDB migration rules
// in CLAUDE.md ("Never create a partial index on a column added in the
// same migration") because CockroachDB requires the column to be
// committed ("public") before a partial index can reference it. The whole
// file is sent to CockroachDB as a single simple-query message, which
// (like PostgreSQL) executes it as one implicit transaction, so the ADD
// COLUMN has not yet committed when CREATE INDEX runs - and asserts
// RunMigrations surfaces that failure against a real CockroachDB instance.
func TestRealMigrations_CockroachDBInvalidMigrationFails(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("skipping integration test; use -short=false or set CI=true")
	}

	dsn, cleanup := setupTestCockroachDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	invalidFS := fstest.MapFS{
		"current-account/migrations/20240101000001_create_foo.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS foo (id UUID PRIMARY KEY DEFAULT gen_random_uuid());`),
		},
		"current-account/migrations/20240101000002_partial_index_on_new_column.sql": &fstest.MapFile{
			Data: []byte(`
				ALTER TABLE foo ADD COLUMN bar VARCHAR(255);
				CREATE INDEX idx_foo_bar ON foo (bar) WHERE bar IS NOT NULL;
			`),
		},
	}

	err := migrations.RunMigrations(ctx, invalidFS, dsn, logger)
	if err == nil {
		t.Fatal("expected CockroachDB to reject a partial index on a same-migration column, but RunMigrations succeeded - the CockroachDB compatibility guard is not catching this class of bug")
	}
	t.Logf("CockroachDB correctly rejected the partial-index-on-new-column migration: %v", err)
}
