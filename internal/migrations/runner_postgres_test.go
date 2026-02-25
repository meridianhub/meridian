package migrations_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/meridianhub/meridian/internal/migrations"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupTestPostgres starts a PostgreSQL 16 testcontainer and returns a
// superuser DSN and cleanup function.
//
// POSTGRES_HOST_AUTH_METHOD=trust disables password auth so service users
// created with CREATE USER (no password) can connect.
func setupTestPostgres(t *testing.T) (string, func()) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("defaultdb"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		// POSTGRES_HOST_AUTH_METHOD=trust makes pg_hba.conf allow all connections
		// without password. This lets service users created with CREATE USER (no
		// password) authenticate. Safe for isolated test containers.
		testcontainers.WithEnv(map[string]string{"POSTGRES_HOST_AUTH_METHOD": "trust"}),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(context.Background())
		t.Fatalf("get PostgreSQL connection string: %v", err)
	}

	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = container.Terminate(cleanupCtx)
	}

	return connStr, cleanup
}

// replaceDSNDatabasePG parses a standard postgres:// DSN and replaces the database name.
func replaceDSNDatabasePG(t *testing.T, dsn, database string) string {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.User, cfg.Password, cfg.Host, port, database)
}

func TestRunMigrations_Postgres(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("skipping integration test; use -short=false or set CI=true")
	}

	t.Setenv("DB_DRIVER", "postgres")

	dsn, cleanup := setupTestPostgres(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := context.Background()
	testFS := testMigrationFS()

	err := migrations.RunMigrations(ctx, testFS, dsn, logger)
	if err != nil {
		t.Fatalf("RunMigrations on PostgreSQL: %v", err)
	}

	// Verify migrations were recorded in current-account database.
	caDSN := replaceDSNDatabasePG(t, dsn, "meridian_current_account")
	caConn, err := pgx.Connect(ctx, caDSN+"&default_query_exec_mode=simple_protocol")
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
			WHERE table_schema = 'public'
			  AND table_name = 'account'
			  AND column_name = 'status'
		)`,
	).Scan(&hasStatus)
	if err != nil {
		t.Fatalf("check status column: %v", err)
	}
	if !hasStatus {
		t.Error("account table missing status column")
	}
}

func TestRunMigrations_Postgres_ScramAuth(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("skipping integration test; use -short=false or set CI=true")
	}

	t.Setenv("DB_DRIVER", "postgres")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Start PostgreSQL WITHOUT trust auth (uses default scram-sha-256).
	// This simulates production where service users have no passwords.
	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("defaultdb"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("secretpassword"),
		// NO POSTGRES_HOST_AUTH_METHOD=trust - uses default scram-sha-256
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = container.Terminate(cleanupCtx)
	}()

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get PostgreSQL connection string: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	testFS := testMigrationFS()

	// This should succeed using superuser credentials for migrations.
	err = migrations.RunMigrations(ctx, testFS, connStr, logger)
	if err != nil {
		t.Fatalf("migrations should succeed with scram-sha-256 auth: %v", err)
	}

	// Verify migrations were applied.
	caDSN := replaceDSNDatabasePG(t, connStr, "meridian_current_account")
	caConn, err := pgx.Connect(ctx, caDSN+"&default_query_exec_mode=simple_protocol")
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
		t.Errorf("expected 2 migrations recorded, got %d", count)
	}
}

func TestRunMigrations_Postgres_Idempotent(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("skipping integration test; use -short=false or set CI=true")
	}

	t.Setenv("DB_DRIVER", "postgres")

	dsn, cleanup := setupTestPostgres(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()
	testFS := testMigrationFS()

	// Run first time.
	if err := migrations.RunMigrations(ctx, testFS, dsn, logger); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}

	// Run second time — should be idempotent.
	if err := migrations.RunMigrations(ctx, testFS, dsn, logger); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	// Verify no duplicate records.
	caDSN := replaceDSNDatabasePG(t, dsn, "meridian_current_account")
	caConn, err := pgx.Connect(ctx, caDSN+"&default_query_exec_mode=simple_protocol")
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
