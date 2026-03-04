package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCombinedAuthMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("success with JWT validator only", func(t *testing.T) {
		validator := &mockValidator{claims: &platformauth.Claims{UserID: "test"}}
		config := CombinedAuthConfig{
			JWTValidator: validator,
			Logger:       logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)

		assert.NoError(t, err)
		assert.NotNil(t, middleware)
		assert.NotNil(t, middleware.jwtMiddleware)
		assert.Nil(t, middleware.apiKeyMiddleware)
	})

	t.Run("success with API key config only", func(t *testing.T) {
		config := CombinedAuthConfig{
			APIKeyConfig: APIKeyConfig{
				APIKeys: map[string]string{"test-key": "test-identity"},
			},
			Logger: logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)

		assert.NoError(t, err)
		assert.NotNil(t, middleware)
		assert.Nil(t, middleware.jwtMiddleware)
		assert.NotNil(t, middleware.apiKeyMiddleware)

		// Clean up
		middleware.Close()
	})

	t.Run("success with both JWT and API key", func(t *testing.T) {
		validator := &mockValidator{claims: &platformauth.Claims{UserID: "test"}}
		config := CombinedAuthConfig{
			JWTValidator: validator,
			APIKeyConfig: APIKeyConfig{
				APIKeys: map[string]string{"test-key": "test-identity"},
			},
			Logger: logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)

		assert.NoError(t, err)
		assert.NotNil(t, middleware)
		assert.NotNil(t, middleware.jwtMiddleware)
		assert.NotNil(t, middleware.apiKeyMiddleware)

		// Clean up
		middleware.Close()
	})

	t.Run("success with default logger", func(t *testing.T) {
		validator := &mockValidator{claims: &platformauth.Claims{UserID: "test"}}
		config := CombinedAuthConfig{
			JWTValidator: validator,
			// No logger provided
		}

		middleware, err := NewCombinedAuthMiddleware(config)

		assert.NoError(t, err)
		assert.NotNil(t, middleware)
	})
}

