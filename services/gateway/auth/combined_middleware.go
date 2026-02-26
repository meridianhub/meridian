// Package auth provides authentication middleware for the gateway service.
package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// CombinedAuthMiddleware provides authentication middleware that supports both
// JWT Bearer tokens and API keys. Requests are authenticated if they provide
// either a valid JWT token OR a valid API key.
//
// Authentication flow:
// 1. Check for X-API-Key header - if present, validate API key
//   - For pk_{slug}_{...} format: validate via Control Plane RPC (with caching)
//   - For legacy format: validate via env-var-based map lookup
//
// 2. If no API key, check for Authorization: Bearer header - if present, validate JWT
// 3. If neither is present, return 401 Unauthorized
//
// On successful authentication:
// - For JWT: User ID, tenant ID, roles, scopes are injected into context
// - For API key: API key identity, tenant ID, scopes are injected into context
type CombinedAuthMiddleware struct {
	jwtMiddleware    *JWTMiddleware
	apiKeyMiddleware *APIKeyMiddleware
	rpcValidator     *RPCAPIKeyValidator
	logger           *slog.Logger
}

// CombinedAuthConfig holds configuration for creating a CombinedAuthMiddleware.
type CombinedAuthConfig struct {
	// JWTValidator is the JWT token validator. Required for JWT authentication.
	JWTValidator JWTValidator

	// JWTConfig holds optional JWT middleware configuration (OIDC defaults).
	JWTConfig JWTMiddlewareConfig

	// APIKeyConfig is the configuration for API key authentication.
	// If APIKeys is nil or empty, legacy API key authentication is disabled.
	APIKeyConfig APIKeyConfig

	// RPCValidator is the optional RPC-based API key validator for pk_{slug}_{...} keys.
	// When set, prefixed API keys are validated via the Control Plane service.
	RPCValidator *RPCAPIKeyValidator

	// Logger for authentication events.
	Logger *slog.Logger
}

// NewCombinedAuthMiddleware creates a new combined authentication middleware.
// At least one of JWTValidator or APIKeyConfig.APIKeys must be configured.
func NewCombinedAuthMiddleware(config CombinedAuthConfig) (*CombinedAuthMiddleware, error) {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	var jwtMiddleware *JWTMiddleware
	var apiKeyMiddleware *APIKeyMiddleware

	// Set up JWT middleware if validator is provided
	if config.JWTValidator != nil {
		var err error
		jwtMiddleware, err = NewJWTMiddleware(config.JWTValidator, config.Logger, config.JWTConfig)
		if err != nil {
			return nil, err
		}
	}

	// Set up API key middleware if keys are configured
	if len(config.APIKeyConfig.APIKeys) > 0 {
		config.APIKeyConfig.Logger = config.Logger
		apiKeyMiddleware = NewAPIKeyMiddleware(config.APIKeyConfig)
	}

	return &CombinedAuthMiddleware{
		jwtMiddleware:    jwtMiddleware,
		apiKeyMiddleware: apiKeyMiddleware,
		rpcValidator:     config.RPCValidator,
		logger:           config.Logger,
	}, nil
}

// Close releases resources held by the middleware.
// Safe to call multiple times.
func (m *CombinedAuthMiddleware) Close() {
	if m.apiKeyMiddleware != nil {
		m.apiKeyMiddleware.Close()
	}
	if m.rpcValidator != nil {
		m.rpcValidator.Close()
	}
}

