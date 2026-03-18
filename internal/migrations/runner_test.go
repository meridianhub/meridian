package migrations_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/meridianhub/meridian/internal/migrations"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// setupTestCockroachDB starts a CockroachDB testcontainer with retry logic
// and returns a root DSN and cleanup function.
func setupTestCockroachDB(t *testing.T) (string, func()) {
	t.Helper()

	container, cleanup := testdb.StartCockroachContainer(t, "defaultdb")
	dsn := testdb.CockroachDSN(t, container)
	return dsn, cleanup
}

// testMigrationFS creates a test embed.FS with dummy SQL migrations for two services.
func testMigrationFS() fstest.MapFS {
	return fstest.MapFS{
		"current-account/migrations/20240101000001_create_accounts.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS account (
				id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				name VARCHAR(255) NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT now()
			);`),
		},
		"current-account/migrations/20240101000002_add_status.sql": &fstest.MapFile{
			Data: []byte(`ALTER TABLE account ADD COLUMN IF NOT EXISTS status VARCHAR(50) DEFAULT 'ACTIVE';`),
		},
		"party/migrations/20240101000001_create_parties.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS party (
				id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				name VARCHAR(255) NOT NULL
			);`),
		},
	}
}

func TestRunMigrations(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("skipping integration test; use -short=false or set CI=true")
	}

	dsn, cleanup := setupTestCockroachDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := context.Background()
	testFS := testMigrationFS()

	err := migrations.RunMigrations(ctx, testFS, dsn, logger)
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Verify databases were created.
	rootConn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to verify: %v", err)
	}
	defer rootConn.Close(ctx)

	for _, dbName := range []string{"meridian_current_account", "meridian_party"} {
		var exists bool
		err := rootConn.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM [SHOW DATABASES] WHERE database_name = $1)`,
			dbName,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("check database %s: %v", dbName, err)
		}
		if !exists {
			t.Errorf("database %s was not created", dbName)
		}
	}

	// Verify migrations were recorded in current-account database.
	caDSN := replaceDSNDatabase(t, dsn, "meridian_current_account")
	caConn, err := pgx.Connect(ctx, caDSN)
	if err != nil {
		t.Fatalf("connect to meridian_current_account: %v", err)
	}
	defer caConn.Close(ctx)

	var count int
	err = caConn.QueryRow(ctx, `SELECT count(*) FROM _meridian_migrations`).Scan(&count)
	if err != nil {
		t.Fatalf("count migrations in current_account: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 migrations recorded in current_account, got %d", count)
	}

	// Verify the account table exists with the status column.
	var hasStatus bool
	err = caConn.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'account' AND column_name = 'status'
		)`,
	).Scan(&hasStatus)
	if err != nil {
		t.Fatalf("check status column: %v", err)
	}
	if !hasStatus {
		t.Error("account table missing status column")
	}

	// Verify party database migrations.
	partyDSN := replaceDSNDatabase(t, dsn, "meridian_party")
	partyConn, err := pgx.Connect(ctx, partyDSN)
	if err != nil {
		t.Fatalf("connect to meridian_party: %v", err)
	}
	defer partyConn.Close(ctx)

	err = partyConn.QueryRow(ctx, `SELECT count(*) FROM _meridian_migrations`).Scan(&count)
	if err != nil {
		t.Fatalf("count migrations in party: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 migration recorded in party, got %d", count)
	}
}

func TestRunMigrations_Idempotent(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("skipping integration test; use -short=false or set CI=true")
	}

	dsn, cleanup := setupTestCockroachDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()
	testFS := testMigrationFS()

	// Run first time.
	if err := migrations.RunMigrations(ctx, testFS, dsn, logger); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}

	// Run second time - should be idempotent.
	if err := migrations.RunMigrations(ctx, testFS, dsn, logger); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	// Verify no duplicate records.
	caDSN := replaceDSNDatabase(t, dsn, "meridian_current_account")
	caConn, err := pgx.Connect(ctx, caDSN)
	if err != nil {
		t.Fatalf("connect to meridian_current_account: %v", err)
	}
	defer caConn.Close(ctx)

	var count int
	err = caConn.QueryRow(ctx, `SELECT count(*) FROM _meridian_migrations`).Scan(&count)
	if err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 migrations (no duplicates), got %d", count)
	}
}

