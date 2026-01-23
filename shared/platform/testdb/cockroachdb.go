// Package testdb provides utilities for setting up test databases.
// It offers CockroachDB testcontainers for integration testing with production parity.
package testdb

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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

	// Create context with timeout for container operations
	// CockroachDB takes longer to start than PostgreSQL (~10-15s vs 2-3s)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Create CockroachDB container
	// Note: Do NOT override wait strategy - the module has its own that waits for
	// the database to be created (via WithDatabase) before reporting ready
	crdbContainer, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_db"),
		cockroachdb.WithUser("root"),
		// CockroachDB in insecure mode doesn't require password
		cockroachdb.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("Failed to start CockroachDB container: %v", err)
	}

	// Get connection config (CockroachDB uses PostgreSQL wire protocol)
	// Note: ConnectionConfig returns pgx.ConnConfig which gives us a proper connection string
	// The ConnectionString() method returns a registered config reference, not a URL
	connConfig, err := crdbContainer.ConnectionConfig(ctx)
	if err != nil {
		t.Fatalf("Failed to get connection config: %v", err)
	}
	connStr := connConfig.ConnString()

	// Create GORM connection using postgres driver (CockroachDB is wire-compatible)
	db, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(cfg.logLevel),
	})
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	// Create schemas and run migrations for provided models (if any)
	if len(models) > 0 {
		schemas := extractSchemasFromModels(models)
		createSchemas(t, db, schemas)

		if err := db.AutoMigrate(models...); err != nil {
			t.Fatalf("Failed to migrate database: %v", err)
		}
	}

	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = crdbContainer.Terminate(cleanupCtx)
	}

	return db, cleanup
}

// CockroachDBConnectionInfo holds connection details for external tools like Atlas CLI.
type CockroachDBConnectionInfo struct {
	Host     string
	Port     string
	Database string
	User     string
	ConnStr  string // Uses crdb:// scheme for Atlas compatibility
}

// SetupCockroachDBWithInfo creates a CockroachDB testcontainer and returns connection info
// for use with external tools like Atlas CLI.
//
// Usage:
//
//	func TestAtlasMigrations(t *testing.T) {
//	    info, cleanup := testdb.SetupCockroachDBWithInfo(t)
//	    defer cleanup()
//
//	    // Use info.ConnStr with Atlas CLI
//	    cmd := exec.Command("atlas", "migrate", "apply",
//	        "--url", info.ConnStr,
//	        "--tx-mode", "none")
//	}
func SetupCockroachDBWithInfo(t *testing.T, opts ...PostgresOption) (CockroachDBConnectionInfo, func()) {
	t.Helper()

	cfg := &postgresConfig{
		logLevel: logger.Silent,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	crdbContainer, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_db"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("Failed to start CockroachDB container: %v", err)
	}

	host, err := crdbContainer.Host(ctx)
	if err != nil {
		t.Fatalf("Failed to get container host: %v", err)
	}

	port, err := crdbContainer.MappedPort(ctx, "26257/tcp")
	if err != nil {
		t.Fatalf("Failed to get container port: %v", err)
	}

	// Get connection config and extract proper connection string URL
	connConfig, err := crdbContainer.ConnectionConfig(ctx)
	if err != nil {
		t.Fatalf("Failed to get connection config: %v", err)
	}
	// Convert postgres:// to crdb:// for Atlas compatibility
	// Atlas CLI now requires the crdb:// scheme for CockroachDB connections
	connStr := connConfig.ConnString()
	connStr = toAtlasCrdbURL(connStr)

	info := CockroachDBConnectionInfo{
		Host:     host,
		Port:     port.Port(),
		Database: "test_db",
		User:     "root",
		ConnStr:  connStr,
	}

	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = crdbContainer.Terminate(cleanupCtx)
	}

	return info, cleanup
}

// toAtlasCrdbURL converts a PostgreSQL connection string to use the crdb:// scheme
// required by Atlas CLI for CockroachDB connections.
func toAtlasCrdbURL(connStr string) string {
	connStr = strings.Replace(connStr, "postgresql://", "crdb://", 1)
	connStr = strings.Replace(connStr, "postgres://", "crdb://", 1)
	return connStr
}
