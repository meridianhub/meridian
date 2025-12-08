// Package organization provides multi-organization context propagation and organization identification.
package organization

import "errors"

// Sentinel errors for organization operations.
var (
	// ErrMissingOrganizationContext indicates that the organization context is missing from the request.
	// This is a programming error - all organization-scoped operations must have an organization context.
	ErrMissingOrganizationContext = errors.New("organization context missing")

	// ErrInvalidOrganizationID indicates that the organization ID format is invalid.
	ErrInvalidOrganizationID = errors.New("invalid organization ID: must be 1-50 alphanumeric characters or underscores")
)
