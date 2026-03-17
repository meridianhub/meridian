package bootstrap

import (
	"errors"
	"log/slog"

	"github.com/meridianhub/meridian/shared/pkg/interceptors"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ratelimit"
	"google.golang.org/grpc"
)

// ErrAuthRequired is returned by Build() when neither WithAuthInterceptor()
// nor WithoutAuth() has been called.
var ErrAuthRequired = errors.New("grpc server: auth interceptor required; call WithAuthInterceptor() or WithoutAuth()")

// GrpcServerBuilder provides a fluent API for constructing gRPC servers
// with properly ordered interceptor chains.
//
// The interceptor chain order is critical for correctness:
//  1. Tracing (always first) - captures full request lifecycle including auth failures
//  2. Auth/TenantExtraction - validates JWT and populates claims in context
//  3. Rate limiting (if enabled) - per-tenant, per-method rate limiting
//  4. PlatformAdmin (if enabled) - requires platform-admin role, rejects tenant-scoped tokens
//  5. Custom interceptors - user-provided interceptors
//  6. Recovery (always last) - catches panics from any preceding interceptor or handler
//
// Build() is fail-closed: it returns an error unless WithAuthInterceptor()
// or WithoutAuth() has been called. This prevents services from accidentally
// running without authentication.
//
// Example usage for tenant-layer service:
//
//	server, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
//	    WithAuthInterceptor(authInterceptor).
//	    Build()
//
// Example usage for platform-layer service (like tenant service):
//
//	server, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
//	    WithAuthInterceptor(authInterceptor).
//	    WithPlatformAdmin().
//	    Build()
//
// Example usage when auth is handled elsewhere (unified binary / gateway):
//
//	server, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
//	    WithoutAuth().
//	    Build()
type GrpcServerBuilder struct {
	tracer          *observability.Tracer
	authInterceptor *auth.Interceptor
	authConfigured  bool
	platformAdmin   bool
	authOptOut      bool
	logger          *slog.Logger
	rateLimiter     *ratelimit.Interceptor
	extraUnary      []grpc.UnaryServerInterceptor
	extraStream     []grpc.StreamServerInterceptor
}

// NewGrpcServerBuilder creates a new GrpcServerBuilder with required dependencies.
//
// Parameters:
//   - tracer: OpenTelemetry tracer for distributed tracing (required)
//   - logger: Structured logger for interceptor logging (required)
//
// Returns a builder that can be configured with fluent methods before calling Build().
func NewGrpcServerBuilder(tracer *observability.Tracer, logger *slog.Logger) *GrpcServerBuilder {
	return &GrpcServerBuilder{
		tracer:      tracer,
		logger:      logger,
		extraUnary:  make([]grpc.UnaryServerInterceptor, 0),
		extraStream: make([]grpc.StreamServerInterceptor, 0),
	}
}

// WithAuthInterceptor adds JWT authentication to the interceptor chain.
//
// When auth is enabled:
//   - For tenant-layer services: validates JWT and requires tenant_id claim
//   - For platform-layer services (with WithPlatformAdmin): validates JWT without tenant requirement
//
// When auth is nil (disabled), TenantExtractionInterceptor is used instead,
// which trusts the x-tenant-id header without JWT validation (development only).
func (b *GrpcServerBuilder) WithAuthInterceptor(interceptor *auth.Interceptor) *GrpcServerBuilder {
	b.authInterceptor = interceptor
	b.authConfigured = true
	return b
}

// WithoutAuth explicitly opts out of authentication. Use this only when
// auth is handled at a different layer (e.g. the unified binary handles auth
// at the HTTP gateway level).
//
// Without this or WithAuthInterceptor(), Build() returns ErrAuthRequired.
func (b *GrpcServerBuilder) WithoutAuth() *GrpcServerBuilder {
	b.authOptOut = true
	return b
}

// WithPlatformAdmin enables platform admin validation for the interceptor chain.
//
// This is used for platform-layer services (like tenant service) that:
//   - Do NOT require tenant_id claims in JWT
//   - Require platform-admin or super-admin role
//   - Reject tenant-scoped tokens
//
// Must be combined with WithAuthInterceptor to have any effect.
func (b *GrpcServerBuilder) WithPlatformAdmin() *GrpcServerBuilder {
	b.platformAdmin = true
	return b
}

// WithRateLimiting adds per-tenant, per-method rate limiting to the interceptor chain.
// The rate limiter is positioned after auth (so tenant context is available)
// but before business logic.
func (b *GrpcServerBuilder) WithRateLimiting(limiter *ratelimit.Interceptor) *GrpcServerBuilder {
	b.rateLimiter = limiter
	return b
}

