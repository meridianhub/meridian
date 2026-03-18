package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
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

// generateTestRSAKeys generates a test RSA key pair for testing.
func generateTestRSAKeys() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate RSA keys: %w", err)
	}
	return privateKey, &privateKey.PublicKey, nil
}

// createTestToken creates a signed JWT token for testing.
func createTestToken(privateKey *rsa.PrivateKey, claims *platformauth.Claims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return tokenString, nil
}

// mockValidator implements JWTValidator for testing.
type mockValidator struct {
	claims *platformauth.Claims
	err    error
}

func (m *mockValidator) ValidateToken(_ string) (*platformauth.Claims, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.claims, nil
}

// testLogger returns a logger that discards output for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewJWTMiddleware(t *testing.T) {
	validator := &mockValidator{}
	logger := testLogger()

	t.Run("success with valid parameters", func(t *testing.T) {
		middleware, err := NewJWTMiddleware(validator, logger)

		assert.NoError(t, err)
		assert.NotNil(t, middleware)
	})

	t.Run("error with nil validator", func(t *testing.T) {
		middleware, err := NewJWTMiddleware(nil, logger)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrNilValidator)
		assert.Nil(t, middleware)
	})

	t.Run("error with nil logger", func(t *testing.T) {
		middleware, err := NewJWTMiddleware(validator, nil)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrNilLogger)
		assert.Nil(t, middleware)
	})
}

func TestJWTMiddleware_Handler_ValidToken(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-123",
		TenantID: "acme_bank",
		Roles:    []string{"admin", "user"},
		Scopes:   []string{"read", "write"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	validator := &mockValidator{claims: claims}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	// Create a handler that captures the context
	var capturedCtx context.Context
	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Verify response
	assert.Equal(t, http.StatusOK, rr.Code)

	// Verify claims were injected into context
	userID, ok := GetUserIDFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, "user-123", userID)

	tenantID, ok := GetTenantIDFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, "acme_bank", tenantID)

	roles, ok := GetRolesFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, []string{"admin", "user"}, roles)

	scopes, ok := GetScopesFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, []string{"read", "write"}, scopes)

	extractedClaims, ok := GetClaimsFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, claims, extractedClaims)

	// Verify tenant was also injected via tenant package
	tenantFromPkg, ok := tenant.FromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, tenant.TenantID("acme_bank"), tenantFromPkg)
}

func TestJWTMiddleware_Handler_MissingTenantID_PassesThrough(t *testing.T) {
	claims := &platformauth.Claims{
		UserID: "user-123",
		// TenantID intentionally absent — enforcement is in TenantAuthorizationMiddleware
		Roles: []string{"platform-admin"},
	}
	validator := &mockValidator{claims: claims}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	var called bool
	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.True(t, called, "next handler should be called — JWT middleware does not enforce tenant presence")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestJWTMiddleware_Handler_NilClaims(t *testing.T) {
	validator := &mockValidator{claims: nil, err: nil}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called for nil claims")
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "invalid token")
}

func TestJWTMiddleware_Handler_ExpiredToken(t *testing.T) {
	validator := &mockValidator{err: platformauth.ErrTokenExpired}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called")
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer expired-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "token expired")
	assert.Equal(t, `Bearer realm="api"`, rr.Header().Get("WWW-Authenticate"))
}

func TestJWTMiddleware_Handler_InvalidSignature(t *testing.T) {
	validator := &mockValidator{err: platformauth.ErrInvalidSignature}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called")
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-signature-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "invalid token signature")
}

func TestJWTMiddleware_Handler_InvalidToken(t *testing.T) {
	validator := &mockValidator{err: platformauth.ErrInvalidToken}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called")
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer malformed.token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "invalid token")
}

func TestJWTMiddleware_Handler_MissingAuthorizationHeader(t *testing.T) {
	validator := &mockValidator{}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called")
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	// No Authorization header set
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "missing authorization header")
}

