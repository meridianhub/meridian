package db

import (
	"context"
	"fmt"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// WithTenantScope sets the PostgreSQL search_path to the tenant.s schema.
// This must be called INSIDE a transaction (within a WithTransaction callback) to ensure
// the search_path is transaction-scoped via SET LOCAL.
//
// The function:
//  1. Extracts the tenant ID from context using tenant.FromContext
//  2. Returns ErrMissingTenantContext if tenant is missing (fail-fast)
//  3. Generates schema name via orgID.SchemaName() (returns "org_{id}")
//  4. Executes SET LOCAL search_path TO <schema>, public
//  5. Returns the same DB for chaining
//
// SET LOCAL ensures the search_path automatically reverts when the transaction
// commits or rolls back - no manual cleanup needed.
//
// The public schema is included in search_path to allow read access to shared
// reference data.
//
// Example usage:
//
//	err := db.WithTransaction(ctx, pool, func(tx db.DB) error {
//	    if _, err := db.WithTenantScope(ctx, tx); err != nil {
//	        return err
//	    }
//	    // All queries now target the tenant.s schema
//	    return repository.Save(tx, entity)
//	})
func WithTenantScope(ctx context.Context, db DB) (DB, error) {
	orgID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, tenant.ErrMissingTenantContext
	}

	// Quote the schema name to prevent SQL injection
	// pq.QuoteIdentifier handles special characters safely
	schemaName := pq.QuoteIdentifier(orgID.SchemaName())

	// SET LOCAL is transaction-scoped - automatically reverts on commit/rollback
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
	if _, err := db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	// Verify the tenant schema actually exists - PostgreSQL allows SET LOCAL to non-existent schemas,
	// which would silently fall through to public schema and leak cross-tenant data.
	rawSchema := orgID.SchemaName()
	var schemaExists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname = $1)", rawSchema).Scan(&schemaExists); err != nil {
		return nil, fmt.Errorf("failed to verify tenant schema existence: %w", err)
	}
	if !schemaExists {
		return nil, fmt.Errorf("%w: %s", ErrTenantSchemaNotProvisioned, rawSchema)
	}

	return db, nil
}

// MustWithTenantScope is like WithTenantScope but panics on error.
// Use only when tenant context is guaranteed to be present (e.g., after
// middleware validation).
func MustWithTenantScope(ctx context.Context, db DB) DB {
	result, err := WithTenantScope(ctx, db)
	if err != nil {
		panic(fmt.Sprintf("tenant scope failed: %v", err))
	}
	return result
}
