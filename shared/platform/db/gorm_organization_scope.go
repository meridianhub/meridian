package db

import (
	"context"
	"fmt"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/organization"
	"gorm.io/gorm"
)

// WithGormOrganizationScope sets the PostgreSQL search_path for multi-tenant isolation using GORM.
// This must be called at the start of a transaction to ensure the search_path is transaction-scoped.
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
// Example usage with GORM transactions:
//
//	err := db.Transaction(func(tx *gorm.DB) error {
//	    tx, err := db.WithGormOrganizationScope(ctx, tx)
//	    if err != nil {
//	        return err
//	    }
//	    // All queries now target the organization's schema
//	    return tx.Create(&entity).Error
//	})
func WithGormOrganizationScope(ctx context.Context, tx *gorm.DB) (*gorm.DB, error) {
	orgID, ok := organization.FromContext(ctx)
	if !ok {
		return nil, organization.ErrMissingOrganizationContext
	}

	// Quote the schema name to prevent SQL injection
	// pq.QuoteIdentifier handles special characters safely
	schemaName := pq.QuoteIdentifier(orgID.SchemaName())

	// SET LOCAL is transaction-scoped - automatically reverts on commit/rollback
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
	if err := tx.Exec(query).Error; err != nil {
		return nil, fmt.Errorf("failed to set organization schema scope: %w", err)
	}

	return tx, nil
}

// MustWithGormOrganizationScope is like WithGormOrganizationScope but panics on error.
// Use only when organization context is guaranteed to be present (e.g., after
// middleware validation).
func MustWithGormOrganizationScope(ctx context.Context, tx *gorm.DB) *gorm.DB {
	result, err := WithGormOrganizationScope(ctx, tx)
	if err != nil {
		panic(fmt.Sprintf("gorm organization scope failed: %v", err))
	}
	return result
}

// WithGormOrganizationTransaction provides a helper for running GORM operations
// within a transaction with organization scope automatically set.
//
// This is the recommended way to perform multi-tenant database operations with GORM.
//
// Example:
//
//	err := db.WithGormOrganizationTransaction(ctx, gormDB, func(tx *gorm.DB) error {
//	    return tx.Create(&entity).Error
//	})
func WithGormOrganizationTransaction(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx, err := WithGormOrganizationScope(ctx, tx)
		if err != nil {
			return err
		}
		return fn(tx)
	})
}
