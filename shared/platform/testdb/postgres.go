// Package testdb provides utilities for setting up test databases.
// It offers PostgreSQL testcontainers for integration testing with production parity.
package testdb

import (
	"context"
	"fmt"
	"strings"
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

// CreateTenantSchema creates the database schema for a tenant ID in a test database.
// This must be called before any tenant-scoped database operation that targets this tenant,
// since WithGormTenantScope validates schema existence.
//
// Example:
//
//	db, cleanup := testdb.SetupCockroachDB(t, models)
//	defer cleanup()
//	testdb.CreateTenantSchema(t, db, tenant.MustNewTenantID("test_tenant"))
func CreateTenantSchema(t *testing.T, db *gorm.DB, tenantID interface{ SchemaName() string }) {
	t.Helper()
	schema := tenantID.SchemaName()
	if err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schema)).Error; err != nil {
		t.Fatalf("Failed to create tenant schema %s: %v", schema, err)
	}
}

// extractSchemasFromModels extracts unique schema names from models with TableName() methods.
// It parses "schema.table" format and returns a set of schema names.
func extractSchemasFromModels(models []interface{}) map[string]bool {
	schemas := make(map[string]bool)
	for _, model := range models {
		// Check if model has TableName method
		if tabler, ok := model.(interface{ TableName() string }); ok {
			tableName := tabler.TableName()
			// Extract schema from "schema.table" format using strings.Index
			if idx := strings.Index(tableName, "."); idx > 0 {
				schemaName := tableName[:idx]
				schemas[schemaName] = true
			}
		}
	}
	return schemas
}

// createSchemas creates database schemas if they don't exist.
// Schema names are validated against a strict pattern to prevent SQL injection.
func createSchemas(t *testing.T, db *gorm.DB, schemas map[string]bool) {
	t.Helper()
	for schema := range schemas {
		// Check for empty schema name
		if len(schema) == 0 {
			t.Fatalf("Invalid schema name: empty string")
		}

		// Validate first character: must be letter or underscore
		first := schema[0]
		if (first < 'a' || first > 'z') && (first < 'A' || first > 'Z') && first != '_' {
			t.Fatalf("Invalid schema name %q: must start with a letter or underscore", schema)
		}

		// Validate schema name: only alphanumeric and underscore allowed
		// This prevents SQL injection even though schema names come from TableName()
		for i := 0; i < len(schema); i++ {
			c := schema[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
				t.Fatalf("Invalid schema name %q: must contain only letters, digits, and underscores", schema)
			}
		}

		// Use parameterized query with proper identifier quoting
		// PostgreSQL uses double quotes for identifiers, %q provides this
		sql := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schema)
		if err := db.Exec(sql).Error; err != nil {
			t.Fatalf("Failed to create schema %s: %v", schema, err)
		}
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

	// Create schemas and run migrations for provided models
	if len(models) > 0 {
		// Extract and create schemas from model table names
		schemas := extractSchemasFromModels(models)
		createSchemas(t, db, schemas)

		// Run migrations for provided models
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
