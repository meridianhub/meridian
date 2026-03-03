package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/gorm"
)

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

	schema := tenantID.SchemaName()

	// Warn if called outside a transaction — SET LOCAL has no effect without one.
	// PostgreSQL itself emits a WARNING in this case. We mirror that behavior in application logs.
	if tx.Statement != nil && tx.Statement.ConnPool != nil {
		if _, isTx := tx.Statement.ConnPool.(gorm.TxCommitter); !isTx {
			logger.WarnContext(ctx, "tenant scope: SET LOCAL called outside transaction, scope may not be enforced",
				"tenant_hash", hashTenantID(tenantID))
		}
	}

	// Quote the schema name to prevent SQL injection
	// pq.QuoteIdentifier handles special characters safely
	quotedSchema := pq.QuoteIdentifier(schema)

	// SET LOCAL is transaction-scoped - automatically reverts on commit/rollback.
	// fmt.Sprintf is safe here because quotedSchema is already quoted by pq.QuoteIdentifier above,
	// which properly escapes any special characters including quotes and null bytes.
	// Bypass the TenantGuard for this SET LOCAL exec — this IS the operation that
	// establishes tenant scope, so it must execute before the guard flag is set.
	bypassCtx := WithTenantGuardBypass(ctx)
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", quotedSchema)
	if err := tx.WithContext(bypassCtx).Exec(query).Error; err != nil {
		logger.ErrorContext(ctx, "tenant scope: failed to set search_path",
			"tenant_hash", hashTenantID(tenantID),
			"error", err)
		return nil, fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	// Audit log: emitted on every successful schema access for forensic traceability.
	// Uses hashed tenant ID for privacy-preserving correlation (consistent with error-path logging).
	// Service-level attributes (e.g., "service") should be pre-set on the logger at startup.
	logger.InfoContext(ctx, "tenant.schema.access",
		"tenant_hash", hashTenantID(tenantID),
		"schema", schema,
	)

	// Mark context so TenantGuard knows scope was applied
	scopedCtx := withTenantScopeSet(ctx)
	return tx.WithContext(scopedCtx), nil
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
