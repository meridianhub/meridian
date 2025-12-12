// Package clients provides gRPC client wrappers with resilience patterns.
//
// This package re-exports types from shared/pkg/clients for backward compatibility.
// New code should import directly from github.com/meridianhub/meridian/shared/pkg/clients.
package clients

import (
	"context"
	"time"

	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
)

// PropagateCorrelationID extracts correlation ID from context and adds it to gRPC metadata.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
func PropagateCorrelationID(ctx context.Context) context.Context {
	return sharedclients.PropagateCorrelationID(ctx)
}

// ExtractCorrelationID attempts to extract correlation ID from context.
// It checks multiple common keys used for correlation/request tracking.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
func ExtractCorrelationID(ctx context.Context) string {
	return sharedclients.ExtractCorrelationID(ctx)
}

// WithTimeout applies a timeout to the context if one isn't already set.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return sharedclients.WithTimeout(ctx, timeout)
}

// PropagateOrganization extracts organization ID from context and adds it to gRPC metadata.
// Returns the same context if org is missing or empty (graceful degradation for single-tenant or bootstrap calls).
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
func PropagateOrganization(ctx context.Context) context.Context {
	return sharedclients.PropagateOrganization(ctx)
}
