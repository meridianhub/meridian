// Package testdb provides utilities for setting up test databases.
package testdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PoolOption configures the pgx pool test setup.
type PoolOption func(*poolConfig)

type poolConfig struct {
	migrations string // service name for migrations directory
}

// WithMigrations specifies which service's migrations to apply.
// The migrations directory is expected at services/<service>/migrations/.
func WithMigrations(service string) PoolOption {
	return func(cfg *poolConfig) {
		cfg.migrations = service
	}
}

// NewTestPool creates a pgxpool.Pool connected to a PostgreSQL testcontainer.
// It returns a configured pool that tests should defer Close() on.
//
// Usage:
//
//	func TestMyRepository(t *testing.T) {
//	    pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
//	    defer pool.Close()
//
//	    // Your test code here
//	}
//
// The pool can be used directly with pgx-based repositories.
func NewTestPool(t *testing.T, opts ...PoolOption) *pgxpool.Pool {
	t.Helper()

	// Apply configuration options
	cfg := &poolConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Create context with timeout for container operations
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)

	// Create PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("test_db"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		cancel()
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}

	// Register cleanup
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = pgContainer.Terminate(cleanupCtx)
	})

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		cancel()
		t.Fatalf("Failed to get connection string: %v", err)
	}

	// Create pgxpool
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		cancel()
		t.Fatalf("Failed to create connection pool: %v", err)
	}

	// Cancel setup context now that pool is created
	cancel()

	// Register pool cleanup
	t.Cleanup(func() {
		pool.Close()
	})

	// Apply migrations if specified
	if cfg.migrations != "" {
		applyMigrationsWithPgx(t, pool, cfg.migrations)
	}

	return pool
}

// applyMigrationsWithPgx reads and applies SQL migrations from services/<service>/migrations/.
func applyMigrationsWithPgx(t *testing.T, pool *pgxpool.Pool, service string) {
	t.Helper()

	// Find migrations directory - try multiple paths for test execution contexts
	var migrationsDir string
	possiblePaths := []string{
		filepath.Join("services", service, "migrations"),
		filepath.Join("..", "..", "services", service, "migrations"),
		filepath.Join("..", "..", "..", "services", service, "migrations"),
		filepath.Join("..", "..", "..", "..", "services", service, "migrations"),
	}

	for _, path := range possiblePaths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			migrationsDir = path
			break
		}
	}

	if migrationsDir == "" {
		t.Fatalf("Could not find migrations directory for service %s", service)
	}

	// Read migration files in order
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("Failed to read migrations directory: %v", err)
	}

	ctx := context.Background()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".sql" {
			continue
		}

		path := filepath.Join(migrationsDir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("Failed to read migration %s: %v", entry.Name(), err)
		}

		sql := adaptCockroachDDLForPostgres(string(content))
		_, err = pool.Exec(ctx, sql)
		if err != nil {
			t.Fatalf("Failed to apply migration %s: %v", entry.Name(), err)
		}
	}
}

// SetupTenantSchemaForPgx creates a tenant schema and applies migrations.
// Returns a context with tenant ID and a cleanup function.
//
// Usage:
//
//	pool := testdb.NewTestPool(t)
//	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "test-tenant", "reference-data")
//	defer cleanup()
func SetupTenantSchemaForPgx(t *testing.T, pool *pgxpool.Pool, tenantID string, service string) (context.Context, func()) {
	t.Helper()

	tid := tenant.TenantID(tenantID)
	schemaName := tid.SchemaName()

	ctx := context.Background()

	// Create the tenant schema using proper SQL identifier quoting
	_, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
	if err != nil {
		t.Fatalf("Failed to create tenant schema %s: %v", schemaName, err)
	}

	// Apply migrations to tenant schema
	if service != "" {
		applyMigrationsToSchema(t, pool, service, schemaName)
	}

	// Create context with tenant
	tenantCtx := tenant.WithTenant(context.Background(), tid)

	cleanup := func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
	}

	return tenantCtx, cleanup
}