func TestRunMigrations_SharedDatabase(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("skipping integration test; use -short=false or set CI=true")
	}

	dsn, cleanup := setupTestCockroachDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := context.Background()

	// Both tenant and control-plane target meridian_platform.
	testFS := fstest.MapFS{
		"control-plane/migrations/20240101000001_staff.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS staff_user (
				id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				email VARCHAR(255) NOT NULL
			);`),
		},
		"tenant/migrations/20240101000001_tenants.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS tenant_registry (
				id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				name VARCHAR(255) NOT NULL
			);`),
		},
	}

	if err := migrations.RunMigrations(ctx, testFS, dsn, logger); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Both tables should exist in meridian_platform.
	platformDSN := replaceDSNDatabase(t, dsn, "meridian_platform")
	conn, err := pgx.Connect(ctx, platformDSN)
	if err != nil {
		t.Fatalf("connect to meridian_platform: %v", err)
	}
	defer conn.Close(ctx)

	for _, table := range []string{"staff_user", "tenant_registry"} {
		var exists bool
		err := conn.QueryRow(ctx,
			`SELECT EXISTS(
				SELECT 1 FROM information_schema.tables
				WHERE table_name = $1
			)`,
			table,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s not found in meridian_platform", table)
		}
	}

	// Verify migration tracking records both services.
	var count int
	err = conn.QueryRow(ctx, `SELECT count(DISTINCT service) FROM _meridian_migrations`).Scan(&count)
	if err != nil {
		t.Fatalf("count services: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 distinct services in migration tracking, got %d", count)
	}
}

func TestRunMigrations_NoMigrations(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("skipping integration test; use -short=false or set CI=true")
	}

	dsn, cleanup := setupTestCockroachDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	emptyFS := fstest.MapFS{}

	if err := migrations.RunMigrations(ctx, emptyFS, dsn, logger); err != nil {
		t.Fatalf("RunMigrations with empty FS: %v", err)
	}
}

func TestRunMigrations_FailingMigration(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("skipping integration test; use -short=false or set CI=true")
	}

	dsn, cleanup := setupTestCockroachDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	testFS := fstest.MapFS{
		"current-account/migrations/20240101000001_good.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS good_table (id UUID PRIMARY KEY DEFAULT gen_random_uuid());`),
		},
		"current-account/migrations/20240101000002_bad.sql": &fstest.MapFile{
			Data: []byte(`THIS IS NOT VALID SQL;`),
		},
	}

	err := migrations.RunMigrations(ctx, testFS, dsn, logger)
	if err == nil {
		t.Fatal("expected error for invalid SQL migration, got nil")
	}

	// The first migration should have been applied, but the second should have failed.
	caDSN := replaceDSNDatabase(t, dsn, "meridian_current_account")
	caConn, err2 := pgx.Connect(ctx, caDSN)
	if err2 != nil {
		t.Fatalf("connect to meridian_current_account: %v", err2)
	}
	defer caConn.Close(ctx)

	var count int
	err2 = caConn.QueryRow(ctx, `SELECT count(*) FROM _meridian_migrations`).Scan(&count)
	if err2 != nil {
		t.Fatalf("count migrations: %v", err2)
	}
	if count != 1 {
		t.Errorf("expected 1 migration (first succeeded, second failed), got %d", count)
	}
}

// replaceDSNDatabase parses a pgx DSN and replaces the database name.
// It also connects as root since the service users may not have been
// fully provisioned in insecure CockroachDB mode.
func replaceDSNDatabase(t *testing.T, dsn, database string) string {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	port := cfg.Port
	if port == 0 {
		port = 26257
	}
	return fmt.Sprintf("postgres://%s@%s:%d/%s?sslmode=disable",
		cfg.User, cfg.Host, port, database)
}
