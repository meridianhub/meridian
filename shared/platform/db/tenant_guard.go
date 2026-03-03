package db

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ErrTenantScopeRequired is returned when a query is executed without
// tenant scope being set. This is a safety net that catches cases where
// WithGormTenantScope was not called before executing queries.
var ErrTenantScopeRequired = errors.New("tenant scope required: call WithGormTenantScope before executing queries")

// tenantGuardKey marks the context as having tenant scope set.
type tenantGuardKey struct{}

// tenantBypassKey marks the context as bypassing tenant guard checks.
// Used for migrations, health checks, and platform-level operations.
type tenantBypassKey struct{}

// withTenantScopeSet marks the context as having had tenant scope applied.
// Called internally by WithGormTenantScope.
func withTenantScopeSet(ctx context.Context) context.Context {
	return context.WithValue(ctx, tenantGuardKey{}, true)
}

// hasTenantScopeSet checks whether tenant scope was applied in this context.
func hasTenantScopeSet(ctx context.Context) bool {
	v, ok := ctx.Value(tenantGuardKey{}).(bool)
	return ok && v
}

// WithTenantGuardBypass returns a context that bypasses tenant guard checks.
// Use this for operations that legitimately don't need tenant scope:
// migrations, health checks, tenant provisioning, and platform-level queries.
func WithTenantGuardBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, tenantBypassKey{}, true)
}

// hasTenantGuardBypass checks whether tenant guard bypass is set.
func hasTenantGuardBypass(ctx context.Context) bool {
	v, ok := ctx.Value(tenantBypassKey{}).(bool)
	return ok && v
}

// TenantGuard is a GORM plugin that rejects queries executed without
// tenant scope. It registers Before callbacks on all CRUD operations
// to verify that WithGormTenantScope was called before any query reaches
// the database.
//
// This acts as a safety net: even if a developer forgets to call
// WithGormTenantScope, the query will fail with a clear error rather
// than silently querying the wrong schema.
type TenantGuard struct{}

// NewTenantGuard creates a new TenantGuard plugin.
func NewTenantGuard() *TenantGuard {
	return &TenantGuard{}
}

// Name returns the plugin name for GORM's plugin registry.
func (g *TenantGuard) Name() string {
	return "meridian:tenant_guard"
}

// Initialize registers Before callbacks on all CRUD and raw operations.
func (g *TenantGuard) Initialize(db *gorm.DB) error {
	check := func(tx *gorm.DB) {
		ctx := tx.Statement.Context
		if ctx == nil {
			_ = tx.AddError(ErrTenantScopeRequired)
			return
		}
		if hasTenantGuardBypass(ctx) {
			return
		}
		if hasTenantScopeSet(ctx) {
			return
		}
		_ = tx.AddError(ErrTenantScopeRequired)
	}

	if err := db.Callback().Query().Before("gorm:query").Register("meridian:tenant_guard:query", check); err != nil {
		return err
	}
	if err := db.Callback().Create().Before("gorm:create").Register("meridian:tenant_guard:create", check); err != nil {
		return err
	}
	if err := db.Callback().Update().Before("gorm:update").Register("meridian:tenant_guard:update", check); err != nil {
		return err
	}
	if err := db.Callback().Delete().Before("gorm:delete").Register("meridian:tenant_guard:delete", check); err != nil {
		return err
	}
	if err := db.Callback().Row().Before("gorm:row").Register("meridian:tenant_guard:row", check); err != nil {
		return err
	}
	return db.Callback().Raw().Before("gorm:raw").Register("meridian:tenant_guard:raw", check)
}