// WithUnaryInterceptor adds a custom unary interceptor to the chain.
//
// Custom interceptors are added after auth but before recovery,
// ensuring they have access to authenticated context and are protected
// from panics.
func (b *GrpcServerBuilder) WithUnaryInterceptor(interceptor grpc.UnaryServerInterceptor) *GrpcServerBuilder {
	b.extraUnary = append(b.extraUnary, interceptor)
	return b
}

// WithStreamInterceptor adds a custom stream interceptor to the chain.
//
// Custom interceptors are added after auth but before recovery,
// ensuring they have access to authenticated context and are protected
// from panics.
func (b *GrpcServerBuilder) WithStreamInterceptor(interceptor grpc.StreamServerInterceptor) *GrpcServerBuilder {
	b.extraStream = append(b.extraStream, interceptor)
	return b
}

// Build constructs the gRPC server with the configured interceptor chain.
//
// The interceptor chain is ordered as follows:
//  1. Tracing (always first)
//  2. Auth/TenantExtraction:
//     - If authInterceptor != nil AND platformAdmin: PlatformUnaryInterceptor
//     - If authInterceptor != nil AND !platformAdmin: UnaryInterceptor
//     - If authOptOut: TenantExtractionInterceptor (with warning)
//  3. Rate limiting (if configured via WithRateLimiting)
//  4. PlatformAdmin (if platformAdmin && authInterceptor != nil)
//  5. Custom interceptors (extraUnary/extraStream)
//  6. Recovery (always last)
//
// Returns ErrAuthRequired if neither WithAuthInterceptor() nor WithoutAuth()
// was called.
func (b *GrpcServerBuilder) Build() (*grpc.Server, error) {
	// Fail-closed: require explicit auth configuration.
	// WithAuthInterceptor(nil) counts as explicit (auth disabled in config).
	// WithoutAuth() counts as explicit (auth handled elsewhere).
	// Neither called = error.
	if !b.authConfigured && !b.authOptOut {
		return nil, ErrAuthRequired
	}

	var unaryInterceptors []grpc.UnaryServerInterceptor
	var streamInterceptors []grpc.StreamServerInterceptor

	// 1. Tracing (always first for full request coverage)
	unaryInterceptors = append(unaryInterceptors, b.tracer.UnaryServerInterceptor())
	streamInterceptors = append(streamInterceptors, b.tracer.StreamServerInterceptor())

	// 2. Auth/TenantExtraction
	if b.authInterceptor != nil {
		if b.platformAdmin {
			// Platform services: use PlatformUnaryInterceptor (no tenant requirement)
			unaryInterceptors = append(unaryInterceptors, b.authInterceptor.PlatformUnaryInterceptor())
			streamInterceptors = append(streamInterceptors, b.authInterceptor.PlatformStreamInterceptor())
		} else {
			// Tenant services: use UnaryInterceptor (requires tenant_id claim)
			unaryInterceptors = append(unaryInterceptors, b.authInterceptor.UnaryInterceptor())
			streamInterceptors = append(streamInterceptors, b.authInterceptor.StreamInterceptor())
		}
	} else {
		// Auth opted out: use TenantExtractionInterceptor (development only)
		unaryInterceptors = append(unaryInterceptors, auth.TenantExtractionInterceptor())
		streamInterceptors = append(streamInterceptors, auth.TenantExtractionStreamInterceptor())

		if b.logger != nil {
			b.logger.Warn("auth disabled - using tenant extraction interceptor",
				"hint", "set AUTH_ENABLED=true in production")
		}
	}

	// 3. Rate limiting (after auth, so tenant context is available)
	if b.rateLimiter != nil {
		unaryInterceptors = append(unaryInterceptors, b.rateLimiter.UnaryServerInterceptor())
	}

	// 4. PlatformAdmin (if enabled with auth)
	if b.platformAdmin && b.authInterceptor != nil {
		unaryInterceptors = append(unaryInterceptors, auth.PlatformAdminInterceptor())
		streamInterceptors = append(streamInterceptors, auth.PlatformAdminStreamInterceptor())
	}

	// 5. Custom interceptors
	unaryInterceptors = append(unaryInterceptors, b.extraUnary...)
	streamInterceptors = append(streamInterceptors, b.extraStream...)

	// 6. Recovery (always last to catch all panics)
	unaryInterceptors = append(unaryInterceptors, interceptors.RecoveryUnaryInterceptor(b.logger))
	streamInterceptors = append(streamInterceptors, interceptors.RecoveryStreamInterceptor(b.logger))

	return grpc.NewServer(
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	), nil
}