func TestJWTMiddleware_Handler_MalformedBearerToken(t *testing.T) {
	validator := &mockValidator{}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called")
	})

	handler := middleware.Handler(nextHandler)

	testCases := []struct {
		name          string
		authHeader    string
		expectedError string
	}{
		{
			name:          "missing bearer prefix",
			authHeader:    "some-token-without-bearer",
			expectedError: "expected Bearer scheme",
		},
		{
			name:          "basic auth instead of bearer",
			authHeader:    "Basic dXNlcjpwYXNz",
			expectedError: "expected Bearer scheme, got Basic",
		},
		{
			name:          "empty token after bearer",
			authHeader:    "Bearer ",
			expectedError: "empty token",
		},
		{
			name:          "bearer with lowercase b",
			authHeader:    "bearer token123",
			expectedError: "Bearer scheme is case-sensitive",
		},
		{
			name:          "token with spaces",
			authHeader:    "Bearer token with spaces",
			expectedError: "malformed token",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			req.Header.Set("Authorization", tc.authHeader)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusUnauthorized, rr.Code)
			assert.Contains(t, rr.Body.String(), tc.expectedError)
		})
	}
}

// errUnexpectedDatabase is a static error for testing unexpected database errors.
var errUnexpectedDatabase = errors.New("unexpected database error")

func TestJWTMiddleware_Handler_UnexpectedValidationError(t *testing.T) {
	validator := &mockValidator{err: errUnexpectedDatabase}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called")
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "authentication failed")
}

func TestJWTMiddleware_IntegrationWithRealValidator(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := platformauth.NewJWTValidator(publicKey)
	require.NoError(t, err)

	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	t.Run("valid token sets context correctly", func(t *testing.T) {
		claims := &platformauth.Claims{
			UserID:   "user-456",
			TenantID: "test_tenant",
			Roles:    []string{"editor"},
			Scopes:   []string{"documents:read"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}

		tokenString, err := createTestToken(privateKey, claims)
		require.NoError(t, err)

		var capturedCtx context.Context
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
		})

		handler := middleware.Handler(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		userID, _ := GetUserIDFromContext(capturedCtx)
		assert.Equal(t, "user-456", userID)

		tenantID, _ := GetTenantIDFromContext(capturedCtx)
		assert.Equal(t, "test_tenant", tenantID)
	})

	t.Run("expired token returns 401", func(t *testing.T) {
		claims := &platformauth.Claims{
			UserID:   "user-456",
			TenantID: "test_tenant",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)), // Expired
			},
		}

		tokenString, err := createTestToken(privateKey, claims)
		require.NoError(t, err)

		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("next handler should not be called")
		})

		handler := middleware.Handler(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Body.String(), "token expired")
	})

	t.Run("invalid signature returns 401", func(t *testing.T) {
		// Create token with different key
		otherPrivateKey, _, err := generateTestRSAKeys()
		require.NoError(t, err)

		claims := &platformauth.Claims{
			UserID:   "user-456",
			TenantID: "test_tenant",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}

		tokenString, err := createTestToken(otherPrivateKey, claims)
		require.NoError(t, err)

		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("next handler should not be called")
		})

		handler := middleware.Handler(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Body.String(), "invalid token")
	})
}

func TestExtractBearerToken(t *testing.T) {
	t.Run("success with valid bearer token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9")

		token, err := extractBearerToken(req)

		assert.NoError(t, err)
		assert.Equal(t, "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9", token)
	})

	t.Run("error with missing header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		token, err := extractBearerToken(req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing authorization header")
		assert.Empty(t, token)
	})

	t.Run("error with wrong scheme", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

		token, err := extractBearerToken(req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected Bearer scheme, got Basic")
		assert.Empty(t, token)
	})

	t.Run("error with lowercase bearer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "bearer token123")

		token, err := extractBearerToken(req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "case-sensitive")
		assert.Empty(t, token)
	})

	t.Run("error with empty token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer ")

		token, err := extractBearerToken(req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "empty token")
		assert.Empty(t, token)
	})

	t.Run("error with malformed token containing spaces", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer token with spaces")

		token, err := extractBearerToken(req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "malformed token")
		assert.Empty(t, token)
	})

	t.Run("success with complex JWT token", func(t *testing.T) {
		// Real JWT structure: header.payload.signature (base64 encoded)
		complexToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoiMTIzIn0.signature"
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+complexToken)

		token, err := extractBearerToken(req)

		assert.NoError(t, err)
		assert.Equal(t, complexToken, token)
	})
}

