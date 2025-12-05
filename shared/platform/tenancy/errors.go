// Package tenancy provides multi-tenant context propagation and tenant identification.
package tenancy

import "errors"

// Sentinel errors for tenant operations.
var (
	// ErrMissingTenantContext indicates that the tenant context is missing from the request.
	// This is a programming error - all tenant-scoped operations must have a tenant context.
	ErrMissingTenantContext = errors.New("tenant context missing")

	// ErrInvalidTenantID indicates that the tenant ID format is invalid.
	ErrInvalidTenantID = errors.New("invalid tenant ID: must be 1-50 alphanumeric characters or underscores")
)