// Handler returns an http.Handler that performs combined authentication.
// It first checks for API key, then JWT. If neither is valid, returns 401.
func (m *CombinedAuthMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get(APIKeyHeader)

		// Try API key authentication first (if any API key mechanism is configured)
		if apiKey != "" {
			// Try RPC validation for pk_{slug}_{...} format keys
			if m.rpcValidator != nil && strings.HasPrefix(apiKey, "pk_") {
				m.handleRPCAPIKeyAuth(w, r, next, apiKey)
				return
			}

			// Fall back to legacy env-var-based API key validation
			if m.apiKeyMiddleware != nil {
				m.handleLegacyAPIKeyAuth(w, r, next)
				return
			}

			// API key provided but no validator configured
			m.logger.Debug("API key provided but no validator configured",
				slog.String("path", r.URL.Path),
			)
			writeUnauthorized(w, "invalid API key")
			return
		}

		// Try JWT authentication (if configured)
		if m.jwtMiddleware != nil && r.Header.Get("Authorization") != "" {
			m.jwtMiddleware.Handler(next).ServeHTTP(w, r)
			return
		}

		// No authentication credentials provided
		m.logger.Debug("no authentication credentials provided",
			slog.String("path", r.URL.Path),
		)
		writeUnauthorized(w, "missing authentication credentials")
	})
}

// handleRPCAPIKeyAuth validates a pk_{slug}_{...} API key via the Control Plane RPC.
func (m *CombinedAuthMiddleware) handleRPCAPIKeyAuth(w http.ResponseWriter, r *http.Request, next http.Handler, apiKey string) {
	result, err := m.rpcValidator.Validate(r.Context(), apiKey)
	if err != nil {
		m.logger.Warn("RPC API key validation error",
			slog.String("error", err.Error()),
			slog.String("path", r.URL.Path),
		)
		writeUnauthorized(w, "invalid API key")
		return
	}

	if result == nil || !result.Valid {
		m.logger.Debug("RPC API key validation failed",
			slog.String("path", r.URL.Path),
		)
		writeUnauthorized(w, "invalid API key")
		return
	}

	// Check per-key rate limit using the rate_limit_rps from the validation result
	_, keyPrefix, _ := ParsePrefixedKey(apiKey)
	if !m.rpcValidator.AllowRequest(keyPrefix, result.RateLimitRPS) {
		m.logger.Warn("rate limit exceeded",
			slog.String("identity", result.Identity),
			slog.String("path", r.URL.Path),
		)
		writeJSONError(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	// Inject identity, tenant, and scopes into context
	ctx := context.WithValue(r.Context(), APIKeyIdentityKey, result.Identity)
	if result.TenantID != "" {
		ctx = context.WithValue(ctx, TenantIDContextKey, result.TenantID)
		if tenantID, err := tenant.NewTenantID(result.TenantID); err == nil {
			ctx = tenant.WithTenant(ctx, tenantID)
		}
	}
	if len(result.Scopes) > 0 {
		ctx = context.WithValue(ctx, ScopesContextKey, result.Scopes)
	}

	next.ServeHTTP(w, r.WithContext(ctx))
}

// handleLegacyAPIKeyAuth handles legacy env-var-based API key authentication.
func (m *CombinedAuthMiddleware) handleLegacyAPIKeyAuth(w http.ResponseWriter, r *http.Request, next http.Handler) {
	wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})

	m.apiKeyMiddleware.Handler(wrapper).ServeHTTP(w, r)
}

// TenantAuthorizationMiddleware performs authorization checks to ensure
// the authenticated identity is authorized for the resolved tenant.
//
// This middleware should be placed AFTER both auth and tenant middlewares:
// auth → tenant → tenant_authorization → proxy
//
// Authorization rules:
//   - JWT: tenant ID in JWT claims must match resolved tenant ID (403 if mismatch)
//   - JWT with platform-admin/super-admin role and no tenant claim: bypass comparison,
//     allow access to any tenant resolved from subdomain (cross-tenant access is logged)
//   - API key: API keys are authorized for all tenants (service-to-service)
type TenantAuthorizationMiddleware struct {
	logger *slog.Logger
}

// NewTenantAuthorizationMiddleware creates a new tenant authorization middleware.
func NewTenantAuthorizationMiddleware(logger *slog.Logger) *TenantAuthorizationMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	return &TenantAuthorizationMiddleware{logger: logger}
}