func TestCombinedAuthMiddleware_Handler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("API key auth takes precedence when header present", func(t *testing.T) {
		validator := &mockValidator{claims: &platformauth.Claims{UserID: "jwt-user", TenantID: "jwt-tenant"}}
		config := CombinedAuthConfig{
			JWTValidator: validator,
			APIKeyConfig: APIKeyConfig{
				APIKeys: map[string]string{"valid-key": "service-a"},
			},
			Logger: logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)
		defer middleware.Close()

		var capturedCtx context.Context
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
		})

		handler := middleware.Handler(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("X-API-Key", "valid-key")
		req.Header.Set("Authorization", "Bearer valid-jwt") // Also set JWT, but API key should win
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		// Verify API key identity is in context, not JWT claims
		identity := GetAPIKeyIdentity(capturedCtx)
		assert.Equal(t, "service-a", identity)
	})

	t.Run("JWT auth used when no API key header", func(t *testing.T) {
		claims := &platformauth.Claims{
			UserID:   "jwt-user-123",
			TenantID: "acme_corp",
			Roles:    []string{"admin"},
		}
		validator := &mockValidator{claims: claims}
		config := CombinedAuthConfig{
			JWTValidator: validator,
			APIKeyConfig: APIKeyConfig{
				APIKeys: map[string]string{"valid-key": "service-a"},
			},
			Logger: logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)
		defer middleware.Close()

		var capturedCtx context.Context
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
		})

		handler := middleware.Handler(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer valid-jwt")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		// Verify JWT claims are in context
		userID, ok := GetUserIDFromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, "jwt-user-123", userID)

		tenantID, ok := GetTenantIDFromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, "acme_corp", tenantID)
	})

	t.Run("401 when no auth credentials provided", func(t *testing.T) {
		claims := &platformauth.Claims{UserID: "jwt-user"}
		validator := &mockValidator{claims: claims}
		config := CombinedAuthConfig{
			JWTValidator: validator,
			APIKeyConfig: APIKeyConfig{
				APIKeys: map[string]string{"valid-key": "service-a"},
			},
			Logger: logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)
		defer middleware.Close()

		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("next handler should not be called")
		})

		handler := middleware.Handler(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		// No auth headers
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Body.String(), "missing authentication credentials")
	})

	t.Run("401 when API key is invalid", func(t *testing.T) {
		config := CombinedAuthConfig{
			APIKeyConfig: APIKeyConfig{
				APIKeys: map[string]string{"valid-key": "service-a"},
			},
			Logger: logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)
		defer middleware.Close()

		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("next handler should not be called")
		})

		handler := middleware.Handler(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("X-API-Key", "invalid-key")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Body.String(), "invalid API key")
	})

	t.Run("401 when JWT is invalid", func(t *testing.T) {
		validator := &mockValidator{err: platformauth.ErrInvalidToken}
		config := CombinedAuthConfig{
			JWTValidator: validator,
			Logger:       logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)

		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("next handler should not be called")
		})

		handler := middleware.Handler(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer invalid-jwt")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

func TestTenantAuthorizationMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("API key bypasses tenant authorization", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		var nextCalled bool
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		handler := middleware.Handler(nextHandler)

		// Create context with API key identity and a tenant
		ctx := context.WithValue(context.Background(), APIKeyIdentityKey, "service-a")
		ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("some_tenant"))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.True(t, nextCalled)
	})

	t.Run("JWT with matching tenant passes", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		var nextCalled bool
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		handler := middleware.Handler(nextHandler)

		// Create context with matching JWT tenant and resolved tenant
		ctx := context.WithValue(context.Background(), TenantIDContextKey, "acme_corp")
		ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("acme_corp"))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.True(t, nextCalled)
	})

	t.Run("JWT with matching tenant case-insensitive", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		var nextCalled bool
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		handler := middleware.Handler(nextHandler)

		// Create context with different case JWT tenant
		ctx := context.WithValue(context.Background(), TenantIDContextKey, "ACME_CORP")
		ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("acme_corp"))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.True(t, nextCalled)
	})

	t.Run("403 when JWT tenant does not match resolved tenant", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("next handler should not be called")
		})

		handler := middleware.Handler(nextHandler)

		// Create context with mismatching JWT tenant and resolved tenant
		ctx := context.WithValue(context.Background(), TenantIDContextKey, "acme_corp")
		ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("other_corp"))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Contains(t, rr.Body.String(), "not authorized for this tenant")
	})

	t.Run("OIDC token without tenant claim uses resolved tenant from subdomain", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		var capturedCtx context.Context
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
		})

		handler := middleware.Handler(nextHandler)

		// Simulate OIDC token (e.g. Dex) with no tenant claim but resolved tenant from subdomain
		claims := &platformauth.Claims{
			UserID: "oidc-user",
			RegisteredClaims: jwt.RegisteredClaims{
				Subject: "dex-subject-id",
			},
		}
		ctx := injectClaimsToContext(context.Background(), claims)
		ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("volterra"))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		resolvedTenant, hasTenant := tenant.FromContext(capturedCtx)
		assert.True(t, hasTenant)
		assert.Equal(t, "volterra", resolvedTenant.String())
	})

	t.Run("403 when no JWT tenant claim and no resolved tenant", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("next handler should not be called")
		})

		handler := middleware.Handler(nextHandler)

		// JWT has no tenant claim and no tenant resolved from subdomain
		claims := &platformauth.Claims{
			UserID: "oidc-user",
		}
		ctx := injectClaimsToContext(context.Background(), claims)

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Contains(t, rr.Body.String(), "missing tenant claim")
	})

	t.Run("platform path allowed without tenant claim for authenticated user", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		var nextCalled bool
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		handler := middleware.Handler(nextHandler)

		// OIDC token with no tenant claim, no resolved tenant, but platform path
		claims := &platformauth.Claims{
			UserID: "oidc-user",
		}
		ctx := injectClaimsToContext(context.Background(), claims)

		req := httptest.NewRequest(http.MethodGet, "/v1/tenants", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.True(t, nextCalled, "platform path should be accessible without tenant claim")
	})

	t.Run("platform path allowed for Connect/gRPC path", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		var nextCalled bool
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		handler := middleware.Handler(nextHandler)

		claims := &platformauth.Claims{
			UserID: "oidc-user",
		}
		ctx := injectClaimsToContext(context.Background(), claims)

		req := httptest.NewRequest(http.MethodPost, "/meridian.tenant.v1.TenantService/ListTenants", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.True(t, nextCalled)
	})

	t.Run("403 when no resolved tenant in context", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("next handler should not be called")
		})

		handler := middleware.Handler(nextHandler)

		// Create context with JWT tenant but no resolved tenant
		ctx := context.WithValue(context.Background(), TenantIDContextKey, "acme_corp")

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Contains(t, rr.Body.String(), "tenant context not resolved")
	})

	t.Run("platform-admin without tenant claim accesses tenant via subdomain", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		var capturedCtx context.Context
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
		})

		handler := middleware.Handler(nextHandler)

		// JWT has platform-admin role but no tenant ID claim
		claims := &platformauth.Claims{
			UserID: "admin-user",
			Roles:  []string{"platform-admin"},
		}
		ctx := injectClaimsToContext(context.Background(), claims)
		// Tenant middleware resolved the tenant from subdomain
		ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("acme_corp"))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		// Resolved tenant must still be in context for downstream handlers
		resolvedTenant, hasTenant := tenant.FromContext(capturedCtx)
		assert.True(t, hasTenant)
		assert.Equal(t, "acme_corp", resolvedTenant.String())
	})

	t.Run("super-admin without tenant claim accesses tenant via subdomain", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		var nextCalled bool
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		handler := middleware.Handler(nextHandler)

		// JWT has super-admin role but no tenant ID claim
		claims := &platformauth.Claims{
			UserID: "super-user",
			Roles:  []string{"super-admin"},
		}
		ctx := injectClaimsToContext(context.Background(), claims)
		ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("some_corp"))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.True(t, nextCalled)
	})

	t.Run("regular tenant admin denied cross-tenant access", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("next handler should not be called")
		})

		handler := middleware.Handler(nextHandler)

		// JWT has admin role scoped to acme_corp but subdomain resolves other_corp
		claims := &platformauth.Claims{
			UserID:   "tenant-admin",
			TenantID: "acme_corp",
			Roles:    []string{"admin"},
		}
		ctx := injectClaimsToContext(context.Background(), claims)
		ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("other_corp"))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Contains(t, rr.Body.String(), "not authorized for this tenant")
	})

	t.Run("platform-admin with tenant claim uses normal tenant matching", func(t *testing.T) {
		middleware := NewTenantAuthorizationMiddleware(logger)

		var nextCalled bool
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		handler := middleware.Handler(nextHandler)

		// JWT has platform-admin role AND a tenant ID claim - treated as normal tenant matching
		claims := &platformauth.Claims{
			UserID:   "admin-user",
			TenantID: "acme_corp",
			Roles:    []string{"platform-admin"},
		}
		ctx := injectClaimsToContext(context.Background(), claims)
		ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("acme_corp"))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.True(t, nextCalled)
	})
}

