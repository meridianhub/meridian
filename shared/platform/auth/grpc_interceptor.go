package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	// ErrMissingAuthHeader is returned when the Authorization header is missing
	ErrMissingAuthHeader = errors.New("missing authorization header")
	// ErrInvalidAuthHeader is returned when the Authorization header format is invalid
	ErrInvalidAuthHeader = errors.New("invalid authorization header format")
	// ErrValidatorNil is returned when a nil validator is provided
	ErrValidatorNil = errors.New("validator cannot be nil")
)

// contextKey is used for storing values in context
type contextKey string

const (
	// UserIDContextKey is the context key for user ID
	UserIDContextKey contextKey = "user_id"
	// RolesContextKey is the context key for user roles
	RolesContextKey contextKey = "roles"
	// ScopesContextKey is the context key for user scopes
	ScopesContextKey contextKey = "scopes"
	// ClaimsContextKey is the context key for full claims
	ClaimsContextKey contextKey = "claims"
)

// Interceptor provides gRPC interceptors for JWT authentication
type Interceptor struct {
	validator     *JWTValidator
	jwksValidator *JWTValidatorWithJWKS
	bypassMethods map[string]bool
	useJWKS       bool
}

// InterceptorConfig holds configuration for the auth interceptor
type InterceptorConfig struct {
	Validator     *JWTValidator         // Standard JWT validator
	JWKSValidator *JWTValidatorWithJWKS // JWKS-based validator
	BypassMethods []string              // Methods to bypass authentication (e.g., health checks)
}

// NewAuthInterceptor creates a new authentication interceptor
func NewAuthInterceptor(cfg *InterceptorConfig) (*Interceptor, error) {
	if cfg.Validator == nil && cfg.JWKSValidator == nil {
		return nil, fmt.Errorf("failed to create interceptor: %w", ErrValidatorNil)
	}

	bypassMap := make(map[string]bool)
	for _, method := range cfg.BypassMethods {
		bypassMap[method] = true
	}

	return &Interceptor{
		validator:     cfg.Validator,
		jwksValidator: cfg.JWKSValidator,
		bypassMethods: bypassMap,
		useJWKS:       cfg.JWKSValidator != nil,
	}, nil
}

// UnaryInterceptor returns a gRPC unary server interceptor for authentication
func (a *Interceptor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Check if method should bypass authentication
		if a.bypassMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		// Authenticate and inject context
		newCtx, err := a.authenticate(ctx)
		if err != nil {
			return nil, err
		}

		return handler(newCtx, req)
	}
}

// StreamInterceptor returns a gRPC stream server interceptor for authentication
func (a *Interceptor) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// Check if method should bypass authentication
		if a.bypassMethods[info.FullMethod] {
			return handler(srv, ss)
		}

		// Authenticate and inject context
		newCtx, err := a.authenticate(ss.Context())
		if err != nil {
			return err
		}

		// Wrap stream with authenticated context
		wrappedStream := &wrappedServerStream{
			ServerStream: ss,
			ctx:          newCtx,
		}

		return handler(srv, wrappedStream)
	}
}

// authenticate extracts and validates the JWT token, returning an enriched context.
// This is for tenant-layer services that REQUIRE tenant_id claims.
func (a *Interceptor) authenticate(ctx context.Context) (context.Context, error) {
	claims, err := a.validateToken(ctx)
	if err != nil {
		return nil, err
	}

	// Inject base claims into context
	ctx = injectBaseClaims(ctx, claims)

	// Tenant context injection - ALWAYS required for tenant-layer services.
	// The system is always multi-tenant (platform schema + 1 to N org_X schemas).
	// Platform-layer services (like Tenant Service) should NOT use this interceptor.
	tenantID, err := claims.GetTenantID()
	if err != nil {
		if errors.Is(err, ErrTenantClaimMissing) {
			return nil, status.Error(codes.Unauthenticated, "tenant_id claim required")
		}
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id format")
	}
	ctx = tenant.WithTenant(ctx, tenantID)

	// Add tenant to OpenTelemetry span attributes
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("tenant.id", tenantID.String()))

	return ctx, nil
}

// validateToken extracts and validates the JWT token from context metadata.
// This is the common token validation logic used by both authenticate() and authenticatePlatform().
func (a *Interceptor) validateToken(ctx context.Context) (*Claims, error) {
	token, err := extractTokenFromMetadata(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "extract token: %v", err)
	}

	var claims *Claims
	if a.useJWKS {
		claims, err = a.jwksValidator.ValidateToken(ctx, token)
	} else {
		claims, err = a.validator.ValidateToken(token)
	}

	if err != nil {
		if errors.Is(err, ErrTokenExpired) {
			return nil, status.Error(codes.Unauthenticated, "token expired")
		}
		if errors.Is(err, ErrInvalidSignature) || errors.Is(err, ErrInvalidToken) {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}
		return nil, status.Error(codes.Unauthenticated, "authentication failed")
	}

	return claims, nil
}

