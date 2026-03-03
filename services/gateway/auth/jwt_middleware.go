// Package auth provides HTTP middleware for JWT authentication in the API gateway.
//
// This package handles the extraction and validation of JWT tokens from HTTP requests,
// storing verified claims in the request context for downstream handlers.
//
// Example usage:
//
//	middleware, err := NewJWTMiddleware(validator, logger)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	handler := middleware.Handler(appHandler)
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Context keys for JWT claims.
// Uses the shared contextKey type defined in apikey_middleware.go.
const (
	// UserIDContextKey is the context key for user ID extracted from JWT.
	UserIDContextKey contextKey = "user_id"
	// TenantIDContextKey is the context key for tenant ID extracted from JWT.
	TenantIDContextKey contextKey = "tenant_id"
	// RolesContextKey is the context key for roles extracted from JWT.
	RolesContextKey contextKey = "roles"
	// ScopesContextKey is the context key for scopes extracted from JWT.
	ScopesContextKey contextKey = "scopes"
	// ClaimsContextKey is the context key for full JWT claims.
	ClaimsContextKey contextKey = "claims"
)

// Configuration and validation errors.
var (
	ErrNilValidator = errors.New("validator cannot be nil")
	ErrNilLogger    = errors.New("logger cannot be nil")
)

// Authorization header extraction errors.
var (
	ErrMissingAuthHeader   = errors.New("missing authorization header")
	ErrBearerCaseSensitive = errors.New("invalid authorization header: Bearer scheme is case-sensitive")
	ErrBasicSchemeUsed     = errors.New("invalid authorization header: expected Bearer scheme, got Basic")
	ErrInvalidScheme       = errors.New("invalid authorization header: expected Bearer scheme")
	ErrEmptyToken          = errors.New("invalid authorization header: empty token")
	ErrMalformedToken      = errors.New("invalid authorization header: malformed token")
)

// JWTValidator defines the interface for validating JWT tokens.
// This interface allows for easier testing and supports both standard
// and JWKS-based validators.
type JWTValidator interface {
	ValidateToken(tokenString string) (*platformauth.Claims, error)
}

// JWTMiddlewareConfig holds optional configuration for the JWT middleware.
// Kept for API compatibility; all fields have been removed.
type JWTMiddlewareConfig struct{}

// JWTMiddleware provides HTTP middleware for JWT authentication.
// It extracts the Authorization header, validates the Bearer token,
// and injects verified claims into the request context.
type JWTMiddleware struct {
	validator JWTValidator
	logger    *slog.Logger
}

// NewJWTMiddleware creates a new JWT authentication middleware.
// Both validator and logger are required parameters.
func NewJWTMiddleware(validator JWTValidator, logger *slog.Logger, _ ...JWTMiddlewareConfig) (*JWTMiddleware, error) {
	if validator == nil {
		return nil, ErrNilValidator
	}
	if logger == nil {
		return nil, ErrNilLogger
	}

	return &JWTMiddleware{
		validator: validator,
		logger:    logger,
	}, nil
}

// Handler returns an http.Handler that performs JWT authentication.
// It extracts the Authorization header, validates the Bearer token,
// and injects verified claims into the request context.
//
// On successful validation:
//   - User ID, tenant ID, roles, scopes, and full claims are stored in context
//   - The request proceeds to the next handler
//
// On failure:
//   - Returns 401 Unauthorized with appropriate error message
//   - Logs the failure reason for debugging/auditing
func (m *JWTMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Step 1: Extract token from Authorization header
		token, err := extractBearerToken(r)
		if err != nil {
			m.logger.Debug("failed to extract bearer token",
				slog.String("error", err.Error()),
				slog.String("path", r.URL.Path),
			)
			writeUnauthorized(w, err.Error())
			return
		}

		// Step 2: Validate the token
		claims, err := m.validator.ValidateToken(token)
		if err != nil {
			m.handleValidationError(w, r, err)
			return
		}

		// Step 3: Enforce required claims.
		// Tenant must come from the JWT — no fallback defaults are allowed.
		if claims.TenantID == "" {
			m.logger.Warn("JWT missing required x-tenant-id claim",
				slog.String("path", r.URL.Path),
			)
			writeUnauthorized(w, "x-tenant-id claim required")
			return
		}

		// Create a shallow copy so the original token claims are not mutated,
		// but downstream middleware sees the effective values when reading from context.
		effectiveClaims := *claims
		effectiveClaims.UserID = claims.EffectiveUserID()

		// Step 4: Inject effective claims into context
		ctx := injectClaimsToContext(r.Context(), &effectiveClaims)

		// Step 5: Log successful authentication
		m.logger.Debug("JWT authentication successful",
			slog.String("user_id", effectiveClaims.UserID),
			slog.String("tenant_id", effectiveClaims.TenantID),
			slog.String("path", r.URL.Path),
		)

		// Step 5: Call next handler with enriched context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractBearerToken extracts the JWT token from the Authorization header.
// It validates the header format and returns the token string.
//
// Expected format: "Authorization: Bearer <token>"
//
// Returns errors for:
//   - Missing Authorization header
//   - Non-Bearer authentication scheme
//   - Empty token after Bearer prefix
//   - Malformed header format
func extractBearerToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", ErrMissingAuthHeader
	}

	// Check for Bearer prefix (case-sensitive per RFC 6750)
	if !strings.HasPrefix(authHeader, "Bearer ") {
		// Check for common mistakes
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			return "", ErrBearerCaseSensitive
		}
		if strings.HasPrefix(authHeader, "Basic ") {
			return "", ErrBasicSchemeUsed
		}
		return "", ErrInvalidScheme
	}

	// Extract token after "Bearer "
	token := strings.TrimPrefix(authHeader, "Bearer ")

	// Validate token is not empty
	if token == "" {
		return "", ErrEmptyToken
	}

	// Validate token doesn't contain spaces (malformed)
	if strings.Contains(token, " ") {
		return "", ErrMalformedToken
	}

	return token, nil
}

