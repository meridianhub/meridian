// Package testdb provides utilities for setting up test databases.
// It offers CockroachDB testcontainers for integration testing with production parity.
package testdb

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	// CockroachDBImage is the default CockroachDB container image.
	CockroachDBImage = "cockroachdb/cockroach:v24.3.0"

	// cockroachStartupTimeout is the per-attempt timeout for starting a CockroachDB container.
	cockroachStartupTimeout = 120 * time.Second

	// cockroachMaxRetries is the number of attempts to start the container.
	// CI runners occasionally hit Docker daemon contention or image pull delays.
	cockroachMaxRetries = 3
)

// StartCockroachContainer starts a CockroachDB testcontainer with retry logic
// to handle transient Docker failures on CI. Returns the container and a cleanup
// function. The caller is responsible for calling cleanup when done.
func StartCockroachContainer(t *testing.T, database string) (*cockroachdb.CockroachDBContainer, func()) {
	t.Helper()

	if database == "" {
		database = "test_db"
	}

	allOpts := []testcontainers.ContainerCustomizer{
		cockroachdb.WithDatabase(database),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	}

	var container *cockroachdb.CockroachDBContainer
	var lastErr error

	for attempt := 1; attempt <= cockroachMaxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), cockroachStartupTimeout)

		var err error
		container, err = cockroachdb.Run(ctx, CockroachDBImage, allOpts...)
		cancel()

		if err == nil {
			break
		}

		lastErr = err
		t.Logf("CockroachDB container start attempt %d/%d failed: %v", attempt, cockroachMaxRetries, err)

		// Clean up the failed container if it was partially created
		if container != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = container.Terminate(cleanupCtx)
			cleanupCancel()
			container = nil
		}
	}

	if container == nil {
		t.Fatalf("Failed to start CockroachDB container after %d attempts: %v", cockroachMaxRetries, lastErr)
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			_ = container.Terminate(cleanupCtx)
		})
	}
	// Register with t.Cleanup so the container is terminated even if the
	// caller fatals before deferring the returned cleanup function.
	t.Cleanup(cleanup)

	return container, cleanup
}

// CockroachDSN returns a PostgreSQL-compatible DSN from a CockroachDB container.
func CockroachDSN(t *testing.T, container *cockroachdb.CockroachDBContainer) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connConfig, err := container.ConnectionConfig(ctx)
	if err != nil {
		t.Fatalf("Failed to get CockroachDB connection config: %v", err)
	}

	return fmt.Sprintf("postgres://%s@%s:%d/%s?sslmode=disable",
		connConfig.User, connConfig.Host, connConfig.Port, connConfig.Database)
}

// NewCockroachTestPool starts a CockroachDB testcontainer and returns a
// pgxpool.Pool connected to it, for pgx-based repositories that need
// CockroachDB production parity.
//
// Use this instead of NewTestPool (which is Postgres-backed) when the code
// under test talks to CockroachDB, and instead of SetupCockroachDB (which
// returns a *gorm.DB) when the code under test requires a *pgxpool.Pool.
//
// The pool and container are cleaned up automatically via t.Cleanup. The
// optional WithMigrations option applies a service's SQL migrations; omit it
// to manage schema setup directly in the test.
func NewCockroachTestPool(t *testing.T, opts ...PoolOption) *pgxpool.Pool {
	t.Helper()

	cfg := &poolConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	container, _ := StartCockroachContainer(t, "test_db")
	dsn := CockroachDSN(t, container)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("Failed to create CockroachDB connection pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if cfg.migrations != "" {
		applyMigrationsWithPgx(t, pool, cfg.migrations)
	}

	return pool
}

// SetupCockroachDB creates a CockroachDB testcontainer for integration testing.
// It returns a configured GORM database connection and a cleanup function.
//
// CockroachDB uses the PostgreSQL wire protocol, so we can use the postgres GORM driver.
// This is useful for testing migration compatibility between PostgreSQL and CockroachDB.
//
// Usage:
//
//	func TestMigrationsCockroachDB(t *testing.T) {
//	    db, cleanup := testdb.SetupCockroachDB(t)
//	    defer cleanup()
//
//	    // Run Atlas migrations or test queries...
//	}
//
// For debugging, enable verbose logging:
//
//	db, cleanup := testdb.SetupCockroachDB(t, nil, WithLogLevel(logger.Info))
//
// Known CockroachDB limitations to test for:
//   - ALTER COLUMN TYPE in transactions (not supported)
//   - PL/pgSQL triggers (limited support)
//   - Range types like TSTZRANGE (not supported)
func SetupCockroachDB(t *testing.T, models []interface{}, opts ...PostgresOption) (*gorm.DB, func()) {
	t.Helper()

	// Apply configuration options (reuse PostgresOption for consistency)
	cfg := &postgresConfig{
		logLevel: logger.Silent,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	crdbContainer, containerCleanup := StartCockroachContainer(t, "test_db")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get connection config (CockroachDB uses PostgreSQL wire protocol)
	// Note: ConnectionConfig returns pgx.ConnConfig which gives us a proper connection string
	// The ConnectionString() method returns a registered config reference, not a URL
	connConfig, err := crdbContainer.ConnectionConfig(ctx)
	if err != nil {
		containerCleanup()
		t.Fatalf("Failed to get connection config: %v", err)
	}
	connStr := connConfig.ConnString()

	// Create GORM connection using postgres driver (CockroachDB is wire-compatible)
	db, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(cfg.logLevel),
	})
	if err != nil {
		containerCleanup()
		t.Fatalf("Failed to connect to database: %v", err)
	}

	// Create schemas and run migrations for provided models (if any)
	if len(models) > 0 {
		schemas := extractSchemasFromModels(models)
		createSchemas(t, db, schemas)

		if err := db.AutoMigrate(models...); err != nil {
			containerCleanup()
			t.Fatalf("Failed to migrate database: %v", err)
		}
	}

	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		containerCleanup()
	}

	return db, cleanup
}
