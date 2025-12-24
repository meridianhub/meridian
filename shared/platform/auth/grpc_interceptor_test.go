package auth

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// mockUnaryHandler is a test handler for unary RPCs
func mockUnaryHandler(ctx context.Context, _ interface{}) (interface{}, error) {
	// Return the context so we can verify claims were injected
	return ctx, nil
}

// mockStreamHandler is a test handler for streaming RPCs
func mockStreamHandler(_ interface{}, stream grpc.ServerStream) error {
	// Verify claims are in the stream context
	ctx := stream.Context()
	if _, ok := GetUserIDFromContext(ctx); !ok {
		return fmt.Errorf("missing user ID in context: %w", status.Error(codes.Internal, "missing user ID in context"))
	}
	return nil
}

// mockServerStream implements grpc.ServerStream for testing
type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func TestNewAuthInterceptor(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("success with standard validator", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator:     validator,
			BypassMethods: []string{"/health"},
		}

		interceptor, err := NewAuthInterceptor(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, interceptor)
		assert.False(t, interceptor.useJWKS)
		assert.True(t, interceptor.bypassMethods["/health"])
	})

	t.Run("success with JWKS validator", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		jwksProvider, err := NewJWKSProvider(context.Background(), &JWKSProviderConfig{
			URL:    server.URL,
			Client: http.DefaultClient,
		})
		require.NoError(t, err)
		defer func() { _ = jwksProvider.Close() }()

		jwksValidator, err := NewJWTValidatorWithJWKS(jwksProvider)
		require.NoError(t, err)

		cfg := &InterceptorConfig{
			JWKSValidator: jwksValidator,
		}

		interceptor, err := NewAuthInterceptor(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, interceptor)
		assert.True(t, interceptor.useJWKS)
	})

	t.Run("error with nil validators", func(t *testing.T) {
		cfg := &InterceptorConfig{}

		interceptor, err := NewAuthInterceptor(cfg)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrValidatorNil)
		assert.Nil(t, interceptor)
	})

	t.Run("bypass methods map created correctly", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
			BypassMethods: []string{
				"/grpc.health.v1.Health/Check",
				"/grpc.health.v1.Health/Watch",
			},
		}

		interceptor, err := NewAuthInterceptor(cfg)

		assert.NoError(t, err)
		assert.True(t, interceptor.bypassMethods["/grpc.health.v1.Health/Check"])
		assert.True(t, interceptor.bypassMethods["/grpc.health.v1.Health/Watch"])
		assert.False(t, interceptor.bypassMethods["/other.Service/Method"])
	})

	_ = privateKey // Suppress unused warning
}

func TestUnaryInterceptor(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("success with valid token", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create valid token with tenant_id (always required)
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			Scopes:   []string{"read", "write"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		// Create context with metadata
		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		// Call interceptor
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify claims were injected into context
		resultCtx := resp.(context.Context)
		userID, ok := GetUserIDFromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, "user-123", userID)

		roles, ok := GetRolesFromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, []string{"admin"}, roles)

		scopes, ok := GetScopesFromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, []string{"read", "write"}, scopes)

		// Verify tenant was injected into context
		tenantID, ok := tenant.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("bypass authentication for whitelisted method", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator:     validator,
			BypassMethods: []string{"/grpc.health.v1.Health/Check"},
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// No authorization header
		ctx := context.Background()

		// Call interceptor with bypass method
		info := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("error with missing authorization header", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		ctx := context.Background()

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("error with invalid token", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create context with invalid token
		md := metadata.Pairs("authorization", "Bearer invalid.token.here")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("error with expired token", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create expired token
		claims := &Claims{
			UserID: "user-123",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})
}

func TestStreamInterceptor(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("success with valid token", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create valid token with tenant_id (always required)
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		// Create context with metadata
		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		// Create mock stream
		stream := &mockServerStream{ctx: ctx}

		// Call interceptor
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
		err = interceptor.StreamInterceptor()(nil, stream, info, mockStreamHandler)

		assert.NoError(t, err)
	})

	t.Run("bypass authentication for whitelisted method", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator:     validator,
			BypassMethods: []string{"/grpc.health.v1.Health/Watch"},
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// No authorization header
		ctx := context.Background()
		stream := &mockServerStream{ctx: ctx}

		// Call interceptor with bypass method - should succeed without auth
		info := &grpc.StreamServerInfo{FullMethod: "/grpc.health.v1.Health/Watch"}

		// Use a handler that doesn't require auth for bypass methods
		bypassHandler := func(_ interface{}, _ grpc.ServerStream) error {
			return nil
		}

		err = interceptor.StreamInterceptor()(nil, stream, info, bypassHandler)

		assert.NoError(t, err)
	})

	t.Run("error with missing authorization header", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		ctx := context.Background()
		stream := &mockServerStream{ctx: ctx}

		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
		err = interceptor.StreamInterceptor()(nil, stream, info, mockStreamHandler)

		assert.Error(t, err)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})
}

