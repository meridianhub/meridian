package clients

import (
	"context"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/metadata"
)

// PropagateCorrelationID extracts correlation ID from context and adds it to gRPC metadata
func PropagateCorrelationID(ctx context.Context) context.Context {
	correlationID := ExtractCorrelationID(ctx)
	if correlationID == "" {
		return ctx
	}

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		md = md.Copy()
	}

	md.Set("x-correlation-id", correlationID)
	return metadata.NewOutgoingContext(ctx, md)
}

// ExtractCorrelationID attempts to extract correlation ID from context
// It checks multiple common keys used for correlation/request tracking
func ExtractCorrelationID(ctx context.Context) string {
	keys := []string{"correlation-id", "x-correlation-id", "x-request-id", "request-id"}

	// Check context values first
	for _, key := range keys {
		if val := ctx.Value(key); val != nil {
			if id, ok := val.(string); ok && id != "" {
				return id
			}
		}
	}

	// Check incoming metadata as fallback
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, key := range keys {
			if vals := md.Get(key); len(vals) > 0 && vals[0] != "" {
				return vals[0]
			}
		}
	}

	return ""
}

// WithTimeout applies a timeout to the context if one isn't already set
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return ctx, func() {}
}

// PropagateOrganization extracts organization ID from context and adds it to gRPC metadata.
// Returns the same context if org is missing or empty (graceful degradation for single-tenant or bootstrap calls).
// Usage pattern:
//
//	ctx = clients.PropagateCorrelationID(ctx)
//	ctx = clients.PropagateOrganization(ctx)
//	resp, err := client.SomeMethod(ctx, req)
func PropagateOrganization(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}

	orgID, ok := tenant.FromContext(ctx)
	if !ok || orgID.IsEmpty() {
		// No org in context or empty org - return unchanged (single-tenant or bootstrap call)
		return ctx
	}

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		md = md.Copy()
	}

	// Add org to outgoing metadata using standard header name
	md.Set(tenant.TenantIDKey, orgID.String())
	return metadata.NewOutgoingContext(ctx, md)
}
