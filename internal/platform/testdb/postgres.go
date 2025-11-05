// Package testdb provides utilities for setting up test databases.
// It offers PostgreSQL testcontainers for integration testing with production parity.
package testdb

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// PostgresOption configures the PostgreSQL test setup.
type PostgresOption func(*postgresConfig)

type postgresConfig struct {
	logLevel logger.LogLevel
}

// WithLogLevel sets the GORM logger level (default: logger.Silent).
// Use logger.Info for debugging test database operations.
func WithLogLevel(level logger.LogLevel) PostgresOption {
	return func(cfg *postgresConfig) {
		cfg.logLevel = level
	}
}

// SetupPostgres creates a PostgreSQL testcontainer for integration testing.
// It returns a configured GORM database connection and a cleanup function.
//
// Usage:
//
//	func TestMyRepository(t *testing.T) {
//	    db, cleanup := testdb.SetupPostgres(t, &MyEntity{})
//	    defer cleanup()
//
//	    // Your test code here
//	}
//
// For debugging, enable verbose logging:
//
//	db, cleanup := testdb.SetupPostgres(t, &MyEntity{}, WithLogLevel(logger.Info))
//
// The database will be automatically migrated with the provided models.
// The cleanup function should be deferred to ensure proper resource cleanup.
func SetupPostgres(t *testing.T, models []interface{}, opts ...PostgresOption) (*gorm.DB, func()) {
	t.Helper()

	// Apply configuration options
	cfg := &postgresConfig{
		logLevel: logger.Silent, // Default to silent for clean test output
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Create context with timeout for container operations
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

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
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	// Create GORM connection with configured logger level
	db, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(cfg.logLevel),
	})
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	// Run migrations for provided models
	if len(models) > 0 {
		if err := db.AutoMigrate(models...); err != nil {
			t.Fatalf("Failed to migrate database: %v", err)
		}
	}

	cleanup := func() {
		// Use background context for cleanup to avoid using cancelled context
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = pgContainer.Terminate(cleanupCtx)
	}

	return db, cleanup
}