func TestExtractTokenFromMetadata(t *testing.T) {
	t.Run("success extracting valid bearer token", func(t *testing.T) {
		md := metadata.Pairs("authorization", "Bearer valid-token-123")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		token, err := extractTokenFromMetadata(ctx)

		assert.NoError(t, err)
		assert.Equal(t, "valid-token-123", token)
	})

	t.Run("error with missing metadata", func(t *testing.T) {
		ctx := context.Background()

		token, err := extractTokenFromMetadata(ctx)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingAuthHeader)
		assert.Empty(t, token)
	})

	t.Run("error with missing authorization header", func(t *testing.T) {
		md := metadata.Pairs("other-header", "value")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		token, err := extractTokenFromMetadata(ctx)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingAuthHeader)
		assert.Empty(t, token)
	})

	t.Run("error with invalid scheme", func(t *testing.T) {
		md := metadata.Pairs("authorization", "Basic dXNlcjpwYXNz")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		token, err := extractTokenFromMetadata(ctx)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidAuthHeader)
		assert.Empty(t, token)
	})

	t.Run("error with empty token", func(t *testing.T) {
		md := metadata.Pairs("authorization", "Bearer ")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		token, err := extractTokenFromMetadata(ctx)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidAuthHeader)
		assert.Empty(t, token)
	})

	t.Run("error with missing bearer prefix", func(t *testing.T) {
		md := metadata.Pairs("authorization", "token-without-bearer")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		token, err := extractTokenFromMetadata(ctx)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidAuthHeader)
		assert.Empty(t, token)
	})
}

func TestGetContextHelpers(t *testing.T) {
	t.Run("GetUserIDFromContext", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), UserIDContextKey, "user-123")

		userID, ok := GetUserIDFromContext(ctx)

		assert.True(t, ok)
		assert.Equal(t, "user-123", userID)
	})

	t.Run("GetUserIDFromContext with missing value", func(t *testing.T) {
		ctx := context.Background()

		userID, ok := GetUserIDFromContext(ctx)

		assert.False(t, ok)
		assert.Empty(t, userID)
	})

	t.Run("GetRolesFromContext", func(t *testing.T) {
		roles := []string{"admin", "user"}
		ctx := context.WithValue(context.Background(), RolesContextKey, roles)

		extractedRoles, ok := GetRolesFromContext(ctx)

		assert.True(t, ok)
		assert.Equal(t, roles, extractedRoles)
	})

	t.Run("GetRolesFromContext with missing value", func(t *testing.T) {
		ctx := context.Background()

		roles, ok := GetRolesFromContext(ctx)

		assert.False(t, ok)
		assert.Nil(t, roles)
	})

	t.Run("GetScopesFromContext", func(t *testing.T) {
		scopes := []string{"read", "write"}
		ctx := context.WithValue(context.Background(), ScopesContextKey, scopes)

		extractedScopes, ok := GetScopesFromContext(ctx)

		assert.True(t, ok)
		assert.Equal(t, scopes, extractedScopes)
	})

	t.Run("GetScopesFromContext with missing value", func(t *testing.T) {
		ctx := context.Background()

		scopes, ok := GetScopesFromContext(ctx)

		assert.False(t, ok)
		assert.Nil(t, scopes)
	})

	t.Run("GetClaimsFromContext", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"admin"},
			Scopes: []string{"read"},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		extractedClaims, ok := GetClaimsFromContext(ctx)

		assert.True(t, ok)
		assert.Equal(t, claims, extractedClaims)
	})

	t.Run("GetClaimsFromContext with missing value", func(t *testing.T) {
		ctx := context.Background()

		claims, ok := GetClaimsFromContext(ctx)

		assert.False(t, ok)
		assert.Nil(t, claims)
	})
}