func TestHasPlatformAdminRole(t *testing.T) {
	tests := []struct {
		name     string
		claims   *platformauth.Claims
		expected bool
	}{
		{
			name:     "platform-admin role returns true",
			claims:   &platformauth.Claims{Roles: []string{"platform-admin"}},
			expected: true,
		},
		{
			name:     "super-admin role returns true",
			claims:   &platformauth.Claims{Roles: []string{"super-admin"}},
			expected: true,
		},
		{
			name:     "both roles returns true",
			claims:   &platformauth.Claims{Roles: []string{"platform-admin", "super-admin"}},
			expected: true,
		},
		{
			name:     "admin role returns false",
			claims:   &platformauth.Claims{Roles: []string{"admin"}},
			expected: false,
		},
		{
			name:     "no roles returns false",
			claims:   &platformauth.Claims{Roles: []string{}},
			expected: false,
		},
		{
			name:     "nil claims returns false",
			claims:   nil,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := hasPlatformAdminRole(tc.claims)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestMiddlewareChainOrder(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// This test verifies the correct ordering of middleware execution:
	// auth → tenant → tenant_authz → handler
	//
	// The test uses a mock tenant middleware to simulate tenant resolution.

	t.Run("middleware executes in correct order", func(t *testing.T) {
		var executionOrder []string

		// Mock JWT validator
		claims := &platformauth.Claims{
			UserID:   "user-123",
			TenantID: "acme_corp",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		validator := &mockValidator{claims: claims}

		// Create combined auth middleware
		authConfig := CombinedAuthConfig{
			JWTValidator: validator,
			Logger:       logger,
		}
		authMiddleware, err := NewCombinedAuthMiddleware(authConfig)
		require.NoError(t, err)

		// Create tenant authorization middleware
		tenantAuthz := NewTenantAuthorizationMiddleware(logger)

		// Mock tenant middleware that records execution and sets tenant
		mockTenantMiddleware := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				executionOrder = append(executionOrder, "tenant")
				// Set tenant in context
				ctx := tenant.WithTenant(r.Context(), tenant.MustNewTenantID("acme_corp"))
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		}

		// Final handler
		finalHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			executionOrder = append(executionOrder, "handler")
		})

		// Build chain: auth → tenant → tenant_authz → handler
		// Note: Using http.Handler interface throughout
		var handler http.Handler = finalHandler
		handler = tenantAuthz.Handler(handler)
		handler = mockTenantMiddleware(handler)

		// Wrap auth to record execution order
		authWrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "auth")
			authMiddleware.Handler(handler).ServeHTTP(w, r)
		})

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		rr := httptest.NewRecorder()

		authWrapper.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, []string{"auth", "tenant", "handler"}, executionOrder)
	})

	t.Run("auth failure stops chain before tenant middleware", func(t *testing.T) {
		var executionOrder []string

		// Mock JWT validator that returns error
		validator := &mockValidator{err: platformauth.ErrInvalidToken}

		// Create combined auth middleware
		authConfig := CombinedAuthConfig{
			JWTValidator: validator,
			Logger:       logger,
		}
		authMiddleware, err := NewCombinedAuthMiddleware(authConfig)
		require.NoError(t, err)

		// Mock tenant middleware
		mockTenantMiddleware := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				executionOrder = append(executionOrder, "tenant")
				next.ServeHTTP(w, r)
			})
		}

		// Final handler
		finalHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			executionOrder = append(executionOrder, "handler")
		})

		// Build chain using http.Handler interface
		var handler http.Handler = finalHandler
		handler = mockTenantMiddleware(handler)
		handler = authMiddleware.Handler(handler)

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Empty(t, executionOrder, "no middleware after auth should execute on auth failure")
	})
}