// injectBaseClaims adds user identity claims to the context.
// This is the common claims injection logic used by both authenticate() and authenticatePlatform().
func injectBaseClaims(ctx context.Context, claims *Claims) context.Context {
	ctx = context.WithValue(ctx, UserIDContextKey, claims.UserID)
	ctx = context.WithValue(ctx, RolesContextKey, claims.Roles)
	ctx = context.WithValue(ctx, ScopesContextKey, claims.Scopes)
	ctx = context.WithValue(ctx, ClaimsContextKey, claims)
	return ctx
}

// extractTokenFromMetadata extracts the Bearer token from gRPC metadata
func extractTokenFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", fmt.Errorf("failed to get metadata: %w", ErrMissingAuthHeader)
	}

	values := md.Get("authorization")
	if len(values) == 0 {
		return "", fmt.Errorf("no authorization header: %w", ErrMissingAuthHeader)
	}

	authHeader := values[0]
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", fmt.Errorf("expected Bearer scheme: %w", ErrInvalidAuthHeader)
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return "", fmt.Errorf("empty token: %w", ErrInvalidAuthHeader)
	}

	return token, nil
}

// wrappedServerStream wraps grpc.ServerStream to override Context()
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context returns the wrapped context
func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

// GetUserIDFromContext extracts the user ID from the context
func GetUserIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(UserIDContextKey).(string)
	return userID, ok
}

// GetRolesFromContext extracts the roles from the context
func GetRolesFromContext(ctx context.Context) ([]string, bool) {
	roles, ok := ctx.Value(RolesContextKey).([]string)
	return roles, ok
}

// GetScopesFromContext extracts the scopes from the context
func GetScopesFromContext(ctx context.Context) ([]string, bool) {
	scopes, ok := ctx.Value(ScopesContextKey).([]string)
	return scopes, ok
}

// GetClaimsFromContext extracts the full claims from the context
func GetClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(ClaimsContextKey).(*Claims)
	return claims, ok
}

// RequireRole creates an interceptor that requires specific roles
func RequireRole(requiredRoles ...string) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		claims, ok := GetClaimsFromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Internal, "missing authentication context")
		}

		// Check if user has any of the required roles
		hasRole := false
		for _, required := range requiredRoles {
			if claims.HasRole(required) {
				hasRole = true
				break
			}
		}

		if !hasRole {
			return nil, status.Error(codes.PermissionDenied, "insufficient permissions")
		}

		return handler(ctx, req)
	}
}

// RequireScope creates an interceptor that requires specific scopes
func RequireScope(requiredScopes ...string) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		claims, ok := GetClaimsFromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Internal, "missing authentication context")
		}

		// Check if user has all required scopes
		for _, required := range requiredScopes {
			if !claims.HasScope(required) {
				return nil, status.Error(codes.PermissionDenied, "insufficient scopes")
			}
		}

		return handler(ctx, req)
	}
}

// PlatformUnaryInterceptor returns a gRPC unary server interceptor for platform-layer
// services. Unlike the standard UnaryInterceptor, this does NOT require tenant_id claims.
// Platform services (like Tenant Service) are tenant-agnostic and operate in the platform
// schema. Use this in combination with PlatformAdminInterceptor for full authorization.
func (a *Interceptor) PlatformUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Check if method should bypass authentication
		if a.bypassMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		// Authenticate and inject context (without tenant requirement)
		newCtx, err := a.authenticatePlatform(ctx)
		if err != nil {
			return nil, err
		}

		return handler(newCtx, req)
	}
}

// PlatformStreamInterceptor returns a gRPC stream server interceptor for platform-layer
// services. Unlike the standard StreamInterceptor, this does NOT require tenant_id claims.
func (a *Interceptor) PlatformStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// Check if method should bypass authentication
		if a.bypassMethods[info.FullMethod] {
			return handler(srv, ss)
		}

		// Authenticate and inject context (without tenant requirement)
		newCtx, err := a.authenticatePlatform(ss.Context())
		if err != nil {
			return err
		}

		// Wrap stream with authenticated context
		wrappedStream := &wrappedServerStream{
			ServerStream: ss,
			ctx:          newCtx,
		}

		return handler(srv, wrappedStream)
	}
}

// authenticatePlatform extracts and validates the JWT token for platform services.
// Unlike authenticate(), this does NOT require tenant_id claims.
// Platform services are tenant-agnostic and operate in the platform schema.
func (a *Interceptor) authenticatePlatform(ctx context.Context) (context.Context, error) {
	claims, err := a.validateToken(ctx)
	if err != nil {
		return nil, err
	}

	// Inject base claims into context (no tenant requirement for platform services)
	return injectBaseClaims(ctx, claims), nil
}
