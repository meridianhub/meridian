package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestParsePrefixedKey(t *testing.T) {
	tests := []struct {
		name         string
		apiKey       string
		expectSlug   string
		expectPrefix string
		expectOK     bool
	}{
		{
			name:         "valid key",
			apiKey:       "pk_acme_abc12345xyz67890abcdef",
			expectSlug:   "acme",
			expectPrefix: "pk_acme_abc12345",
			expectOK:     true,
		},
		{
			name:         "valid key with longer slug",
			apiKey:       "pk_my-company_abcdefgh1234567890",
			expectSlug:   "my-company",
			expectPrefix: "pk_my-company_abcdefgh",
			expectOK:     true,
		},
		{
			name:         "minimum entropy length",
			apiKey:       "pk_acme_12345678",
			expectSlug:   "acme",
			expectPrefix: "pk_acme_12345678",
			expectOK:     true,
		},
		{
			name:     "not prefixed key",
			apiKey:   "regular-api-key",
			expectOK: false,
		},
		{
			name:     "empty string",
			apiKey:   "",
			expectOK: false,
		},
		{
			name:     "only prefix",
			apiKey:   "pk_",
			expectOK: false,
		},
		{
			name:     "no entropy",
			apiKey:   "pk_acme_",
			expectOK: false,
		},
		{
			name:     "entropy too short",
			apiKey:   "pk_acme_1234567",
			expectOK: false,
		},
		{
			name:     "empty slug",
			apiKey:   "pk__abc12345xyz67890",
			expectOK: false,
		},
		{
			name:     "missing second underscore",
			apiKey:   "pk_acmeabc12345xyz67890",
			expectOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, prefix, ok := ParsePrefixedKey(tt.apiKey)
			assert.Equal(t, tt.expectOK, ok)
			if ok {
				assert.Equal(t, tt.expectSlug, slug)
				assert.Equal(t, tt.expectPrefix, prefix)
			}
		})
	}
}

// mockAuthServiceClient is a mock for the gRPC AuthServiceClient.
type mockAuthServiceClient struct {
	response  *controlplanev1.ValidateAPIKeyResponse
	err       error
	callCount atomic.Int32
}

func (m *mockAuthServiceClient) ValidateAPIKey(_ context.Context, _ *controlplanev1.ValidateAPIKeyRequest, _ ...grpc.CallOption) (*controlplanev1.ValidateAPIKeyResponse, error) {
	m.callCount.Add(1)
	return m.response, m.err
}

// mockSlugResolver is a mock for the SlugResolver interface.
type mockSlugResolver struct {
	tenantID tenant.TenantID
	err      error
}

func (m *mockSlugResolver) ResolveSlug(_ context.Context, _ string) (tenant.TenantID, error) {
	return m.tenantID, m.err
}

func TestRPCAPIKeyValidator_Validate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("valid key", func(t *testing.T) {
		client := &mockAuthServiceClient{
			response: &controlplanev1.ValidateAPIKeyResponse{
				Valid:        true,
				TenantId:     "test_tenant",
				Identity:     "Alice",
				Scopes:       []string{"read", "write"},
				RateLimitRps: 50,
			},
		}
		resolver := &mockSlugResolver{
			tenantID: tenant.MustNewTenantID("test_tenant"),
		}

		v := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       client,
			SlugResolver: resolver,
			Logger:       logger,
		})
		defer v.Close()

		result, err := v.Validate(context.Background(), "pk_acme_abc12345xyz67890abcdef")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.Valid)
		assert.Equal(t, "test_tenant", result.TenantID)
		assert.Equal(t, "Alice", result.Identity)
		assert.Equal(t, []string{"read", "write"}, result.Scopes)
		assert.Equal(t, int32(50), result.RateLimitRPS)
	})

	t.Run("non-prefixed key returns ErrNotPrefixedKey", func(t *testing.T) {
		client := &mockAuthServiceClient{}
		resolver := &mockSlugResolver{}

		v := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       client,
			SlugResolver: resolver,
			Logger:       logger,
		})
		defer v.Close()

		result, err := v.Validate(context.Background(), "regular-api-key")
		require.ErrorIs(t, err, ErrNotPrefixedKey)
		assert.Nil(t, result)
	})

	t.Run("invalid key from RPC", func(t *testing.T) {
		client := &mockAuthServiceClient{
			response: &controlplanev1.ValidateAPIKeyResponse{
				Valid: false,
			},
		}
		resolver := &mockSlugResolver{
			tenantID: tenant.MustNewTenantID("test_tenant"),
		}

		v := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       client,
			SlugResolver: resolver,
			Logger:       logger,
		})
		defer v.Close()

		result, err := v.Validate(context.Background(), "pk_acme_abc12345xyz67890abcdef")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.Valid)
	})

	t.Run("cached result avoids RPC call", func(t *testing.T) {
		client := &mockAuthServiceClient{
			response: &controlplanev1.ValidateAPIKeyResponse{
				Valid:    true,
				TenantId: "test_tenant",
				Identity: "Alice",
			},
		}
		resolver := &mockSlugResolver{
			tenantID: tenant.MustNewTenantID("test_tenant"),
		}

		v := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       client,
			SlugResolver: resolver,
			CacheTTL:     5 * time.Minute,
			Logger:       logger,
		})
		defer v.Close()

		// First call
		result1, err := v.Validate(context.Background(), "pk_acme_abc12345xyz67890abcdef")
		require.NoError(t, err)
		assert.True(t, result1.Valid)

		// Second call should use cache
		result2, err := v.Validate(context.Background(), "pk_acme_abc12345xyz67890abcdef")
		require.NoError(t, err)
		assert.True(t, result2.Valid)

		// Only one RPC call should have been made
		assert.Equal(t, int32(1), client.callCount.Load())
	})

	t.Run("slug resolution failure returns error", func(t *testing.T) {
		client := &mockAuthServiceClient{}
		resolver := &mockSlugResolver{
			err: assert.AnError,
		}

		v := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       client,
			SlugResolver: resolver,
			Logger:       logger,
		})
		defer v.Close()

		_, err := v.Validate(context.Background(), "pk_acme_abc12345xyz67890abcdef")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resolve tenant slug")
	})
}

