package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meridianhub/meridian/services/api-gateway/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
)

// TestMetadataPropagationMiddleware_JWTAuthenticated verifies that JWT-authenticated
// requests propagate identity as lowercase gRPC metadata headers.
func TestMetadataPropagationMiddleware_JWTAuthenticated(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/meridian.party.v1.PartyService/CreateParty", nil)
	ctx := context.WithValue(req.Context(), auth.UserIDContextKey, "user-123")
	ctx = context.WithValue(ctx, auth.TenantIDContextKey, "tenant-abc")
	req = req.WithContext(ctx)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "user-123", capturedHeaders.Get("x-user-id"))
	assert.Equal(t, "tenant-abc", capturedHeaders.Get("x-tenant-id"))
	assert.Equal(t, AuthMethodJWT, capturedHeaders.Get("x-auth-method"))
}

// TestMetadataPropagationMiddleware_JWTWithRoles verifies that JWT roles are
// propagated as a comma-separated gRPC metadata header.
func TestMetadataPropagationMiddleware_JWTWithRoles(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	ctx := context.WithValue(req.Context(), auth.UserIDContextKey, "user-456")
	ctx = context.WithValue(ctx, auth.TenantIDContextKey, "tenant-xyz")
	ctx = context.WithValue(ctx, auth.RolesContextKey, []string{"admin", "operator"})
	req = req.WithContext(ctx)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "admin,operator", capturedHeaders.Get("x-auth-roles"))
}

// TestMetadataPropagationMiddleware_APIKeyAuthenticated verifies that API key
// authenticated requests propagate identity as lowercase gRPC metadata headers.
func TestMetadataPropagationMiddleware_APIKeyAuthenticated(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	ctx := context.WithValue(req.Context(), auth.APIKeyIdentityKey, "service-payment-processor")
	req = req.WithContext(ctx)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "service-payment-processor", capturedHeaders.Get("x-user-id"))
	assert.Equal(t, AuthMethodAPIKey, capturedHeaders.Get("x-auth-method"))
	assert.Empty(t, capturedHeaders.Get("x-tenant-id"), "API key without tenant should not set x-tenant-id")
}

// TestMetadataPropagationMiddleware_APIKeyWithTenant verifies that API keys
// with a resolved tenant ID propagate the tenant as gRPC metadata.
func TestMetadataPropagationMiddleware_APIKeyWithTenant(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	ctx := context.WithValue(req.Context(), auth.APIKeyIdentityKey, "service-a")
	ctx = context.WithValue(ctx, auth.TenantIDContextKey, "tenant-001")
	req = req.WithContext(ctx)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "service-a", capturedHeaders.Get("x-user-id"))
	assert.Equal(t, AuthMethodAPIKey, capturedHeaders.Get("x-auth-method"))
	assert.Equal(t, "tenant-001", capturedHeaders.Get("x-tenant-id"))
}

// TestMetadataPropagationMiddleware_SecurityStripsIncomingIdentityHeaders verifies
// that spoofed identity headers are stripped before adding authenticated ones.
func TestMetadataPropagationMiddleware_SecurityStripsIncomingIdentityHeaders(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	// Attacker sends spoofed identity headers
	req.Header.Set("x-user-id", "spoofed-admin")
	req.Header.Set("x-tenant-id", "spoofed-tenant")
	req.Header.Set("x-auth-method", "jwt")
	req.Header.Set("x-auth-roles", "superadmin,root")
	// No auth context — unauthenticated request

	handler.ServeHTTP(httptest.NewRecorder(), req)

	// All spoofed headers must be stripped
	assert.Empty(t, capturedHeaders.Get("x-user-id"))
	assert.Empty(t, capturedHeaders.Get("x-tenant-id"))
	assert.Empty(t, capturedHeaders.Get("x-auth-method"))
	assert.Empty(t, capturedHeaders.Get("x-auth-roles"))
}

// TestMetadataPropagationMiddleware_SecurityReplacesCanonicalSpoofedHeaders verifies
// that canonical-case spoofed headers (X-User-ID) are also stripped.
func TestMetadataPropagationMiddleware_SecurityReplacesCanonicalSpoofedHeaders(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	// Client sends canonical-case spoofed headers
	req.Header.Set(HeaderUserID, "spoofed-admin")
	req.Header.Set(HeaderTenantID, "spoofed-tenant")
	req.Header.Set(HeaderAuthMethod, "jwt")
	req.Header.Set(HeaderAuthRoles, "superadmin")

	// Real authenticated identity in context
	ctx := context.WithValue(req.Context(), auth.UserIDContextKey, "real-user-789")
	ctx = context.WithValue(ctx, auth.TenantIDContextKey, "real-tenant-def")
	req = req.WithContext(ctx)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Real identity replaces spoofed headers
	assert.Equal(t, "real-user-789", capturedHeaders.Get("x-user-id"))
	assert.Equal(t, "real-tenant-def", capturedHeaders.Get("x-tenant-id"))
	assert.Equal(t, AuthMethodJWT, capturedHeaders.Get("x-auth-method"))
	// No roles set in context → no x-auth-roles
	assert.Empty(t, capturedHeaders.Get("x-auth-roles"))
}