func TestWriteForbidden(t *testing.T) {
	t.Run("sets correct headers and status", func(t *testing.T) {
		rr := httptest.NewRecorder()

		writeForbidden(rr, "test error message")

		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Equal(t, "application/json; charset=utf-8", rr.Header().Get("Content-Type"))
		assert.Contains(t, rr.Body.String(), "test error message")
	})

	t.Run("returns valid JSON", func(t *testing.T) {
		rr := httptest.NewRecorder()

		writeForbidden(rr, "access denied")

		// json.Encoder.Encode adds a trailing newline
		assert.Equal(t, "{\"error\":\"access denied\"}\n", rr.Body.String())
	})
}

func TestTenantsMatch(t *testing.T) {
	tests := []struct {
		name           string
		jwtTenant      string
		resolvedTenant tenant.TenantID
		expected       bool
	}{
		{
			name:           "exact match",
			jwtTenant:      "acme_corp",
			resolvedTenant: tenant.MustNewTenantID("acme_corp"),
			expected:       true,
		},
		{
			name:           "case insensitive match - uppercase JWT",
			jwtTenant:      "ACME_CORP",
			resolvedTenant: tenant.MustNewTenantID("acme_corp"),
			expected:       true,
		},
		{
			name:           "case insensitive match - lowercase JWT",
			jwtTenant:      "acme_corp",
			resolvedTenant: tenant.MustNewTenantID("ACME_CORP"),
			expected:       true,
		},
		{
			name:           "no match",
			jwtTenant:      "acme_corp",
			resolvedTenant: tenant.MustNewTenantID("other_corp"),
			expected:       false,
		},
		{
			name:           "empty JWT tenant",
			jwtTenant:      "",
			resolvedTenant: tenant.MustNewTenantID("acme_corp"),
			expected:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tenantsMatch(tc.jwtTenant, tc.resolvedTenant)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestCombinedAuthMiddleware_Close(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("close is safe to call multiple times", func(t *testing.T) {
		config := CombinedAuthConfig{
			APIKeyConfig: APIKeyConfig{
				APIKeys: map[string]string{"key": "identity"},
			},
			Logger: logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)

		// Should not panic on multiple closes
		middleware.Close()
		middleware.Close()
		middleware.Close()
	})

	t.Run("close is safe when no API key middleware", func(t *testing.T) {
		validator := &mockValidator{claims: &platformauth.Claims{UserID: "test"}}
		config := CombinedAuthConfig{
			JWTValidator: validator,
			Logger:       logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)

		// Should not panic
		middleware.Close()
	})
}

// =============================================================================
// Benchmark Tests
// =============================================================================

// BenchmarkCombinedAuthMiddleware_JWT benchmarks combined auth with JWT authentication.
func BenchmarkCombinedAuthMiddleware_JWT(b *testing.B) {
	claims := &platformauth.Claims{
		UserID:   "bench-user",
		TenantID: "bench-tenant",
		Roles:    []string{"admin"},
	}
	validator := &mockValidator{claims: claims}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	config := CombinedAuthConfig{
		JWTValidator: validator,
		APIKeyConfig: APIKeyConfig{
			APIKeys: map[string]string{"bench-key": "bench-service"},
		},
		Logger: logger,
	}

	middleware, err := NewCombinedAuthMiddleware(config)
	if err != nil {
		b.Fatalf("failed to create middleware: %v", err)
	}
	defer middleware.Close()

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer valid-jwt")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

// BenchmarkCombinedAuthMiddleware_APIKey benchmarks combined auth with API key authentication.
func BenchmarkCombinedAuthMiddleware_APIKey(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	config := CombinedAuthConfig{
		APIKeyConfig: APIKeyConfig{
			APIKeys:            map[string]string{"bench-key": "bench-service"},
			RateLimitPerSecond: 1000000, // Very high to not interfere
			RateLimitBurst:     1000000,
		},
		Logger: logger,
	}

	middleware, err := NewCombinedAuthMiddleware(config)
	if err != nil {
		b.Fatalf("failed to create middleware: %v", err)
	}
	defer middleware.Close()

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "bench-key")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

// BenchmarkTenantAuthorizationMiddleware benchmarks tenant authorization checks.
func BenchmarkTenantAuthorizationMiddleware(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	middleware := NewTenantAuthorizationMiddleware(logger)

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create context with matching JWT tenant and resolved tenant
		ctx := context.WithValue(context.Background(), TenantIDContextKey, "acme_corp")
		ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("acme_corp"))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
	}
}