func TestContextHelpers(t *testing.T) {
	t.Run("GetUserIDFromContext returns value when present", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), UserIDContextKey, "user-123")

		userID, ok := GetUserIDFromContext(ctx)

		assert.True(t, ok)
		assert.Equal(t, "user-123", userID)
	})

	t.Run("GetUserIDFromContext returns empty when not present", func(t *testing.T) {
		ctx := context.Background()

		userID, ok := GetUserIDFromContext(ctx)

		assert.False(t, ok)
		assert.Empty(t, userID)
	})

	t.Run("GetTenantIDFromContext returns value when present", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), TenantIDContextKey, "acme_bank")

		tenantID, ok := GetTenantIDFromContext(ctx)

		assert.True(t, ok)
		assert.Equal(t, "acme_bank", tenantID)
	})

	t.Run("GetTenantIDFromContext returns empty when not present", func(t *testing.T) {
		ctx := context.Background()

		tenantID, ok := GetTenantIDFromContext(ctx)

		assert.False(t, ok)
		assert.Empty(t, tenantID)
	})

	t.Run("GetRolesFromContext returns value when present", func(t *testing.T) {
		roles := []string{"admin", "user"}
		ctx := context.WithValue(context.Background(), RolesContextKey, roles)

		extractedRoles, ok := GetRolesFromContext(ctx)

		assert.True(t, ok)
		assert.Equal(t, roles, extractedRoles)
	})

	t.Run("GetRolesFromContext returns nil when not present", func(t *testing.T) {
		ctx := context.Background()

		roles, ok := GetRolesFromContext(ctx)

		assert.False(t, ok)
		assert.Nil(t, roles)
	})

	t.Run("GetScopesFromContext returns value when present", func(t *testing.T) {
		scopes := []string{"read", "write"}
		ctx := context.WithValue(context.Background(), ScopesContextKey, scopes)

		extractedScopes, ok := GetScopesFromContext(ctx)

		assert.True(t, ok)
		assert.Equal(t, scopes, extractedScopes)
	})

	t.Run("GetScopesFromContext returns nil when not present", func(t *testing.T) {
		ctx := context.Background()

		scopes, ok := GetScopesFromContext(ctx)

		assert.False(t, ok)
		assert.Nil(t, scopes)
	})

	t.Run("GetClaimsFromContext returns value when present", func(t *testing.T) {
		claims := &platformauth.Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		extractedClaims, ok := GetClaimsFromContext(ctx)

		assert.True(t, ok)
		assert.Equal(t, claims, extractedClaims)
	})

	t.Run("GetClaimsFromContext returns nil when not present", func(t *testing.T) {
		ctx := context.Background()

		claims, ok := GetClaimsFromContext(ctx)

		assert.False(t, ok)
		assert.Nil(t, claims)
	})
}

func TestInjectClaimsToContext(t *testing.T) {
	t.Run("injects all claims correctly", func(t *testing.T) {
		claims := &platformauth.Claims{
			UserID:   "user-789",
			TenantID: "test_org",
			Roles:    []string{"viewer", "editor"},
			Scopes:   []string{"api:read", "api:write"},
		}

		ctx := injectClaimsToContext(context.Background(), claims)

		userID, _ := GetUserIDFromContext(ctx)
		assert.Equal(t, "user-789", userID)

		tenantID, _ := GetTenantIDFromContext(ctx)
		assert.Equal(t, "test_org", tenantID)

		roles, _ := GetRolesFromContext(ctx)
		assert.Equal(t, []string{"viewer", "editor"}, roles)

		scopes, _ := GetScopesFromContext(ctx)
		assert.Equal(t, []string{"api:read", "api:write"}, scopes)

		extractedClaims, _ := GetClaimsFromContext(ctx)
		assert.Equal(t, claims, extractedClaims)

		// Verify tenant package integration
		tenantFromPkg, ok := tenant.FromContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("test_org"), tenantFromPkg)
	})

	t.Run("handles empty tenant ID", func(t *testing.T) {
		claims := &platformauth.Claims{
			UserID:   "user-789",
			TenantID: "", // Empty tenant
			Roles:    []string{"admin"},
		}

		ctx := injectClaimsToContext(context.Background(), claims)

		// Tenant ID should be empty string in context
		tenantID, ok := GetTenantIDFromContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, "", tenantID)

		// Tenant package should not have tenant
		_, ok = tenant.FromContext(ctx)
		assert.False(t, ok)
	})

	t.Run("handles nil roles and scopes", func(t *testing.T) {
		claims := &platformauth.Claims{
			UserID:   "user-789",
			TenantID: "test_org",
			Roles:    nil,
			Scopes:   nil,
		}

		ctx := injectClaimsToContext(context.Background(), claims)

		// GetRoles and GetScopes return empty slices for nil
		roles, ok := GetRolesFromContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, []string{}, roles)

		scopes, ok := GetScopesFromContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, []string{}, scopes)
	})
}

