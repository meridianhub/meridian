// Package interceptors provides shared gRPC interceptors for all Meridian services.
//
// The interceptors in this package provide cross-cutting concerns like panic recovery,
// logging, and tracing that should be consistently applied across all gRPC services.
//
// # Panic Recovery
//
// The recovery interceptors prevent service crashes from panics in business logic:
//
//   - RecoveryUnaryInterceptor: Recovers from panics in unary RPCs
//   - RecoveryStreamInterceptor: Recovers from panics in streaming RPCs
//   - RecoveryStreamInterceptorWithWrappedStream: Provides granular recovery for Send/Recv operations
//
// All recovery interceptors log panic details with stack traces for debugging and return
// codes.Internal to clients without exposing internal error details.
//
// # Interceptor Chain Order
//
// When building an interceptor chain, the recommended order is:
//
//  1. Metrics (first to capture all requests)
//  2. Tracing (for observability)
//  3. Auth (authentication/authorization) - see SECURITY NOTE below
//  4. Recovery (last to catch all panics from above layers)
//
// # SECURITY NOTE: Tenant Context Validation
//
// The auth package provides two complementary interceptors for tenant context:
//
//   - auth.Interceptor.UnaryInterceptor(): Validates JWT and extracts tenant_id claim.
//     ALSO validates that x-tenant-id header (if present) matches the JWT tenant claim.
//     This prevents cross-tenant attacks where a user with a valid JWT for tenant A
//     attempts to access tenant B's resources via header manipulation.
//
//   - auth.TenantExtractionInterceptor(): Extracts tenant from x-tenant-id header ONLY.
//     Used when auth is disabled (development/testing). Does NOT validate against JWT.
//
// IMPORTANT: These interceptors are MUTUALLY EXCLUSIVE in the chain:
//   - With AUTH_ENABLED=true: Use auth.Interceptor.UnaryInterceptor() (validates header vs JWT)
//   - With AUTH_ENABLED=false: Use auth.TenantExtractionInterceptor() (header extraction only)
//
// The x-tenant-id header is set by the API gateway's TenantResolverMiddleware based on
// the subdomain (e.g., "acme.api.meridian.io" -> x-tenant-id: "org_acme_uuid").
// The auth interceptor's double-check ensures a user's JWT tenant matches the subdomain
// they're accessing, preventing subdomain hopping attacks.
//
// Example usage:
//
//	unaryInterceptors := []grpc.UnaryServerInterceptor{
//	    metricsInterceptor,
//	    tracer.UnaryServerInterceptor(),
//	    authInterceptor,
//	    interceptors.RecoveryUnaryInterceptor(logger),
//	}
//	grpcServer := grpc.NewServer(
//	    grpc.ChainUnaryInterceptor(unaryInterceptors...),
//	)
package interceptors
