package auth

import (
	"context"
	"log/slog"
	"sync"

	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// warnOnceAuthEnabled emits a one-time warning when TenantExtractionInterceptor is
// used with AUTH_ENABLED=true. This interceptor trusts headers without JWT validation,
// so using it in production (where AUTH_ENABLED defaults to true) is a security risk.
var warnOnceAuthEnabled sync.Once

// checkAuthEnabled is the function used to check AUTH_ENABLED. Overridable in tests.
var checkAuthEnabled = func() bool {
	return env.GetEnvAsBool("AUTH_ENABLED", true)
}

// warnLogger is the logger used for tenant extraction warnings. Overridable in tests.
var warnLogger = slog.Default

// TenantExtractionInterceptor extracts tenant ID from gRPC metadata (x-tenant-id header).
//
// WARNING: This interceptor is for DEVELOPMENT/TESTING ONLY. It trusts the x-tenant-id
// header without JWT validation. In production, use auth.Interceptor.UnaryInterceptor()
// which validates the header against the JWT tenant_id claim.
//
// IMPORTANT: This interceptor is MUTUALLY EXCLUSIVE with auth.Interceptor.UnaryInterceptor().
// Do NOT use both in the same interceptor chain.
//
//   - With AUTH_ENABLED=true:  Use auth.Interceptor.UnaryInterceptor() which validates
//     the x-tenant-id header against the JWT tenant_id claim for security.
//   - With AUTH_ENABLED=false: Use this interceptor (TenantExtractionInterceptor) which
//     trusts the x-tenant-id header without JWT validation (development/testing only).
//
// The x-tenant-id header is set by the API gateway's TenantResolverMiddleware based on
// the request subdomain (e.g., "acme.api.meridian.io" -> x-tenant-id: "org_acme_uuid").
//
// Use case: Service A calls Service B with tenant in metadata. Service B
// extracts the tenant from metadata and injects it into context, enabling multi-hop
// call chains to propagate tenant context.
//
// Security: The tenant ID format is validated before being added to context.
// Invalid tenant IDs are silently ignored (context remains unchanged).
// However, this interceptor does NOT validate tenant ownership - use auth.Interceptor
// in production to ensure the JWT tenant matches the header.
func TenantExtractionInterceptor() grpc.UnaryServerInterceptor {
	// Emit a one-time warning if AUTH_ENABLED=true, since this interceptor
	// should only be used in development/testing when auth is disabled.
	warnOnceAuthEnabled.Do(func() {
		if checkAuthEnabled() {
			warnLogger().Warn("TenantExtractionInterceptor is active with AUTH_ENABLED=true; "+
				"this interceptor trusts x-tenant-id headers without JWT validation "+
				"and should only be used for development/testing",
				"interceptor", "TenantExtractionInterceptor",
				"recommendation", "use auth.Interceptor.UnaryInterceptor() for production",
			)
		}
	})

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
// IMPORTANT: This interceptor is MUTUALLY EXCLUSIVE with auth.Interceptor.StreamInterceptor().
// Do NOT use both in the same interceptor chain. See TenantExtractionInterceptor docs for details.
//
// If tenant is already in context (from JWT auth), this is a no-op.
//
// Security: The tenant ID format is validated before being added to context.
// Invalid tenant IDs are silently ignored (context remains unchanged).
// However, this interceptor does NOT validate tenant ownership - use auth.Interceptor
// in production to ensure the JWT tenant matches the header.
func TenantExtractionStreamInterceptor() grpc.StreamServerInterceptor {
	// Reuse the same sync.Once - warning only needs to fire once per process
	warnOnceAuthEnabled.Do(func() {
		if checkAuthEnabled() {
			warnLogger().Warn("TenantExtractionStreamInterceptor is active with AUTH_ENABLED=true; "+
				"this interceptor trusts x-tenant-id headers without JWT validation "+
				"and should only be used for development/testing",
				"interceptor", "TenantExtractionStreamInterceptor",
				"recommendation", "use auth.Interceptor.StreamInterceptor() for production",
			)
		}
	})

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
