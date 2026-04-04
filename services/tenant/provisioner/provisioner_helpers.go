package provisioner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// timeNow is a package-level variable for time.Now to allow testing.
// In production code, this is always time.Now.
var timeNow = time.Now

// openServiceConnections opens database connections for all services.
// On failure, it closes any already-opened connections.
func openServiceConnections(services []ServiceConfig) (map[string]*gorm.DB, error) {
	serviceDbs := make(map[string]*gorm.DB)

	for _, svc := range services {
		db, err := gorm.Open(postgres.Open(svc.DatabaseURL), &gorm.Config{
			SkipDefaultTransaction: true,
			PrepareStmt:            true,
		})
		if err != nil {
			closeServiceConnections(serviceDbs)
			return nil, fmt.Errorf("failed to connect to %s database: %w", svc.Name, err)
		}

		sqlDB, err := db.DB()
		if err != nil {
			// Note: If db.DB() fails, we cannot properly close this GORM connection
			// since that requires the underlying *sql.DB. Log the potential leak.
			slog.Default().Error("failed to get underlying DB (connection may leak)",
				"service", svc.Name, "error", err)
			closeServiceConnections(serviceDbs)
			return nil, fmt.Errorf("get underlying DB for %s: %w", svc.Name, err)
		}

		// Configure connection pool - provisioner doesn't need many connections
		sqlDB.SetMaxOpenConns(5)
		sqlDB.SetMaxIdleConns(2)
		sqlDB.SetConnMaxLifetime(time.Hour)

		serviceDbs[svc.Name] = db
	}

	return serviceDbs, nil
}

// closeServiceConnections closes all connections in the map.
// Used for cleanup during initialization failures.
func closeServiceConnections(serviceDbs map[string]*gorm.DB) {
	for name, db := range serviceDbs {
		if sqlDB, err := db.DB(); err == nil {
			if closeErr := sqlDB.Close(); closeErr != nil {
				slog.Default().Warn("failed to close database during cleanup",
					"service", name, "error", closeErr)
			}
		}
	}
}

// maskDatabaseURL redacts password from connection string for logging.
// Uses url.Parse for robust handling of various URL formats.
func maskDatabaseURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}

// quoteIdentifier safely quotes a PostgreSQL identifier to prevent SQL injection.
// Uses double quotes and escapes any embedded double quotes.
func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// createSchemaInDB creates the org_{tenant_id} schema in the specified database.
//
// IDEMPOTENCY: Uses CREATE SCHEMA IF NOT EXISTS, so calling this multiple times
// for the same schema is safe - PostgreSQL silently ignores the request if the
// schema already exists.
func (p *PostgresProvisioner) createSchemaInDB(ctx context.Context, db *gorm.DB, schemaName string) error {
	query := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quoteIdentifier(schemaName))
	return db.WithContext(bypassCtx(ctx)).Exec(query).Error
}

// dropSchemaInAllDBs drops the org_{tenant_id} schema from all service databases.
// This function attempts to drop schemas from ALL databases, collecting errors
// along the way rather than failing on the first error.
func (p *PostgresProvisioner) dropSchemaInAllDBs(ctx context.Context, schemaName string) error {
	var errs []error
	for _, svc := range p.config.Services {
		serviceDB, ok := p.serviceDbs[svc.Name]
		if !ok || serviceDB == nil {
			errs = append(errs, fmt.Errorf("%w: %s", ErrServiceDatabaseNotFound, svc.Name))
			continue
		}
		query := fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", quoteIdentifier(schemaName))
		if err := serviceDB.WithContext(bypassCtx(ctx)).Exec(query).Error; err != nil {
			errs = append(errs, fmt.Errorf("drop schema in %s: %w", svc.Name, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %v", ErrDeprovisioningFailed, errors.Join(errs...)) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
	}
	return nil
}

// verifySchemaProvisioned checks that expected tables exist in the tenant schema
// after migrations have been applied. This closes the partial-provisioning gap
// where the schema namespace exists but migrations failed halfway through.
//
// For each service:
//   - If SentinelTable is set, verifies that specific table exists
//   - If SentinelTable is empty, verification is skipped (service has no required tables)
//
// Returns nil if verification passes, or an error listing which services failed.
func (p *PostgresProvisioner) verifySchemaProvisioned(ctx context.Context, schemaName string, logger *slog.Logger) error {
	var failedServices []string

	for _, svc := range p.config.Services {
		serviceDB, ok := p.serviceDbs[svc.Name]
		if !ok {
			continue // Already caught by provisionSingleService
		}

		if svc.SentinelTable != "" {
			// Check for specific sentinel table
			var exists bool
			err := serviceDB.WithContext(bypassCtx(ctx)).Raw(
				`SELECT EXISTS(
					SELECT 1 FROM information_schema.tables
					WHERE table_schema = ? AND table_name = ?
				)`, schemaName, svc.SentinelTable,
			).Scan(&exists).Error
			if err != nil {
				logger.Error("failed to verify sentinel table",
					"service", svc.Name,
					"sentinel_table", svc.SentinelTable,
					"error", err)
				failedServices = append(failedServices, fmt.Sprintf("%s (query error: %v)", svc.Name, err))
				continue
			}
			if !exists {
				logger.Error("sentinel table missing after migration",
					"service", svc.Name,
					"schema", schemaName,
					"sentinel_table", svc.SentinelTable)
				failedServices = append(failedServices, fmt.Sprintf("%s (missing table: %s)", svc.Name, svc.SentinelTable))
			}
		} else {
			// No sentinel table configured - service has no required tables
			// (e.g., internal-account, reconciliation). Skip verification.
			logger.Debug("no sentinel table configured, skipping verification", "service", svc.Name)
		}
	}

	if len(failedServices) > 0 {
		return fmt.Errorf("%w: %s", ErrSchemaVerificationFailed, strings.Join(failedServices, "; "))
	}
	return nil
}

// isAlreadyExistsError checks if the error indicates an object already exists.
//
// IDEMPOTENCY: This is a key idempotency mechanism for migrations. When a migration
// attempts to create a table, index, or schema that already exists, we treat it as
// success rather than failure. This handles partial provisioning retries gracefully.
//
// Recognized PostgreSQL error codes:
//   - 42P07: duplicate_table (table already exists)
//   - 42P06: duplicate_schema (schema already exists)
//   - 42710: duplicate_object (index or other object already exists)
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}

	// Check for PostgreSQL-specific error codes
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 42P07: duplicate_table
		// 42P06: duplicate_schema
		// 42710: duplicate_object (for indexes)
		switch pgErr.Code {
		case "42P07", "42P06", "42710":
			return true
		}
	}

	// Fallback to string matching
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "already exists") ||
		strings.Contains(errStr, "duplicate")
}
