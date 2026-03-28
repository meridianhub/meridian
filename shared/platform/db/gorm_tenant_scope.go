package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/gorm"
)

// ErrTenantScopeRequiresTransaction is returned when WithGormTenantScope is called
// outside an active database transaction. SET LOCAL has no effect without a transaction,
// so allowing this would silently skip tenant isolation.
var ErrTenantScopeRequiresTransaction = errors.New("tenant scope requires an active transaction: SET LOCAL has no effect outside a transaction")

// ErrTenantSchemaNotProvisioned is returned when SET LOCAL search_path targets a schema
// that does not exist in the database. PostgreSQL silently accepts non-existent schemas
// in search_path, which would cause queries to fall through to the public schema and
// expose cross-tenant data.
var ErrTenantSchemaNotProvisioned = errors.New("tenant schema not provisioned: schema does not exist in database")

// hashTenantID creates a short, privacy-preserving hash of the tenant ID for logging.
// This allows correlation in logs without exposing the actual tenant ID.
func hashTenantID(tenantID tenant.TenantID) string {
	h := sha256.Sum256([]byte(tenantID))
	return hex.EncodeToString(h[:8]) // First 8 bytes = 16 hex chars
}

// WithGormTenantScope sets the PostgreSQL search_path for multi-tenant isolation using GORM.
// This must be called at the start of a transaction to ensure the search_path is transaction-scoped.
//
// The function:
//  1. Extracts the tenant ID from context using tenant.FromContext
//  2. Returns ErrMissingTenantContext if tenant is missing (fail-fast)
//  3. Generates schema name via tenantID.SchemaName() (returns "org_{id}")
//  4. Executes SET LOCAL search_path TO <schema>, public
//  5. Returns the same DB for chaining
//
// SET LOCAL ensures the search_path automatically reverts when the transaction
// commits or rolls back - no manual cleanup needed.
//
// The public schema is included in search_path to allow read access to shared
// reference data.
//
// Example usage with GORM transactions:
//
//	err := db.Transaction(func(tx *gorm.DB) error {
//	    tx, err := db.WithGormTenantScope(ctx, tx)
//	    if err != nil {
//	        return err
//	    }
//	    // All queries now target the tenant.s schema
//	    return tx.Create(&entity).Error
//	})
func WithGormTenantScope(ctx context.Context, tx *gorm.DB) (*gorm.DB, error) {
	return WithGormTenantScopeAndLogger(ctx, tx, slog.Default())
}

// WithGormTenantScopeAndLogger is like WithGormTenantScope but accepts an explicit logger.
// This enables structured audit logging with service-level attributes pre-configured
// on the logger (e.g., service name).
//
// On success, emits an INFO-level "tenant.schema.access" audit log with tenant and schema fields.
func WithGormTenantScopeAndLogger(ctx context.Context, tx *gorm.DB, logger *slog.Logger) (*gorm.DB, error) {
	if logger == nil {
		logger = slog.Default()
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		logger.DebugContext(ctx, "tenant scope: missing tenant context, returning error")
		return nil, tenant.ErrMissingTenantContext
	}

	if err := validateTenantTransaction(tx); err != nil {
		return nil, err
	}

	schema := tenantID.SchemaName()
	quotedSchema := pq.QuoteIdentifier(schema)

	bypassCtx := WithTenantGuardBypass(ctx)
	if err := setLocalSearchPath(tx, bypassCtx, quotedSchema, tenantID, logger); err != nil {
		return nil, err
	}

	if err := verifySchemaExists(tx, bypassCtx, schema, tenantID, logger); err != nil {
		return nil, err
	}

	logger.InfoContext(ctx, "tenant.schema.access",
		"tenant_hash", hashTenantID(tenantID),
		"schema", schema,
	)

	scopedCtx := withTenantScopeSet(ctx)
	return tx.WithContext(scopedCtx), nil
}

// validateTenantTransaction checks that the GORM DB is an active transaction.
// SET LOCAL has no effect outside a transaction, so this prevents silent misuse.
func validateTenantTransaction(tx *gorm.DB) error {
	if tx.Statement == nil || tx.Statement.ConnPool == nil {
		return ErrTenantScopeRequiresTransaction
	}
	if _, isTx := tx.Statement.ConnPool.(gorm.TxCommitter); !isTx {
		return ErrTenantScopeRequiresTransaction
	}
	return nil
}

// setLocalSearchPath executes SET LOCAL search_path for tenant isolation.
func setLocalSearchPath(tx *gorm.DB, ctx context.Context, quotedSchema string, tenantID tenant.TenantID, logger *slog.Logger) error {
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", quotedSchema)
	if err := tx.WithContext(ctx).Exec(query).Error; err != nil {
		logger.ErrorContext(ctx, "tenant scope: failed to set search_path",
			"tenant_hash", hashTenantID(tenantID),
			"error", err)
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}
	return nil
}

// verifySchemaExists checks that the tenant schema exists in pg_namespace.
// PostgreSQL silently accepts non-existent schemas in search_path, which would
// cause queries to fall through to public schema and expose cross-tenant data.
func verifySchemaExists(tx *gorm.DB, ctx context.Context, schema string, tenantID tenant.TenantID, logger *slog.Logger) error {
	var schemaExists bool
	if err := tx.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname = ?)", schema).Scan(&schemaExists).Error; err != nil {
		logger.ErrorContext(ctx, "tenant scope: failed to verify schema existence",
			"tenant_hash", hashTenantID(tenantID),
			"schema", schema,
			"error", err)
		return fmt.Errorf("failed to verify tenant schema existence: %w", err)
	}
	if !schemaExists {
		logger.ErrorContext(ctx, "tenant scope: schema not provisioned",
			"tenant_hash", hashTenantID(tenantID),
			"schema", schema)
		return fmt.Errorf("%w: %s", ErrTenantSchemaNotProvisioned, schema)
	}
	return nil
}

// MustWithGormTenantScope is like WithGormTenantScope but panics on error.
// Use only when tenant context is guaranteed to be present (e.g., after
// middleware validation).
func MustWithGormTenantScope(ctx context.Context, tx *gorm.DB) *gorm.DB {
	result, err := WithGormTenantScope(ctx, tx)
	if err != nil {
		panic(fmt.Sprintf("gorm tenant scope failed: %v", err))
	}
	return result
}

// WithGormTenantTransaction provides a helper for running GORM operations
// within a transaction with tenant scope automatically set.
//
// This is the recommended way to perform multi-tenant database operations with GORM.
//
// Example:
//
//	err := db.WithGormTenantTransaction(ctx, gormDB, func(tx *gorm.DB) error {
//	    return tx.Create(&entity).Error
//	})
func WithGormTenantTransaction(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx, err := WithGormTenantScope(ctx, tx)
		if err != nil {
			return err
		}
		return fn(tx)
	})
}