// TestMetadataPropagationMiddleware_SecurityStripsAPIKeyHeader verifies that
// the raw API key header is stripped before forwarding to the backend.
func TestMetadataPropagationMiddleware_SecurityStripsAPIKeyHeader(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.Header.Set(auth.APIKeyHeader, "secret-api-key-value")
	ctx := context.WithValue(req.Context(), auth.APIKeyIdentityKey, "service-b")
	req = req.WithContext(ctx)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Raw API key credential must not leak to backend
	assert.Empty(t, capturedHeaders.Get(auth.APIKeyHeader))
	// But resolved identity must be present
	assert.Equal(t, "service-b", capturedHeaders.Get("x-user-id"))
}

// TestMetadataPropagationMiddleware_Unauthenticated verifies that unauthenticated
// requests receive no identity metadata headers.
func TestMetadataPropagationMiddleware_Unauthenticated(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	// No auth context

	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Empty(t, capturedHeaders.Get("x-user-id"))
	assert.Empty(t, capturedHeaders.Get("x-tenant-id"))
	assert.Empty(t, capturedHeaders.Get("x-auth-method"))
	assert.Empty(t, capturedHeaders.Get("x-auth-roles"))
}

// TestMetadataPropagationMiddleware_EmptyRolesNotPropagated verifies that an
// empty roles slice does not result in an empty x-auth-roles metadata header.
func TestMetadataPropagationMiddleware_EmptyRolesNotPropagated(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	ctx := context.WithValue(req.Context(), auth.UserIDContextKey, "user-no-roles")
	ctx = context.WithValue(ctx, auth.TenantIDContextKey, "tenant-abc")
	ctx = context.WithValue(ctx, auth.RolesContextKey, []string{})
	req = req.WithContext(ctx)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "user-no-roles", capturedHeaders.Get("x-user-id"))
	assert.Empty(t, capturedHeaders.Get("x-auth-roles"), "empty roles slice should not set header")
}

// TestMetadataPropagationMiddleware_JWTPrioritizedOverAPIKey verifies that
// when both JWT and API key identities are present, JWT takes precedence.
func TestMetadataPropagationMiddleware_JWTPrioritizedOverAPIKey(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	ctx := context.WithValue(req.Context(), auth.UserIDContextKey, "jwt-user")
	ctx = context.WithValue(ctx, auth.TenantIDContextKey, "jwt-tenant")
	ctx = context.WithValue(ctx, auth.APIKeyIdentityKey, "apikey-service")
	req = req.WithContext(ctx)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "jwt-user", capturedHeaders.Get("x-user-id"))
	assert.Equal(t, AuthMethodJWT, capturedHeaders.Get("x-auth-method"))
	assert.Equal(t, "jwt-tenant", capturedHeaders.Get("x-tenant-id"))
}

// TestMetadataPropagationMiddleware_TenantResolverFallback verifies that when
// auth is disabled (no JWT or API key identity), the middleware falls back to
// tenant context set by the tenant resolver middleware.
func TestMetadataPropagationMiddleware_TenantResolverFallback(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/meridian.party.v1.PartyService/ListParties", nil)
	// No auth context (AUTH_ENABLED=false), but tenant resolver injected tenant
	ctx := tenant.WithTenant(req.Context(), tenant.TenantID("volterra_energy"))
	req = req.WithContext(ctx)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Tenant ID propagated from resolver context
	assert.Equal(t, "volterra_energy", capturedHeaders.Get("x-tenant-id"))
	// No auth identity headers
	assert.Empty(t, capturedHeaders.Get("x-user-id"))
	assert.Empty(t, capturedHeaders.Get("x-auth-method"))
}

// TestMetadataPropagationMiddleware_TenantResolverSpoofedHeaderStripped verifies
// that spoofed x-tenant-id headers are stripped even when tenant resolver context
// is used as fallback.
func TestMetadataPropagationMiddleware_TenantResolverSpoofedHeaderStripped(t *testing.T) {
	var capturedHeaders http.Header

	handler := metadataPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.Header.Set("x-tenant-id", "spoofed-tenant")
	// Tenant resolver set the real tenant
	ctx := tenant.WithTenant(req.Context(), tenant.TenantID("real_tenant"))
	req = req.WithContext(ctx)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Spoofed header replaced by resolver tenant
	assert.Equal(t, "real_tenant", capturedHeaders.Get("x-tenant-id"))
}