// Handler returns an http.Handler that performs tenant authorization.
func (m *TenantAuthorizationMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Check if request was authenticated with API key
		if identity := GetAPIKeyIdentity(ctx); identity != "" {
			// API keys are authorized for all tenants (service-to-service)
			m.logger.Debug("API key authorized for tenant",
				slog.String("identity", identity),
				slog.String("path", r.URL.Path),
			)
			next.ServeHTTP(w, r)
			return
		}

		// Check JWT tenant claim against resolved tenant
		jwtTenantID, hasJWTTenant := GetTenantIDFromContext(ctx)
		if !hasJWTTenant {
			// No JWT tenant claim - this shouldn't happen if auth middleware ran
			m.logger.Warn("no tenant ID in JWT claims",
				slog.String("path", r.URL.Path),
			)
			writeForbidden(w, "missing tenant claim in token")
			return
		}

		// Get resolved tenant from context
		resolvedTenant, hasTenant := tenant.FromContext(ctx)
		if !hasTenant {
			// No resolved tenant - this shouldn't happen if tenant middleware ran
			m.logger.Warn("no resolved tenant in context",
				slog.String("path", r.URL.Path),
			)
			writeForbidden(w, "tenant context not resolved")
			return
		}

		// Platform-admin/super-admin bypass: when JWT has no tenant claim but has
		// elevated platform role, allow cross-tenant access via subdomain resolution.
		if jwtTenantID == "" {
			claims, hasClaims := GetClaimsFromContext(ctx)
			if hasClaims && hasPlatformAdminRole(claims) {
				m.logger.Info("platform admin cross-tenant access",
					slog.String("user_id", claims.UserID),
					slog.String("resolved_tenant", resolvedTenant.String()),
					slog.String("path", r.URL.Path),
				)
				next.ServeHTTP(w, r)
				return
			}
			// No tenant claim and not a platform admin
			writeForbidden(w, "missing tenant claim in token")
			return
		}

		// Compare JWT tenant with resolved tenant
		if !tenantsMatch(jwtTenantID, resolvedTenant) {
			m.logger.Warn("JWT tenant does not match resolved tenant",
				slog.String("jwt_tenant", jwtTenantID),
				slog.String("resolved_tenant", resolvedTenant.String()),
				slog.String("path", r.URL.Path),
			)
			writeForbidden(w, "not authorized for this tenant")
			return
		}

		m.logger.Debug("tenant authorization successful",
			slog.String("tenant", resolvedTenant.String()),
			slog.String("path", r.URL.Path),
		)
		next.ServeHTTP(w, r)
	})
}

// hasPlatformAdminRole returns true if claims contain platform-admin or super-admin role.
func hasPlatformAdminRole(claims *platformauth.Claims) bool {
	if claims == nil {
		return false
	}
	return claims.HasRole(platformauth.RolePlatformAdmin) || claims.HasRole(platformauth.RoleSuperAdmin)
}

// tenantsMatch compares JWT tenant ID (string) with resolved tenant ID.
// The comparison is case-insensitive to handle potential case differences.
func tenantsMatch(jwtTenantID string, resolvedTenant tenant.TenantID) bool {
	return strings.EqualFold(jwtTenantID, resolvedTenant.String())
}

// writeForbidden writes a 403 Forbidden response with the given message
// using proper JSON encoding to prevent injection attacks.
func writeForbidden(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

// JWTValidatorWithContext is an adapter that wraps JWTValidatorWithJWKS to implement JWTValidator.
// This is needed because JWTValidatorWithJWKS.ValidateToken requires a context parameter.
type JWTValidatorWithContext struct {
	validator *platformauth.JWTValidatorWithJWKS
}

// NewJWTValidatorWithContext creates a new adapter for JWTValidatorWithJWKS.
func NewJWTValidatorWithContext(validator *platformauth.JWTValidatorWithJWKS) *JWTValidatorWithContext {
	return &JWTValidatorWithContext{validator: validator}
}

// ValidateToken validates the JWT token using a background context.
// This is safe because the underlying JWKS fetch already has timeout handling.
func (v *JWTValidatorWithContext) ValidateToken(tokenString string) (*platformauth.Claims, error) {
	return v.validator.ValidateToken(context.Background(), tokenString)
}

// Close releases resources held by the validator.
func (v *JWTValidatorWithContext) Close() error {
	return v.validator.Close()
}
