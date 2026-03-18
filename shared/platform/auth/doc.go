// Package auth provides JWT authentication middleware for Meridian gRPC services.
//
// The package supports three authentication modes configured via AUTH_MODE:
//   - "jwks": validates Bearer tokens against a JWKS endpoint (production default)
//   - "oauth": issues outbound OAuth client-credentials tokens for service-to-service calls
//   - "disabled": passes requests through without validation (local development only)
//
// Validated tokens are parsed and the tenant ID and subject are injected into
// the request context for downstream handlers via the [tenant] package.
//
// # gRPC Interceptor
//
//	interceptor, err := auth.NewInterceptor(ctx, auth.DefaultConfig(logger))
//	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(interceptor.Unary()))
//
// # Environment Variables
//
//   - AUTH_MODE: "jwks" | "oauth" | "disabled" (default: "jwks")
//   - JWKS_URL: URL of the JWKS endpoint (required when AUTH_MODE=jwks)
package auth
