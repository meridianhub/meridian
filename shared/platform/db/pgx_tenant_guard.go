package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ErrPgxTenantContextRequired is returned when a pgx query is executed without
// tenant context. This is the pgx equivalent of ErrTenantScopeRequired for GORM.
var ErrPgxTenantContextRequired = errors.New("pgx tenant guard: tenant context required — use tenant.WithTenant(ctx, id) or WithPgxTenantBypass(ctx)")

// pgxTenantBypassKey marks a context as bypassing pgx tenant guard checks.
// Used for infrastructure operations: migrations, health checks, schema provisioning.
type pgxTenantBypassKey struct{}

// WithPgxTenantBypass returns a context that bypasses pgx tenant guard checks.
// Use this for operations that legitimately don't need tenant context:
// migrations, health checks, tenant provisioning, and platform-level queries.
func WithPgxTenantBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, pgxTenantBypassKey{}, true)
}

// hasPgxTenantBypass checks whether pgx tenant guard bypass is set.
func hasPgxTenantBypass(ctx context.Context) bool {
	v, ok := ctx.Value(pgxTenantBypassKey{}).(bool)
	return ok && v
}

// RequirePgxTenantContext validates that the context carries either a tenant ID
// or a bypass marker. Returns the tenant ID if present, or an error if neither
// tenant context nor bypass is set.
//
// This is the core guard function used by GuardedPgxPool to enforce tenant
// isolation on all pgx database operations.
func RequirePgxTenantContext(ctx context.Context) (tenant.TenantID, error) {
	if hasPgxTenantBypass(ctx) {
		return "", nil
	}

	tid, ok := tenant.FromContext(ctx)
	if !ok || tid.IsEmpty() {
		return "", fmt.Errorf("%w", ErrPgxTenantContextRequired)
	}

	return tid, nil
}