// applyMigrationsToSchema applies migrations to a specific schema.
func applyMigrationsToSchema(t *testing.T, pool *pgxpool.Pool, service string, schemaName string) {
	t.Helper()

	// Find migrations directory
	var migrationsDir string
	possiblePaths := []string{
		filepath.Join("services", service, "migrations"),
		filepath.Join("..", "..", "services", service, "migrations"),
		filepath.Join("..", "..", "..", "services", service, "migrations"),
		filepath.Join("..", "..", "..", "..", "services", service, "migrations"),
	}

	for _, path := range possiblePaths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			migrationsDir = path
			break
		}
	}

	if migrationsDir == "" {
		t.Fatalf("Could not find migrations directory for service %s", service)
	}

	// Read migration files in order
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("Failed to read migrations directory: %v", err)
	}

	ctx := context.Background()

	// Start a transaction and set search_path
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	if err != nil {
		t.Fatalf("Failed to set search_path: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".sql" {
			continue
		}

		path := filepath.Join(migrationsDir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("Failed to read migration %s: %v", entry.Name(), err)
		}

		sql := adaptCockroachDDLForPostgres(string(content))
		_, err = tx.Exec(ctx, sql)
		if err != nil {
			t.Fatalf("Failed to apply migration %s to schema %s: %v", entry.Name(), schemaName, err)
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		t.Fatalf("Failed to commit migrations: %v", err)
	}
}

// adaptCockroachDDLForPostgres rewrites CockroachDB-specific DDL statements to work on PostgreSQL.
//
// Transformations applied:
//
//  1. DROP INDEX CASCADE for constraint-backed unique indexes: CockroachDB requires
//     DROP INDEX CASCADE to drop unique constraints; PostgreSQL requires ALTER TABLE
//     DROP CONSTRAINT.
//
//  2. ADD CONSTRAINT ... CHECK on public-schema tables: These migrations run
//     per-tenant but modify the shared public schema. In tests, multiple tenant schemas
//     are provisioned against the same database, so the second tenant's migration would
//     fail with "constraint already exists". All ADD CONSTRAINT CHECK statements that
//     target public-schema tables are wrapped in a DO block that ignores
//     duplicate_object errors (SQLSTATE 42710), providing idempotent application.
//     This handles both the legacy "ADD CONSTRAINT IF NOT EXISTS" form (CockroachDB
//     extension) and the standard "ADD CONSTRAINT" form used after the PG16 fix.
func adaptCockroachDDLForPostgres(sql string) string {
	result := strings.ReplaceAll(sql,
		`DROP INDEX IF EXISTS "public"."uq_platform_saga_definition_name" CASCADE`,
		`ALTER TABLE "public"."platform_saga_definition" DROP CONSTRAINT IF EXISTS "uq_platform_saga_definition_name"`,
	)
	// Wrap ADD CONSTRAINT ... CHECK statements targeting public-schema tables in a
	// DO block that ignores duplicate_object errors. This handles both:
	//   - "ADD CONSTRAINT IF NOT EXISTS" (CockroachDB extension, removed for PG16 compat)
	//   - "ADD CONSTRAINT" (standard SQL, used after the PG16 compatibility fix)
	//
	// The pattern matches ALTER TABLE public.<table> ADD CONSTRAINT [IF NOT EXISTS]
	// <name> CHECK (<expr>); and wraps it in an exception handler.
	re := regexp.MustCompile(`(?s)(ALTER TABLE\s+public\.\S+\s+)ADD CONSTRAINT(?:\s+IF NOT EXISTS)?\s+(\S+\s+CHECK\s*\([^;]+?\));`)
	result = re.ReplaceAllStringFunc(result, func(match string) string {
		// Normalise: strip "IF NOT EXISTS" if present
		inner := strings.Replace(match, "ADD CONSTRAINT IF NOT EXISTS", "ADD CONSTRAINT", 1)
		// Remove trailing semicolon since it goes inside the DO block
		inner = strings.TrimSuffix(strings.TrimSpace(inner), ";")
		return "DO $compat$ BEGIN " + inner + "; EXCEPTION WHEN duplicate_object THEN NULL; END $compat$;"
	})
	return result
}