// handleValidationError handles JWT validation errors and writes appropriate HTTP responses.
// It maps different validation errors to appropriate error messages.
func (m *JWTMiddleware) handleValidationError(w http.ResponseWriter, r *http.Request, err error) {
	var message string

	switch {
	case errors.Is(err, platformauth.ErrTokenExpired):
		message = "token expired"
		m.logger.Debug("JWT validation failed: token expired",
			slog.String("path", r.URL.Path),
		)
	case errors.Is(err, platformauth.ErrInvalidSignature):
		message = "invalid token signature"
		m.logger.Warn("JWT validation failed: invalid signature",
			slog.String("path", r.URL.Path),
		)
	case errors.Is(err, platformauth.ErrInvalidToken):
		message = "invalid token"
		m.logger.Debug("JWT validation failed: invalid token",
			slog.String("path", r.URL.Path),
		)
	case errors.Is(err, platformauth.ErrTokenStringEmpty):
		message = "invalid token"
		m.logger.Debug("JWT validation failed: empty token string",
			slog.String("path", r.URL.Path),
		)
	default:
		message = "authentication failed"
		m.logger.Warn("JWT validation failed with unexpected error",
			slog.String("error", err.Error()),
			slog.String("path", r.URL.Path),
		)
	}

	writeUnauthorized(w, message)
}

// injectClaimsToContext adds verified JWT claims to the request context.
// It stores individual claim values for easy access and the full claims object
// for cases where all claims are needed.
func injectClaimsToContext(ctx context.Context, claims *platformauth.Claims) context.Context {
	ctx = context.WithValue(ctx, UserIDContextKey, claims.UserID)
	ctx = context.WithValue(ctx, TenantIDContextKey, claims.TenantID)
	ctx = context.WithValue(ctx, RolesContextKey, claims.GetRoles())
	ctx = context.WithValue(ctx, ScopesContextKey, claims.GetScopes())
	ctx = context.WithValue(ctx, ClaimsContextKey, claims)

	// Also inject tenant into context using the shared tenant package
	// This allows downstream handlers to use tenant.FromContext()
	if claims.TenantID != "" {
		if tenantID, err := tenant.NewTenantID(claims.TenantID); err == nil {
			ctx = tenant.WithTenant(ctx, tenantID)
		}
	}

	return ctx
}

// writeUnauthorized writes a 401 Unauthorized response with the given message
// using proper JSON encoding to prevent injection attacks.
func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

// GetUserIDFromContext extracts the user ID from the request context.
// Returns the user ID and true if found, empty string and false otherwise.
func GetUserIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(UserIDContextKey).(string)
	return userID, ok
}

// GetTenantIDFromContext extracts the tenant ID from the request context.
// Returns the tenant ID and true if found, empty string and false otherwise.
func GetTenantIDFromContext(ctx context.Context) (string, bool) {
	tenantID, ok := ctx.Value(TenantIDContextKey).(string)
	return tenantID, ok
}

// GetRolesFromContext extracts the roles from the request context.
// Returns the roles slice and true if found, nil and false otherwise.
func GetRolesFromContext(ctx context.Context) ([]string, bool) {
	roles, ok := ctx.Value(RolesContextKey).([]string)
	return roles, ok
}

// GetScopesFromContext extracts the scopes from the request context.
// Returns the scopes slice and true if found, nil and false otherwise.
func GetScopesFromContext(ctx context.Context) ([]string, bool) {
	scopes, ok := ctx.Value(ScopesContextKey).([]string)
	return scopes, ok
}

// GetClaimsFromContext extracts the full JWT claims from the request context.
// Returns the claims and true if found, nil and false otherwise.
func GetClaimsFromContext(ctx context.Context) (*platformauth.Claims, bool) {
	claims, ok := ctx.Value(ClaimsContextKey).(*platformauth.Claims)
	return claims, ok
}