func TestRequireRole(t *testing.T) {
	t.Run("success when user has required role", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"admin", "user"},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := RequireRole("admin")
		resp, err := interceptor(ctx, nil, nil, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("success when user has one of multiple required roles", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"user", "moderator"},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := RequireRole("admin", "moderator")
		resp, err := interceptor(ctx, nil, nil, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("error when user lacks required role", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"user"},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := RequireRole("admin")
		resp, err := interceptor(ctx, nil, nil, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
	})

	t.Run("error when claims missing from context", func(t *testing.T) {
		ctx := context.Background()

		interceptor := RequireRole("admin")
		resp, err := interceptor(ctx, nil, nil, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestRequireScope(t *testing.T) {
	t.Run("success when user has all required scopes", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Scopes: []string{"read", "write", "delete"},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := RequireScope("read", "write")
		resp, err := interceptor(ctx, nil, nil, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("error when user lacks one required scope", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Scopes: []string{"read"},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := RequireScope("read", "write")
		resp, err := interceptor(ctx, nil, nil, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
	})

	t.Run("error when claims missing from context", func(t *testing.T) {
		ctx := context.Background()

		interceptor := RequireScope("read")
		resp, err := interceptor(ctx, nil, nil, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

// TestTenantContext tests that tenant context is always required and injected.
// The system is always multi-tenant (platform schema + 1 to N org_X schemas).
func TestTenantContext(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("injects tenant into context", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token with tenant claim
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify tenant was injected into context
		resultCtx := resp.(context.Context)
		tenantID, ok := tenant.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("rejects token without tenant claim", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token without tenant claim
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
		assert.Contains(t, st.Message(), "tenant_id claim required")
	})

	t.Run("rejects token with invalid tenant format", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token with invalid organization format (spaces not allowed)
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "invalid org!",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "invalid tenant_id format")
	})

	t.Run("always rejects token without tenant claim", func(t *testing.T) {
		// The system is always multi-tenant - tenant claims are always required
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token without tenant claim
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		// Should fail - tenant claim is always required
		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
		assert.Contains(t, st.Message(), "tenant_id claim required")
	})

	t.Run("always injects tenant context when present in token", func(t *testing.T) {
		// The system is always multi-tenant - tenant context is always injected
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token with tenant claim
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Tenant should always be in context
		resultCtx := resp.(context.Context)
		tenantID, ok := tenant.FromContext(resultCtx)
		assert.True(t, ok, "tenant context should always be present")
		assert.Equal(t, "acme_bank", tenantID.String())
	})
}

func TestPlatformUnaryInterceptor(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("success with valid token without tenant claim", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create valid token WITHOUT tenant_id (platform services don't require it)
		claims := &Claims{
			UserID: "admin-123",
			Roles:  []string{"platform-admin"},
			Scopes: []string{"read", "write"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor.PlatformUnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify claims were injected into context
		resultCtx := resp.(context.Context)
		userID, ok := GetUserIDFromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, "admin-123", userID)

		roles, ok := GetRolesFromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, []string{"platform-admin"}, roles)

		// Verify NO tenant in context (platform services don't inject tenant)
		_, hasTenant := tenant.FromContext(resultCtx)
		assert.False(t, hasTenant, "platform interceptor should not inject tenant context")
	})

	t.Run("success with token that has tenant claim", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Platform interceptor accepts tokens WITH tenant claims too
		// (PlatformAdminInterceptor is responsible for rejecting them)
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor.PlatformUnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify claims were injected into context
		resultCtx := resp.(context.Context)
		claimsFromCtx, ok := GetClaimsFromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, "acme_bank", claimsFromCtx.TenantID)
	})

	t.Run("bypass authentication for whitelisted method", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator:     validator,
			BypassMethods: []string{"/grpc.health.v1.Health/Check"},
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// No authorization header
		ctx := context.Background()

		info := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
		resp, err := interceptor.PlatformUnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("error with missing authorization header", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		ctx := context.Background()

		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor.PlatformUnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("error with invalid token", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer invalid.token.here")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor.PlatformUnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("error with expired token", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create expired token
		claims := &Claims{
			UserID: "admin-123",
			Roles:  []string{"platform-admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)), // Expired 1 hour ago
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor.PlatformUnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
		assert.Contains(t, st.Message(), "expired")
	})
}

func TestPlatformStreamInterceptor(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("success with valid token without tenant claim", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create valid token WITHOUT tenant_id
		claims := &Claims{
			UserID: "admin-123",
			Roles:  []string{"platform-admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)
		stream := &mockServerStream{ctx: ctx}

		info := &grpc.StreamServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/WatchTenants"}

		// Custom handler to verify context
		var handlerCtx context.Context
		handler := func(_ interface{}, s grpc.ServerStream) error {
			handlerCtx = s.Context()
			return nil
		}

		err = interceptor.PlatformStreamInterceptor()(nil, stream, info, handler)

		assert.NoError(t, err)

		// Verify claims were injected into context
		userID, ok := GetUserIDFromContext(handlerCtx)
		assert.True(t, ok)
		assert.Equal(t, "admin-123", userID)

		// Verify NO tenant in context
		_, hasTenant := tenant.FromContext(handlerCtx)
		assert.False(t, hasTenant, "platform interceptor should not inject tenant context")
	})

	t.Run("bypass authentication for whitelisted method", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator:     validator,
			BypassMethods: []string{"/grpc.health.v1.Health/Watch"},
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		ctx := context.Background()
		stream := &mockServerStream{ctx: ctx}

		info := &grpc.StreamServerInfo{FullMethod: "/grpc.health.v1.Health/Watch"}

		handler := func(_ interface{}, _ grpc.ServerStream) error {
			return nil
		}

		err = interceptor.PlatformStreamInterceptor()(nil, stream, info, handler)

		assert.NoError(t, err)
	})

	t.Run("error with missing authorization header", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		ctx := context.Background()
		stream := &mockServerStream{ctx: ctx}

		info := &grpc.StreamServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/WatchTenants"}

		handler := func(_ interface{}, _ grpc.ServerStream) error {
			return nil
		}

		err = interceptor.PlatformStreamInterceptor()(nil, stream, info, handler)

		assert.Error(t, err)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("error with invalid token", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer invalid.token.here")
		ctx := metadata.NewIncomingContext(context.Background(), md)
		stream := &mockServerStream{ctx: ctx}

		info := &grpc.StreamServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/WatchTenants"}

		handler := func(_ interface{}, _ grpc.ServerStream) error {
			return nil
		}

		err = interceptor.PlatformStreamInterceptor()(nil, stream, info, handler)

		assert.Error(t, err)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})
}

// TestTenantMismatchValidation tests the double-check validation between JWT tenant and header tenant.
// This is a SECURITY feature to prevent cross-tenant access attacks.
func TestTenantMismatchValidation(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("success when JWT tenant matches header tenant", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token with tenant claim
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		// Create context with both authorization and x-tenant-id header (matching)
		md := metadata.Pairs(
			"authorization", "Bearer "+tokenString,
			tenant.TenantIDKey, "acme_bank",
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify tenant was injected into context
		resultCtx := resp.(context.Context)
		tenantID, ok := tenant.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("error when JWT tenant differs from header tenant", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token with tenant claim
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		// Create context with MISMATCHED tenants (user trying to access wrong tenant)
		md := metadata.Pairs(
			"authorization", "Bearer "+tokenString,
			tenant.TenantIDKey, "other_bank", // Different from JWT's acme_bank
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
		assert.Contains(t, st.Message(), "tenant context mismatch")
	})

	t.Run("success when header is missing but JWT tenant exists", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token with tenant claim
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		// Create context with ONLY authorization header (no x-tenant-id)
		// This is for backward compatibility and direct gRPC calls without gateway
		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify tenant was injected into context from JWT
		resultCtx := resp.(context.Context)
		tenantID, ok := tenant.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("error with missing tenant_id claim", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token WITHOUT tenant claim
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
		assert.Contains(t, st.Message(), "tenant_id claim required")
	})

	t.Run("error with invalid tenant_id format in JWT", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token with INVALID tenant format
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "invalid tenant!", // Invalid format (spaces and special chars)
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		md := metadata.Pairs("authorization", "Bearer "+tokenString)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "invalid tenant_id format")
	})

	t.Run("ignores invalid header tenant format and succeeds with JWT tenant", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token with valid tenant claim
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		// Create context with invalid header tenant format
		// Invalid header format is ignored (err != nil from NewTenantID)
		md := metadata.Pairs(
			"authorization", "Bearer "+tokenString,
			tenant.TenantIDKey, "invalid tenant!", // Invalid format
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		// Should succeed because header parsing error is ignored
		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify JWT tenant was used
		resultCtx := resp.(context.Context)
		tenantID, ok := tenant.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("stream interceptor rejects tenant mismatch", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Create token with tenant claim
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		// Create context with MISMATCHED tenants
		md := metadata.Pairs(
			"authorization", "Bearer "+tokenString,
			tenant.TenantIDKey, "other_bank",
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)
		stream := &mockServerStream{ctx: ctx}

		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
		err = interceptor.StreamInterceptor()(nil, stream, info, mockStreamHandler)

		assert.Error(t, err)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
		assert.Contains(t, st.Message(), "tenant context mismatch")
	})
}
