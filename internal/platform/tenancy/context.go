package tenancy

import "context"

// contextKey is a private type to prevent collisions with context keys.
type contextKey string

// tenantContextKey is the context key for tenant ID.
const tenantContextKey contextKey = "meridian_tenant_id"

// WithTenant returns a new context with the tenant ID attached.
func WithTenant(ctx context.Context, tenantID TenantID) context.Context {
	return context.WithValue(ctx, tenantContextKey, tenantID)
}

// FromContext extracts the tenant ID from the context.
// Returns the tenant ID and true if present, or an empty tenant ID and false if not.
func FromContext(ctx context.Context) (TenantID, bool) {
	tenant, ok := ctx.Value(tenantContextKey).(TenantID)
	return tenant, ok
}

// MustFromContext extracts the tenant ID from the context.
// Panics if the tenant context is missing - this is a fail-fast strategy for
// catching programming errors where tenant context propagation was not set up correctly.
func MustFromContext(ctx context.Context) TenantID {
	tenant, ok := FromContext(ctx)
	if !ok {
		panic("tenant context missing - this indicates a programming error")
	}
	return tenant
}
