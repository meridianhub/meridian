package infra

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Tenant isolation errors.
var (
	// ErrMissingTenant is returned when tenant context is required but not provided.
	ErrMissingTenant = errors.New("tenant context is required for this operation")
	// ErrTenantIsolationFailed is returned when schema scoping fails.
	ErrTenantIsolationFailed = errors.New("failed to apply tenant isolation")
)

// TenantHelper provides utilities for multi-tenant schema isolation.
// It wraps the tenant context operations and ensures proper PostgreSQL
// search_path configuration for multi-tenant database operations.
//
// This helper implements the schema-per-tenant pattern where each tenant's
// data lives in a separate PostgreSQL schema (e.g., "org_acme_bank").
type TenantHelper struct {
	pool *pgxpool.Pool
}

// NewTenantHelper creates a new tenant helper with the given connection pool.
// Returns ErrNilPool if the pool is nil.
func NewTenantHelper(pool *pgxpool.Pool) (*TenantHelper, error) {
	if pool == nil {
		return nil, ErrNilPool
	}
	return &TenantHelper{pool: pool}, nil
}

// WithTenantContext returns a context with the tenant ID attached.
// This is a convenience wrapper around tenant.WithTenant.
func (h *TenantHelper) WithTenantContext(ctx context.Context, tenantID string) (context.Context, error) {
	tid, err := tenant.NewTenantID(tenantID)
	if err != nil {
		return ctx, fmt.Errorf("invalid tenant ID %q: %w", tenantID, err)
	}
	return tenant.WithTenant(ctx, tid), nil
}

// GetTenantFromContext extracts the tenant ID from the context.
// Returns ErrMissingTenant if the tenant context is not set.
func (h *TenantHelper) GetTenantFromContext(ctx context.Context) (tenant.TenantID, error) {
	tid, ok := tenant.FromContext(ctx)
	if !ok {
		return "", ErrMissingTenant
	}
	return tid, nil
}

// BeginTenantScopedTx begins a transaction with tenant schema isolation.
// It executes SET LOCAL search_path to scope all subsequent operations
// to the tenant's schema.
//
// The caller is responsible for committing or rolling back the transaction.
//
// Usage:
//
//	ctx = helper.WithTenantContext(ctx, "acme_bank")
//	tx, err := helper.BeginTenantScopedTx(ctx)
//	if err != nil {
//	    return err
//	}
//	defer tx.Rollback(ctx)
//
//	// All operations on tx are scoped to org_acme_bank schema
//	_, err = tx.Exec(ctx, "INSERT INTO position ...")
//
//	return tx.Commit(ctx)
func (h *TenantHelper) BeginTenantScopedTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := h.setSearchPath(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}

	return tx, nil
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
// Uses SET LOCAL to ensure the setting only applies within this transaction.
//
// In multi-tenant mode, it sets the search_path to the tenant's schema.
// In single-tenant mode (no tenant context), it does nothing.
func (h *TenantHelper) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		// Single-tenant mode - no search_path override needed
		return nil
	}

	schemaName := pgx.Identifier{tenantID.SchemaName()}.Sanitize()
	query := fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)

	_, err := tx.Exec(ctx, query)
	if err != nil {
		return errors.Join(ErrTenantIsolationFailed,
			fmt.Errorf("failed to set search_path to %s: %w", schemaName, err))
	}

	return nil
}

// ExecuteInTenantScope executes a function within a tenant-scoped transaction.
// This is a convenience method that handles transaction management.
//
// The function receives a transaction that is already scoped to the tenant's schema.
// If the function returns nil, the transaction is committed.
// If the function returns an error, the transaction is rolled back.
//
// Usage:
//
//	err := helper.ExecuteInTenantScope(ctx, func(tx pgx.Tx) error {
//	    _, err := tx.Exec(ctx, "INSERT INTO position ...")
//	    return err
//	})
func (h *TenantHelper) ExecuteInTenantScope(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := h.BeginTenantScopedTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// SchemaNameForTenant returns the PostgreSQL schema name for the given tenant ID.
// This is a convenience method that validates the tenant ID and returns the schema name.
func (h *TenantHelper) SchemaNameForTenant(tenantID string) (string, error) {
	tid, err := tenant.NewTenantID(tenantID)
	if err != nil {
		return "", fmt.Errorf("invalid tenant ID %q: %w", tenantID, err)
	}
	return tid.SchemaName(), nil
}

// ValidateTenant checks if a tenant ID is valid according to naming conventions.
// Returns nil if valid, or an error describing why it's invalid.
func (h *TenantHelper) ValidateTenant(tenantID string) error {
	_, err := tenant.NewTenantID(tenantID)
	return err
}

// TenantInfo contains information about a tenant's database configuration.
type TenantInfo struct {
	// TenantID is the validated tenant identifier.
	TenantID string

	// SchemaName is the PostgreSQL schema name (e.g., "org_acme_bank").
	SchemaName string
}

// GetTenantInfo returns tenant information for the given tenant ID.
// This validates the tenant ID and computes the schema name.
func (h *TenantHelper) GetTenantInfo(tenantID string) (*TenantInfo, error) {
	tid, err := tenant.NewTenantID(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant ID %q: %w", tenantID, err)
	}

	return &TenantInfo{
		TenantID:   string(tid),
		SchemaName: tid.SchemaName(),
	}, nil
}
