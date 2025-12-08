package organization

import "context"

// contextKey is a private type to prevent collisions with context keys.
type contextKey string

// organizationContextKey is the context key for organization ID.
const organizationContextKey contextKey = OrgIDKey

// WithOrganization returns a new context with the organization ID attached.
func WithOrganization(ctx context.Context, orgID OrganizationID) context.Context {
	return context.WithValue(ctx, organizationContextKey, orgID)
}

// FromContext extracts the organization ID from the context.
// Returns the organization ID and true if present, or an empty organization ID and false if not.
// Returns (empty, false) if ctx is nil.
func FromContext(ctx context.Context) (OrganizationID, bool) {
	if ctx == nil {
		return "", false
	}
	org, ok := ctx.Value(organizationContextKey).(OrganizationID)
	return org, ok
}

// MustFromContext extracts the organization ID from the context.
// Panics if the organization context is missing - this is a fail-fast strategy for
// catching programming errors where organization context propagation was not set up correctly.
func MustFromContext(ctx context.Context) OrganizationID {
	org, ok := FromContext(ctx)
	if !ok {
		panic("organization context missing - this indicates a programming error")
	}
	return org
}