func TestRPCAPIKeyValidator_AllowRequest(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("allows within rate limit", func(t *testing.T) {
		v := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       &mockAuthServiceClient{},
			SlugResolver: &mockSlugResolver{},
			Logger:       logger,
		})
		defer v.Close()

		// With rate_limit_rps=10, burst=20
		for i := 0; i < 10; i++ {
			assert.True(t, v.AllowRequest("pk_acme_abc12345", 10))
		}
	})

	t.Run("rejects when rate exceeded", func(t *testing.T) {
		v := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       &mockAuthServiceClient{},
			SlugResolver: &mockSlugResolver{},
			Logger:       logger,
		})
		defer v.Close()

		// Exhaust burst (rate=1, burst=2)
		v.AllowRequest("pk_test_12345678", 1)
		v.AllowRequest("pk_test_12345678", 1)

		// Should be rejected
		assert.False(t, v.AllowRequest("pk_test_12345678", 1))
	})
}

func TestCombinedAuthMiddleware_RPCAPIKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("RPC validation for pk_ prefixed keys", func(t *testing.T) {
		client := &mockAuthServiceClient{
			response: &controlplanev1.ValidateAPIKeyResponse{
				Valid:        true,
				TenantId:     "test_tenant",
				Identity:     "Alice",
				Scopes:       []string{"read"},
				RateLimitRps: 1000,
			},
		}
		resolver := &mockSlugResolver{
			tenantID: tenant.MustNewTenantID("test_tenant"),
		}

		rpcValidator := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       client,
			SlugResolver: resolver,
			Logger:       logger,
		})

		config := CombinedAuthConfig{
			RPCValidator: rpcValidator,
			Logger:       logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)
		defer middleware.Close()

		var capturedCtx context.Context
		handler := middleware.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("X-API-Key", "pk_acme_abc12345xyz67890abcdef")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "Alice", GetAPIKeyIdentity(capturedCtx))

		// Verify tenant was injected
		tenantID, ok := tenant.FromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, "test_tenant", tenantID.String())

		// Verify scopes were injected
		scopes, ok := GetScopesFromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, []string{"read"}, scopes)

		// Note: X-API-Key header stripping is handled by the proxy director
	})

	t.Run("legacy key falls back to env-var validation", func(t *testing.T) {
		client := &mockAuthServiceClient{}
		resolver := &mockSlugResolver{}

		rpcValidator := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       client,
			SlugResolver: resolver,
			Logger:       logger,
		})

		config := CombinedAuthConfig{
			APIKeyConfig: APIKeyConfig{
				APIKeys: map[string]string{"legacy-key-123": "service-a"},
			},
			RPCValidator: rpcValidator,
			Logger:       logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)
		defer middleware.Close()

		var capturedIdentity string
		handler := middleware.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedIdentity = GetAPIKeyIdentity(r.Context())
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("X-API-Key", "legacy-key-123")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "service-a", capturedIdentity)
		// No RPC call should have been made
		assert.Equal(t, int32(0), client.callCount.Load())
	})

	t.Run("invalid pk_ key returns 401", func(t *testing.T) {
		client := &mockAuthServiceClient{
			response: &controlplanev1.ValidateAPIKeyResponse{
				Valid: false,
			},
		}
		resolver := &mockSlugResolver{
			tenantID: tenant.MustNewTenantID("test_tenant"),
		}

		rpcValidator := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       client,
			SlugResolver: resolver,
			Logger:       logger,
		})

		config := CombinedAuthConfig{
			RPCValidator: rpcValidator,
			Logger:       logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)
		defer middleware.Close()

		handler := middleware.Handler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("handler should not be called for invalid key")
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("X-API-Key", "pk_acme_abc12345xyz67890abcdef")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("RPC rate limit exceeded returns 429", func(t *testing.T) {
		client := &mockAuthServiceClient{
			response: &controlplanev1.ValidateAPIKeyResponse{
				Valid:        true,
				TenantId:     "test_tenant",
				Identity:     "Alice",
				RateLimitRps: 1, // Very low
			},
		}
		resolver := &mockSlugResolver{
			tenantID: tenant.MustNewTenantID("test_tenant"),
		}

		rpcValidator := NewRPCAPIKeyValidator(RPCValidatorConfig{
			Client:       client,
			SlugResolver: resolver,
			Logger:       logger,
		})

		config := CombinedAuthConfig{
			RPCValidator: rpcValidator,
			Logger:       logger,
		}

		middleware, err := NewCombinedAuthMiddleware(config)
		require.NoError(t, err)
		defer middleware.Close()

		handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		apiKey := "pk_acme_abc12345xyz67890abcdef"

		// Exhaust rate limit (burst = rate*2 = 2)
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			req.Header.Set("X-API-Key", apiKey)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code, "request %d should succeed", i+1)
		}

		// Next request should be rate limited
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("X-API-Key", apiKey)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
	})
}
