package auth

import (
	"context"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TenantExtractionInterceptor extracts tenant ID from gRPC metadata.
// This works alongside JWT auth interceptor for service-to-service calls.
// If tenant is already in context (from JWT auth), this is a no-op.
//
// Use case: Service A calls Service B with tenant in metadata. Service B
// extracts the tenant from metadata and injects it into context, enabling multi-hop
// call chains to propagate tenant context.
//
// Security: The tenant ID is validated before being added to context.
// Invalid tenant IDs are silently ignored (context remains unchanged).
func TenantExtractionInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Check if tenant is already in context (from JWT auth)
		if _, ok := tenant.FromContext(ctx); ok {
			return handler(ctx, req)
		}

		// Extract from incoming metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			if vals := md.Get(tenant.TenantIDKey); len(vals) > 0 {
				// Validate tenant ID to prevent malformed values from untrusted callers
				tenantID, err := tenant.NewTenantID(vals[0])
				if err == nil {
					ctx = tenant.WithTenant(ctx, tenantID)
				}
				// Invalid tenant IDs are silently ignored - context unchanged
			}
		}

		return handler(ctx, req)
	}
}

// TenantExtractionStreamInterceptor extracts tenant ID from gRPC
// metadata for streaming RPCs. This is the streaming equivalent of
// TenantExtractionInterceptor.
//
// If tenant is already in context (from JWT auth), this is a no-op.
//
// Security: The tenant ID is validated before being added to context.
// Invalid tenant IDs are silently ignored (context remains unchanged).
func TenantExtractionStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx := ss.Context()

		// Check if tenant is already in context (from JWT auth)
		if _, ok := tenant.FromContext(ctx); ok {
			return handler(srv, ss)
		}

		// Extract from incoming metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			if vals := md.Get(tenant.TenantIDKey); len(vals) > 0 {
				// Validate tenant ID to prevent malformed values from untrusted callers
				tenantID, err := tenant.NewTenantID(vals[0])
				if err == nil {
					ctx = tenant.WithTenant(ctx, tenantID)

					// Wrap stream with the new context containing tenant
					wrappedStream := &wrappedServerStream{
						ServerStream: ss,
						ctx:          ctx,
					}

					return handler(srv, wrappedStream)
				}
				// Invalid tenant IDs are silently ignored - use original stream
			}
		}

		return handler(srv, ss)
	}
}
