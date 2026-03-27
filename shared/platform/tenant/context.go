package tenant

import "context"

// contextKey is a private type to prevent collisions with context keys.
type contextKey string

// tenantContextKey is the context key for tenant ID.
const tenantContextKey contextKey = TenantIDKey

// slugContextKey is the context key for the tenant slug.
const slugContextKey contextKey = TenantSlugKey

// displayNameContextKey is the context key for the tenant display name.
const displayNameContextKey contextKey = TenantDisplayNameKey

// WithTenant returns a new context with the tenant ID attached.
func WithTenant(ctx context.Context, tenantID TenantID) context.Context {
	return context.WithValue(ctx, tenantContextKey, tenantID)
}

// WithSlug returns a new context with the tenant slug attached.
func WithSlug(ctx context.Context, slug string) context.Context {
	return context.WithValue(ctx, slugContextKey, slug)
}

// WithDisplayName returns a new context with the tenant display name attached.
func WithDisplayName(ctx context.Context, displayName string) context.Context {
	return context.WithValue(ctx, displayNameContextKey, displayName)
}

// DisplayNameFromContext extracts the tenant display name from the context.
// Returns the display name and true if present, or empty string and false if not.
func DisplayNameFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	s, ok := ctx.Value(displayNameContextKey).(string)
	return s, ok
}

// SlugFromContext extracts the tenant slug from the context.
// Returns the slug and true if present, or empty string and false if not.
func SlugFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	s, ok := ctx.Value(slugContextKey).(string)
	return s, ok
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
//
// Deprecated: Use RequireFromContext in request handler paths (HTTP/gRPC) where
// returning an error is preferable to panicking. MustFromContext is still appropriate
// for init/startup code where a missing tenant context indicates a fatal configuration error.
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

// PropagateToBackground creates a new background context with the tenant ID
// from the parent context propagated. This is useful for async operations
// (goroutines) that need to outlive the request context but still require
// tenant context for database operations.
//
// Usage:
//
//	// Instead of:
//	//   asyncCtx := context.Background()
//	//   if tenantID, ok := tenant.FromContext(ctx); ok {
//	//       asyncCtx = tenant.WithTenant(asyncCtx, tenantID)
//	//   }
//	//
//	// Use:
//	asyncCtx := tenant.PropagateToBackground(ctx)
//	go func() {
//	    // asyncCtx has tenant ID but no deadline/cancellation from parent
//	    repo.FindByID(asyncCtx, id)
//	}()
//
// If the parent context has no tenant, returns a plain background context.
func PropagateToBackground(parent context.Context) context.Context {
	asyncCtx := context.Background()
	if tenantID, ok := FromContext(parent); ok {
		asyncCtx = WithTenant(asyncCtx, tenantID)
	}
	if slug, ok := SlugFromContext(parent); ok {
		asyncCtx = WithSlug(asyncCtx, slug)
	}
	if displayName, ok := DisplayNameFromContext(parent); ok {
		asyncCtx = WithDisplayName(asyncCtx, displayName)
	}
	return asyncCtx
}
