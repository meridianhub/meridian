package tenant

import "context"

// contextKey is a private type to prevent collisions with context keys.
type contextKey string

// tenantContextKey is the context key for tenant ID.
const tenantContextKey contextKey = TenantIDKey

// WithTenant returns a new context with the tenant ID attached.
func WithTenant(ctx context.Context, tenantID TenantID) context.Context {
	return context.WithValue(ctx, tenantContextKey, tenantID)
}

// FromContext extracts the tenant ID from the context.
// Returns the tenant ID and true if present, or an empty tenant ID and false if not.
// Returns (empty, false) if ctx is nil.
func FromContext(ctx context.Context) (TenantID, bool) {
	if ctx == nil {
		return "", false
	}
	t, ok := ctx.Value(tenantContextKey).(TenantID)
	return t, ok
}

// MustFromContext extracts the tenant ID from the context.
// Panics if the tenant context is missing - this is a fail-fast strategy for
// catching programming errors where tenant context propagation was not set up correctly.
func MustFromContext(ctx context.Context) TenantID {
	t, ok := FromContext(ctx)
	if !ok {
		panic("tenant context missing - this indicates a programming error")
	}
	return t
}

// RequireFromContext extracts the tenant ID from the context.
// Returns ErrMissingTenantContext if the tenant context is missing.
// Use this when you want to handle missing context gracefully rather than panicking.
func RequireFromContext(ctx context.Context) (TenantID, error) {
	t, ok := FromContext(ctx)
	if !ok {
		return "", ErrMissingTenantContext
	}
	return t, nil
}