func TestWriteUnauthorized(t *testing.T) {
	t.Run("sets correct headers and status", func(t *testing.T) {
		rr := httptest.NewRecorder()

		writeUnauthorized(rr, "test error message")

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Equal(t, "application/json; charset=utf-8", rr.Header().Get("Content-Type"))
		assert.Equal(t, `Bearer realm="api"`, rr.Header().Get("WWW-Authenticate"))
		assert.Contains(t, rr.Body.String(), "test error message")
	})

	t.Run("returns valid JSON", func(t *testing.T) {
		rr := httptest.NewRecorder()

		writeUnauthorized(rr, "token expired")

		// json.Encoder.Encode adds a trailing newline
		assert.Equal(t, "{\"error\":\"token expired\"}\n", rr.Body.String())
	})
}

func TestJWTMiddleware_Handler_EmptyTokenStringError(t *testing.T) {
	validator := &mockValidator{err: platformauth.ErrTokenStringEmpty}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called")
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "invalid token")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

// BenchmarkJWTMiddleware_Authentication benchmarks JWT authentication performance.
// Measures the overhead of the JWT middleware in the request path.
func BenchmarkJWTMiddleware_Authentication(b *testing.B) {
	claims := &platformauth.Claims{
		UserID:   "bench-user",
		TenantID: "bench-tenant",
		Roles:    []string{"admin", "user"},
		Scopes:   []string{"read", "write"},
	}

	validator := &mockValidator{claims: claims}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	middleware, err := NewJWTMiddleware(validator, logger)
	if err != nil {
		b.Fatalf("failed to create middleware: %v", err)
	}

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer valid-jwt-token")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

// BenchmarkJWTMiddleware_TokenExtraction benchmarks just the token extraction step.
func BenchmarkJWTMiddleware_TokenExtraction(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoiMTIzIn0.signature")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = extractBearerToken(req)
	}
}

// BenchmarkJWTMiddleware_ContextInjection benchmarks the context injection step.
func BenchmarkJWTMiddleware_ContextInjection(b *testing.B) {
	claims := &platformauth.Claims{
		UserID:   "bench-user",
		TenantID: "bench-tenant",
		Roles:    []string{"admin", "user", "operator"},
		Scopes:   []string{"read", "write", "delete"},
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = injectClaimsToContext(ctx, claims)
	}
}

// BenchmarkJWTMiddleware_Parallel benchmarks JWT authentication under concurrent load.
func BenchmarkJWTMiddleware_Parallel(b *testing.B) {
	claims := &platformauth.Claims{
		UserID:   "bench-user",
		TenantID: "bench-tenant",
		Roles:    []string{"admin"},
	}

	validator := &mockValidator{claims: claims}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	middleware, err := NewJWTMiddleware(validator, logger)
	if err != nil {
		b.Fatalf("failed to create middleware: %v", err)
	}

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			req.Header.Set("Authorization", "Bearer valid-jwt-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}
	})
}

// =============================================================================
// Performance and Security Tests
// =============================================================================

// TestJWTMiddleware_Security_ExpiredTokenRejectedQuickly verifies that expired
// tokens are rejected quickly (within 10ms). This ensures fast-fail behavior
// without being so tight that CI runners with variable performance fail.
func TestJWTMiddleware_Security_ExpiredTokenRejectedQuickly(t *testing.T) {
	validator := &mockValidator{err: platformauth.ErrTokenExpired}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called")
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer expired-token")
	rr := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "token expired")
	// Expiration check should complete quickly (10ms allows for CI variability)
	assert.Less(t, elapsed, 10*time.Millisecond, "expired token rejection should take less than 10ms")
}

