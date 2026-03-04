package gateway

import (
	"net/http"
	"strings"

	"github.com/meridianhub/meridian/services/api-gateway/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// metadataPropagationMiddleware is an HTTP middleware for the Vanguard transcoder
// path that propagates identity context as lowercase gRPC metadata headers.
//
// It performs two operations in sequence:
//
//  1. Security: Strip any incoming identity headers to prevent client spoofing.
//     Clients must not be able to inject their own x-user-id, x-tenant-id,
//     x-auth-method, or x-auth-roles headers.
//
//  2. Propagation: Read the authenticated identity from the request context
//     (set by the auth middleware) and write it as lowercase HTTP headers.
//     These headers are forwarded by Vanguard to the gRPC backend, where the
//     gRPC server reads them as incoming metadata. The existing interceptor
//     chain (TenantExtractionInterceptor, auth.Interceptor) reads these keys:
//     - x-user-id  → identifies the authenticated principal
//     - x-tenant-id → identifies the tenant (read by TenantExtractionInterceptor)
//     - x-auth-method → "jwt" or "apikey"
//     - x-auth-roles → comma-separated role list (JWT only)
//
// This middleware is the Vanguard equivalent of the security logic in
// NewProxyHandler's Director function, ensuring that all backend paths
// (legacy proxy and Vanguard transcoder) apply the same security model.
func metadataPropagationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SECURITY: Strip all incoming identity headers (both canonical and
		// lowercase forms) to prevent client spoofing before writing new ones.
		r.Header.Del(HeaderUserID)
		r.Header.Del(HeaderTenantID)
		r.Header.Del(HeaderAuthMethod)
		r.Header.Del(HeaderAuthRoles)
		r.Header.Del(auth.APIKeyHeader)

		// Write authenticated identity as lowercase gRPC metadata headers.
		writeIdentityMetadata(r)

		next.ServeHTTP(w, r)
	})
}

// writeIdentityMetadata reads authenticated identity from the request context
// and writes it as lowercase HTTP headers compatible with gRPC metadata.
//
// JWT authentication sets:
//   - x-user-id     from JWT user_id claim
//   - x-tenant-id   from JWT tenant_id claim (if present)
//   - x-auth-method "jwt"
//   - x-auth-roles  comma-separated roles (if present and non-empty)
//
// API key authentication sets:
//   - x-user-id     from resolved API key identity
//   - x-tenant-id   from resolved tenant (if present)
//   - x-auth-method "apikey"
//
// If no authenticated identity is found in context, no headers are written.
func writeIdentityMetadata(req *http.Request) {
	ctx := req.Context()

	// Helper: set x-tenant-id from auth context or tenant resolver context.
	// Auth context (JWT/API key claims) takes precedence, but when the token
	// lacks a tenant claim (e.g. standard OIDC providers like Dex), fall back
	// to the tenant resolved by TenantResolverMiddleware via subdomain/header.
	setTenantHeader := func() {
		if tenantID, ok := auth.GetTenantIDFromContext(ctx); ok && tenantID != "" {
			req.Header.Set("x-tenant-id", tenantID)
			return
		}
		if tenantID, ok := tenant.FromContext(ctx); ok && !tenantID.IsEmpty() {
			req.Header.Set("x-tenant-id", string(tenantID))
		}
	}

	// JWT takes precedence over API key when both are present.
	if userID, ok := auth.GetUserIDFromContext(ctx); ok && userID != "" {
		req.Header.Set("x-user-id", userID)
		req.Header.Set("x-auth-method", AuthMethodJWT)
		setTenantHeader()

		if roles, ok := auth.GetRolesFromContext(ctx); ok && len(roles) > 0 {
			req.Header.Set("x-auth-roles", strings.Join(roles, ","))
		}

		return
	}

	// Fall back to API key identity.
	if identity := auth.GetAPIKeyIdentity(ctx); identity != "" {
		req.Header.Set("x-user-id", identity)
		req.Header.Set("x-auth-method", AuthMethodAPIKey)
		setTenantHeader()
		return
	}

	// Fall back to tenant resolver context. When auth is disabled
	// (AUTH_ENABLED=false), the tenant resolver middleware still injects the
	// tenant ID into the request context via tenant.WithTenant(). Propagate
	// it as a header so Vanguard forwards it as gRPC metadata.
	if tenantID, ok := tenant.FromContext(ctx); ok && !tenantID.IsEmpty() {
		req.Header.Set("x-tenant-id", string(tenantID))
	}
}
