package db

import (
	"context"
	"fmt"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/organization"
)

// WithOrganizationScope sets the PostgreSQL search_path to the organization's schema.
// This must be called INSIDE a transaction (within a WithTransaction callback) to ensure
// the search_path is transaction-scoped via SET LOCAL.
//
// The function:
//  1. Extracts the organization ID from context using organization.FromContext
//  2. Returns ErrMissingOrganizationContext if organization is missing (fail-fast)
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
//	    if _, err := db.WithOrganizationScope(ctx, tx); err != nil {
//	        return err
//	    }
//	    // All queries now target the organization's schema
//	    return repository.Save(tx, entity)
//	})
func WithOrganizationScope(ctx context.Context, db DB) (DB, error) {
	orgID, ok := organization.FromContext(ctx)
	if !ok {
		return nil, organization.ErrMissingOrganizationContext
	}

	// Quote the schema name to prevent SQL injection
	// pq.QuoteIdentifier handles special characters safely
	schemaName := pq.QuoteIdentifier(orgID.SchemaName())

	// SET LOCAL is transaction-scoped - automatically reverts on commit/rollback
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
	if _, err := db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("failed to set organization schema scope: %w", err)
	}

	return db, nil
}

// MustWithOrganizationScope is like WithOrganizationScope but panics on error.
// Use only when organization context is guaranteed to be present (e.g., after
// middleware validation).
func MustWithOrganizationScope(ctx context.Context, db DB) DB {
	result, err := WithOrganizationScope(ctx, db)
	if err != nil {
		panic(fmt.Sprintf("organization scope failed: %v", err))
	}
	return result
}