// TestJWTMiddleware_Security_InvalidSignatureRejectedQuickly verifies that tokens
// with invalid signatures are rejected quickly.
func TestJWTMiddleware_Security_InvalidSignatureRejectedQuickly(t *testing.T) {
	validator := &mockValidator{err: platformauth.ErrInvalidSignature}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called")
	})

	handler := middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-signature-token")
	rr := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "invalid token signature")
	// Signature validation should complete quickly (10ms allows for CI variability)
	assert.Less(t, elapsed, 10*time.Millisecond, "invalid signature rejection should take less than 10ms")
}

// TestJWTMiddleware_Performance_P99LatencyUnder5ms verifies that the auth
// middleware adds less than 5ms p99 latency under typical load.
func TestJWTMiddleware_Performance_P99LatencyUnder5ms(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	claims := &platformauth.Claims{
		UserID:   "perf-user",
		TenantID: "perf-tenant",
		Roles:    []string{"admin", "user"},
		Scopes:   []string{"read", "write"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	validator := &mockValidator{claims: claims}
	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Run 1000 requests and collect latencies
	const numRequests = 1000
	latencies := make([]time.Duration, numRequests)

	for i := 0; i < numRequests; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer valid-jwt-token")
		rr := httptest.NewRecorder()

		start := time.Now()
		handler.ServeHTTP(rr, req)
		latencies[i] = time.Since(start)
	}

	// Calculate p99 (99th percentile)
	// Sort latencies and take the 99th percentile
	sortedLatencies := make([]time.Duration, numRequests)
	copy(sortedLatencies, latencies)
	sortDurations(sortedLatencies)

	p99Index := int(float64(numRequests) * 0.99)
	p99Latency := sortedLatencies[p99Index]

	t.Logf("P50 latency: %v", sortedLatencies[numRequests/2])
	t.Logf("P99 latency: %v", p99Latency)

	// P99 should be under 5ms (using mock validator, so no network delay)
	assert.Less(t, p99Latency, 5*time.Millisecond,
		"P99 latency should be under 5ms, got %v", p99Latency)
}

// sortDurations sorts a slice of durations in ascending order.
func sortDurations(durations []time.Duration) {
	for i := 0; i < len(durations); i++ {
		for j := i + 1; j < len(durations); j++ {
			if durations[j] < durations[i] {
				durations[i], durations[j] = durations[j], durations[i]
			}
		}
	}
}

// TestJWTMiddleware_IntegrationWithRealToken_ExpirationBoundary tests that
// a token is correctly rejected exactly at the expiration boundary.
func TestJWTMiddleware_IntegrationWithRealToken_ExpirationBoundary(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := platformauth.NewJWTValidator(publicKey)
	require.NoError(t, err)

	logger := testLogger()

	middleware, err := NewJWTMiddleware(validator, logger)
	require.NoError(t, err)

	// Create token that expires 2s from now (enough time for key gen overhead in CI)
	expiresAt := time.Now().Add(2 * time.Second)
	claims := &platformauth.Claims{
		UserID:   "boundary-user",
		TenantID: "boundary-tenant",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	tokenString, err := createTestToken(privateKey, claims)
	require.NoError(t, err)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.Handler(nextHandler)

	// Token should be valid initially (before expiration)
	t.Run("token valid before expiration", func(t *testing.T) {
		// Make sure we're still before expiration
		if time.Now().After(expiresAt) {
			t.Skip("test took too long, token already expired")
		}

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	// Intentional sleep: Wait for token to actually expire to test expiration behavior.
	// This is testing real-time token expiration, not waiting for async operations.
	waitTime := time.Until(expiresAt) + 100*time.Millisecond
	if waitTime > 0 {
		time.Sleep(waitTime) //nolint:forbidigo // waits for real JWT token expiration
	}

	// Token should be rejected after expiration
	t.Run("token rejected after expiration", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Body.String(), "token expired")
	})
}
