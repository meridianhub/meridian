// Package testdb provides utilities for setting up test databases.
package testdb

import (
	"context"
	"fmt"
	"testing"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Option configures SetupTestDB.
type Option func(*setupConfig)

type setupConfig struct {
	logLevel    logger.LogLevel
	models      []interface{}
	tenantID    string
	auditTables bool
}

// WithModels specifies GORM models to AutoMigrate.
// When combined with WithTenant, tables are created in both the public schema
// (for cross-tenant fallback) and the tenant schema (for tenant-scoped queries).
func WithModels(models ...interface{}) Option {
	return func(cfg *setupConfig) {
		cfg.models = append(cfg.models, models...)
	}
}

// WithTenant creates a tenant schema, sets the search_path, and returns
// a context.Context with the tenant ID injected.
func WithTenant(tenantID string) Option {
	return func(cfg *setupConfig) {
		cfg.tenantID = tenantID
	}
}

// WithAuditTables creates the audit_outbox and audit_log tables required
// for audit logging hooks.
func WithAuditTables() Option {
	return func(cfg *setupConfig) {
		cfg.auditTables = true
	}
}

// WithSetupLogLevel sets the GORM logger level (default: logger.Silent).
func WithSetupLogLevel(level logger.LogLevel) Option {
	return func(cfg *setupConfig) {
		cfg.logLevel = level
	}
}

// SetupTestDB creates a PostgreSQL testcontainer and configures it according
// to the provided options. It returns a GORM database, a context (with tenant
// if WithTenant was specified), and a cleanup function.
//
// When WithTenant is specified, models are AutoMigrated in both the public
// schema and the tenant schema. This ensures that tests switching tenant
// contexts (e.g., org-scoped queries) can still find tables via the public
// schema fallback in search_path.
//
// Usage:
//
//	db, ctx, cleanup := testdb.SetupTestDB(t,
//	    testdb.WithModels(&MyEntity{}),
//	    testdb.WithTenant("test_tenant"),
//	)
//	defer cleanup()
//
// Without tenant:
//
//	db, ctx, cleanup := testdb.SetupTestDB(t,
//	    testdb.WithModels(&MyEntity{}),
//	)
//	defer cleanup()
func SetupTestDB(t *testing.T, opts ...Option) (*gorm.DB, context.Context, func()) {
	t.Helper()

	cfg := &setupConfig{
		logLevel: logger.Silent,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Start PostgreSQL container with GORM connection
	db, cleanup := SetupPostgres(t, nil, WithLogLevel(cfg.logLevel))

	// Pin to a single connection so session-level SET search_path persists
	// across all subsequent Exec/AutoMigrate calls.
	if cfg.tenantID != "" {
		sqlDB, err := db.DB()
		if err != nil {
			cleanup()
			t.Fatalf("testdb: failed to get underlying sql.DB: %v", err)
		}
		sqlDB.SetMaxOpenConns(1)
	}

	// AutoMigrate models in public schema first (provides fallback for cross-tenant queries)
	if len(cfg.models) > 0 {
		if err := db.AutoMigrate(cfg.models...); err != nil {
			cleanup()
			t.Fatalf("testdb: failed to auto-migrate models in public schema: %v", err)
		}
	}

	// Create audit tables in public schema if requested
	if cfg.auditTables {
		CreateAuditTables(t, db)
	}

	// Set up tenant schema if requested
	ctx := context.Background()
	if cfg.tenantID != "" {
		tid := tenant.TenantID(cfg.tenantID)
		schemaName := tid.SchemaName()

		err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
		if err != nil {
			cleanup()
			t.Fatalf("testdb: failed to create tenant schema %s: %v", schemaName, err)
		}

		// Use a transaction to pin the connection for SET search_path +
		// AutoMigrate so they are guaranteed to execute on the same session.
		// SET search_path is session-level, so it persists after COMMIT.
		err = db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error; err != nil {
				return fmt.Errorf("set search_path to %s: %w", schemaName, err)
			}

			// AutoMigrate models in tenant schema too
			if len(cfg.models) > 0 {
				if err := tx.AutoMigrate(cfg.models...); err != nil {
					return fmt.Errorf("auto-migrate models in tenant schema: %w", err)
				}
			}

			// Create audit tables in tenant schema if requested
			if cfg.auditTables {
				CreateAuditTables(t, tx)
			}

			return nil
		})
		if err != nil {
			cleanup()
			t.Fatalf("testdb: failed to set up tenant schema %s: %v", schemaName, err)
		}

		ctx = tenant.WithTenant(ctx, tid)
	}

	return db, ctx, cleanup
}
