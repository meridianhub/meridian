package middleware

import "errors"

// Sentinel errors for tenant resolution.
var (
	// ErrMissingTenant indicates that no tenant subdomain was provided.
	ErrMissingTenant = errors.New("tenant subdomain required")

	// ErrInvalidHost indicates that the host does not match the expected base domain.
	ErrInvalidHost = errors.New("invalid host: does not match expected domain")
)
