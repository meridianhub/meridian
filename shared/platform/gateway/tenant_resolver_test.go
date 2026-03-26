package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTenantResolverMiddleware(t *testing.T) {
	// Setup valid dependencies for happy path tests
	validSlugCache := &MockSlugCache{}
	validTenantRepo := &MockTenantRepository{}
	validBaseDomain := "meridian.com"
	validLogger := slog.Default()

	tests := []struct {
		name        string
		slugCache   slugCache
		tenantRepo  tenantRepository
		baseDomain  string
		logger      *slog.Logger
		wantErr     error
		errContains string
	}{
		{
			name:       "valid configuration",
			slugCache:  validSlugCache,
			tenantRepo: validTenantRepo,
			baseDomain: validBaseDomain,
			logger:     validLogger,
			wantErr:    nil,
		},
		{
			name:       "nil slug cache",
			slugCache:  nil,
			tenantRepo: validTenantRepo,
			baseDomain: validBaseDomain,
			logger:     validLogger,
			wantErr:    ErrNilSlugCache,
		},
		{
			name:       "nil tenant repository",
			slugCache:  validSlugCache,
			tenantRepo: nil,
			baseDomain: validBaseDomain,
			logger:     validLogger,
			wantErr:    ErrNilTenantRepo,
		},
		{
			name:       "empty base domain",
			slugCache:  validSlugCache,
			tenantRepo: validTenantRepo,
			baseDomain: "",
			logger:     validLogger,
			wantErr:    ErrEmptyBaseDomain,
		},
		{
			name:       "nil logger",
			slugCache:  validSlugCache,
			tenantRepo: validTenantRepo,
			baseDomain: validBaseDomain,
			logger:     nil,
			wantErr:    ErrNilLogger,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware, err := NewTenantResolverMiddleware(
				tt.slugCache,
				tt.tenantRepo,
				tt.baseDomain,
				tt.logger,
				false, // localDevMode
			)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, middleware)
			} else {
				require.NoError(t, err)
				require.NotNil(t, middleware)

				// Verify fields are properly initialized (non-nil)
				assert.NotNil(t, middleware.slugCache)
				assert.NotNil(t, middleware.tenantRepo)
				assert.Equal(t, tt.baseDomain, middleware.baseDomain)
				assert.Equal(t, tt.logger, middleware.logger)
				assert.False(t, middleware.localDevMode)
			}
		})
	}
}

func TestNewTenantResolverMiddleware_FieldInitialization(t *testing.T) {
	slugCache := &MockSlugCache{}
	tenantRepo := &MockTenantRepository{}
	baseDomain := "example.com"
	logger := slog.Default()

	middleware, err := NewTenantResolverMiddleware(
		slugCache,
		tenantRepo,
		baseDomain,
		logger,
		true, // localDevMode
	)

	require.NoError(t, err)
	require.NotNil(t, middleware)

	// Verify struct fields are accessible and correctly set
	assert.NotNil(t, middleware.slugCache, "slugCache field should be set")
	assert.NotNil(t, middleware.tenantRepo, "tenantRepo field should be set")
	assert.Equal(t, baseDomain, middleware.baseDomain, "baseDomain should match input")
	assert.NotNil(t, middleware.logger, "logger field should be set")
	assert.True(t, middleware.localDevMode, "localDevMode should be true")
}

func TestExtractSlug(t *testing.T) {
	tests := []struct {
		name       string
		baseDomain string
		host       string
		want       string
		reason     string
	}{
		{
			name:       "valid single subdomain",
			baseDomain: "api.meridian.io",
			host:       "acme.api.meridian.io",
			want:       "acme",
			reason:     "should extract single-level subdomain",
		},
		{
			name:       "valid single subdomain with port",
			baseDomain: "api.meridian.io",
			host:       "acme.api.meridian.io:8080",
			want:       "acme",
			reason:     "should strip port and extract subdomain",
		},
		{
			name:       "valid multi-level subdomain",
			baseDomain: "api.meridian.io",
			host:       "acme.staging.api.meridian.io",
			want:       "acme.staging",
			reason:     "should extract multi-level subdomain",
		},
		{
			name:       "valid multi-level subdomain with port",
			baseDomain: "api.meridian.io",
			host:       "acme.staging.api.meridian.io:9090",
			want:       "acme.staging",
			reason:     "should strip port and extract multi-level subdomain",
		},
		{
			name:       "no subdomain - exact match",
			baseDomain: "api.meridian.io",
			host:       "api.meridian.io",
			want:       "",
			reason:     "should return empty for direct base domain access",
		},
		{
			name:       "no subdomain - exact match with port",
			baseDomain: "api.meridian.io",
			host:       "api.meridian.io:8080",
			want:       "",
			reason:     "should return empty for direct base domain access with port",
		},
		{
			name:       "wrong domain",
			baseDomain: "api.meridian.io",
			host:       "invalid.com",
			want:       "",
			reason:     "should return empty for non-matching domain",
		},
		{
			name:       "wrong domain with port",
			baseDomain: "api.meridian.io",
			host:       "invalid.com:8080",
			want:       "",
			reason:     "should return empty for non-matching domain with port",
		},
		{
			name:       "IPv4 address",
			baseDomain: "api.meridian.io",
			host:       "192.168.1.1",
			want:       "",
			reason:     "should return empty for IPv4 address",
		},
		{
			name:       "IPv4 address with port",
			baseDomain: "api.meridian.io",
			host:       "192.168.1.1:8080",
			want:       "",
			reason:     "should return empty for IPv4 address with port",
		},
		{
			name:       "IPv6 address",
			baseDomain: "api.meridian.io",
			host:       "[2001:db8::1]",
			want:       "",
			reason:     "should return empty for IPv6 address",
		},
		{
			name:       "IPv6 address with port bracket notation",
			baseDomain: "api.meridian.io",
			host:       "[2001:db8::1]:8080",
			want:       "",
			reason:     "should return empty for IPv6 address with port in bracket notation",
		},
		{
			name:       "IPv6 address without brackets",
			baseDomain: "api.meridian.io",
			host:       "2001:db8::1",
			want:       "",
			reason:     "should return empty for IPv6 address without brackets",
		},
		{
			name:       "localhost",
			baseDomain: "api.meridian.io",
			host:       "localhost",
			want:       "",
			reason:     "should return empty for localhost",
		},
		{
			name:       "localhost with port",
			baseDomain: "api.meridian.io",
			host:       "localhost:8080",
			want:       "",
			reason:     "should return empty for localhost with port",
		},
		{
			name:       "empty host",
			baseDomain: "api.meridian.io",
			host:       "",
			want:       "",
			reason:     "should return empty for empty host",
		},
		{
			name:       "partial domain match",
			baseDomain: "api.meridian.io",
			host:       "acme.badapi.meridian.io",
			want:       "",
			reason:     "should return empty when suffix matches but not exact base domain",
		},
		{
			name:       "domain with hyphen in subdomain",
			baseDomain: "api.meridian.io",
			host:       "acme-corp.api.meridian.io",
			want:       "acme-corp",
			reason:     "should handle hyphens in subdomain",
		},
		{
			name:       "domain with number in subdomain",
			baseDomain: "api.meridian.io",
			host:       "acme123.api.meridian.io",
			want:       "acme123",
			reason:     "should handle numbers in subdomain",
		},
		{
			name:       "very short base domain",
			baseDomain: "io",
			host:       "acme.io",
			want:       "acme",
			reason:     "should work with short base domains",
		},
		{
			name:       "host shorter than base domain",
			baseDomain: "api.meridian.io",
			host:       "short.io",
			want:       "",
			reason:     "should return empty when host is shorter than base domain",
		},
		{
			name:       "high port number",
			baseDomain: "api.meridian.io",
			host:       "acme.api.meridian.io:65535",
			want:       "acme",
			reason:     "should handle high port numbers",
		},
		{
			name:       "port with leading zeros",
			baseDomain: "api.meridian.io",
			host:       "acme.api.meridian.io:0080",
			want:       "acme",
			reason:     "should handle ports with leading zeros",
		},
		// Invalid slug format test cases (security validation)
		{
			name:       "slug starting with hyphen",
			baseDomain: "api.meridian.io",
			host:       "-acme.api.meridian.io",
			want:       "",
			reason:     "should reject slug starting with hyphen",
		},
		{
			name:       "slug ending with hyphen",
			baseDomain: "api.meridian.io",
			host:       "acme-.api.meridian.io",
			want:       "",
			reason:     "should reject slug ending with hyphen",
		},
		{
			name:       "slug with consecutive hyphens",
			baseDomain: "api.meridian.io",
			host:       "acme--corp.api.meridian.io",
			want:       "",
			reason:     "should reject slug with consecutive hyphens",
		},
		{
			name:       "slug starting with period",
			baseDomain: "api.meridian.io",
			host:       ".acme.api.meridian.io",
			want:       "",
			reason:     "should reject slug starting with period",
		},
		{
			name:       "slug ending with period",
			baseDomain: "api.meridian.io",
			host:       "acme..api.meridian.io",
			want:       "",
			reason:     "should reject slug ending with period",
		},
		{
			name:       "slug with uppercase letters",
			baseDomain: "api.meridian.io",
			host:       "ACME.api.meridian.io",
			want:       "",
			reason:     "should reject slug with uppercase letters",
		},
		{
			name:       "slug with mixed case",
			baseDomain: "api.meridian.io",
			host:       "Acme-Corp.api.meridian.io",
			want:       "",
			reason:     "should reject slug with mixed case",
		},
		{
			name:       "slug with special characters",
			baseDomain: "api.meridian.io",
			host:       "acme_corp.api.meridian.io",
			want:       "",
			reason:     "should reject slug with underscore",
		},
		{
			name:       "slug with consecutive periods",
			baseDomain: "api.meridian.io",
			host:       "acme..staging.api.meridian.io",
			want:       "",
			reason:     "should reject slug with consecutive periods",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create minimal middleware instance for testing
			middleware := &TenantResolverMiddleware{
				baseDomain: tt.baseDomain,
				logger:     slog.Default(),
			}

			got := middleware.extractSlug(tt.host)
			assert.Equal(t, tt.want, got, tt.reason)
		})
	}
}

// Test errors for resolveTenant tests.
var (
	errCacheWriteFailed = errors.New("redis connection failed")
	errCacheReadTimeout = errors.New("redis connection timeout")
	errDatabaseLost     = errors.New("database connection lost")
)

// TestResolveTenant tests the cache-first tenant resolution with database fallback.
func TestResolveTenant(t *testing.T) {
	ctx := context.Background()
	testSlug := "acme"
	testTenantID := tenant.MustNewTenantID("tenant_123")
	testTenant := &domain.Tenant{
		ID:          testTenantID,
		DisplayName: "Acme Corp",
		Slug:        testSlug,
		Status:      domain.StatusActive,
	}

	t.Run("cache hit returns tenant ID without DB call", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache returns tenant ID
		mockCache.On("Get", ctx, testSlug).Return(testTenantID, nil)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: "meridian.io",
			logger:     logger,
		}

		// Execute
		result, err := middleware.resolveTenant(ctx, testSlug)

		// Assert
		require.NoError(t, err)
		assert.Equal(t, testTenantID, result.ID)

		// Verify cache was called
		mockCache.AssertExpectations(t)

		// Verify repository was NOT called (cache hit)
		mockRepo.AssertNotCalled(t, "GetBySlug")
	})

	t.Run("cache miss triggers DB lookup and cache population", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache miss (empty TenantID), DB returns tenant
		mockCache.On("Get", ctx, testSlug).Return(tenant.TenantID(""), nil)
		mockRepo.On("GetBySlug", ctx, testSlug).Return(testTenant, nil)
		mockCache.On("Set", ctx, testSlug, testTenantID).Return(nil)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: "meridian.io",
			logger:     logger,
		}

		// Execute
		result, err := middleware.resolveTenant(ctx, testSlug)

		// Assert
		require.NoError(t, err)
		assert.Equal(t, testTenantID, result.ID)
		assert.Equal(t, "Acme Corp", result.DisplayName)

		// Verify all expected calls were made
		mockCache.AssertExpectations(t)
		mockRepo.AssertExpectations(t)
	})

	t.Run("DB not-found error propagates correctly", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache miss, DB returns domain.ErrNotFound
		mockCache.On("Get", ctx, testSlug).Return(tenant.TenantID(""), nil)
		mockRepo.On("GetBySlug", ctx, testSlug).Return(nil, domain.ErrNotFound)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: "meridian.io",
			logger:     logger,
		}

		// Execute
		result, err := middleware.resolveTenant(ctx, testSlug)

		// Assert
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTenantNotFound)
		assert.Empty(t, result)

		// Verify calls
		mockCache.AssertExpectations(t)
		mockRepo.AssertExpectations(t)
		mockCache.AssertNotCalled(t, "Set") // Should not attempt to cache not-found
	})

	t.Run("cache write failure doesn't fail request", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache miss, DB succeeds, but cache write fails
		mockCache.On("Get", ctx, testSlug).Return(tenant.TenantID(""), nil)
		mockRepo.On("GetBySlug", ctx, testSlug).Return(testTenant, nil)
		mockCache.On("Set", ctx, testSlug, testTenantID).Return(errCacheWriteFailed)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: "meridian.io",
			logger:     logger,
		}

		// Execute
		result, err := middleware.resolveTenant(ctx, testSlug)

		// Assert: Request should succeed despite cache write failure
		require.NoError(t, err, "cache write failure should not fail the request")
		assert.Equal(t, testTenantID, result.ID)
		assert.Equal(t, "Acme Corp", result.DisplayName)

		// Verify all calls were made
		mockCache.AssertExpectations(t)
		mockRepo.AssertExpectations(t)
	})

	t.Run("cache read failure falls through to DB", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache read fails, but DB succeeds
		mockCache.On("Get", ctx, testSlug).Return(tenant.TenantID(""), errCacheReadTimeout)
		mockRepo.On("GetBySlug", ctx, testSlug).Return(testTenant, nil)
		mockCache.On("Set", ctx, testSlug, testTenantID).Return(nil)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: "meridian.io",
			logger:     logger,
		}

		// Execute
		result, err := middleware.resolveTenant(ctx, testSlug)

		// Assert: Request should succeed despite cache read failure
		require.NoError(t, err, "cache read failure should fall through to DB")
		assert.Equal(t, testTenantID, result.ID)
		assert.Equal(t, "Acme Corp", result.DisplayName)

		// Verify all calls were made (cache get, DB get, cache set)
		mockCache.AssertExpectations(t)
		mockRepo.AssertExpectations(t)
	})

	t.Run("DB error is wrapped and returned", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache miss, DB fails with non-not-found error
		mockCache.On("Get", ctx, testSlug).Return(tenant.TenantID(""), nil)
		mockRepo.On("GetBySlug", ctx, testSlug).Return(nil, errDatabaseLost)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: "meridian.io",
			logger:     logger,
		}

		// Execute
		result, err := middleware.resolveTenant(ctx, testSlug)

		// Assert
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get tenant from database")
		assert.ErrorIs(t, err, errDatabaseLost)
		assert.Empty(t, result)

		// Verify calls
		mockCache.AssertExpectations(t)
		mockRepo.AssertExpectations(t)
	})

	t.Run("context cancellation is respected", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Create cancelled context
		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		// Setup: Cache returns context.Canceled error, DB also respects cancellation
		mockCache.On("Get", cancelledCtx, testSlug).Return(tenant.TenantID(""), context.Canceled)
		mockRepo.On("GetBySlug", cancelledCtx, testSlug).Return(nil, context.Canceled)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: "meridian.io",
			logger:     logger,
		}

		// Execute
		result, err := middleware.resolveTenant(cancelledCtx, testSlug)

		// Assert
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, result)

		// Verify both cache and DB were called (cache error fell through to DB)
		mockCache.AssertExpectations(t)
		mockRepo.AssertExpectations(t)
	})
}

// TestServeHTTP tests the complete middleware integration including
// slug extraction, tenant resolution, header injection, and context propagation.
func TestServeHTTP(t *testing.T) {
	ctx := context.Background()
	baseDomain := "api.meridian.io"
	testSlug := "acme"
	testHost := "acme.api.meridian.io"
	testTenantID := tenant.MustNewTenantID("tenant_123")
	testTenant := &domain.Tenant{
		ID:          testTenantID,
		DisplayName: "Acme Corp",
		Slug:        testSlug,
		Status:      domain.StatusActive,
	}

	t.Run("valid subdomain returns 200 with x-tenant-id header set", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache hit
		mockCache.On("Get", ctx, testSlug).Return(testTenantID, nil)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: baseDomain,
			logger:     logger,
		}

		// Create test request
		req := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/test", nil)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		// Track if next handler was called
		nextCalled := false
		var capturedTenantID tenant.TenantID
		var capturedTenantOk bool
		var capturedHeaderValue string

		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			capturedHeaderValue = r.Header.Get(tenant.TenantIDKey)
			capturedTenantID, capturedTenantOk = tenant.FromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusOK, rec.Code, "should return 200 OK")
		assert.True(t, nextCalled, "next handler should be called")

		// Verify x-tenant-id header was injected
		assert.Equal(t, string(testTenantID), capturedHeaderValue,
			"x-tenant-id header should be set")

		// Verify tenant context was propagated
		assert.True(t, capturedTenantOk, "tenant should be in context")
		assert.Equal(t, testTenantID, capturedTenantID, "tenant ID in context should match")

		mockCache.AssertExpectations(t)
	})

	t.Run("missing subdomain returns 404 with Invalid subdomain", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: baseDomain,
			logger:     logger,
		}

		// Create test request with no subdomain
		req := httptest.NewRequest(http.MethodGet, "http://api.meridian.io/api/test", nil)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		// Track if next handler was called
		nextCalled := false

		next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusNotFound, rec.Code, "should return 404 Not Found")
		assert.Contains(t, rec.Body.String(), "Invalid subdomain",
			"response should contain 'Invalid subdomain'")
		assert.False(t, nextCalled, "next handler should not be called")

		// Verify no cache/DB calls were made
		mockCache.AssertNotCalled(t, "Get")
		mockRepo.AssertNotCalled(t, "GetBySlug")
	})

	t.Run("unknown tenant returns 404 with Tenant not found", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache miss, DB returns domain.ErrNotFound
		mockCache.On("Get", ctx, testSlug).Return(tenant.TenantID(""), nil)
		mockRepo.On("GetBySlug", ctx, testSlug).Return(nil, domain.ErrNotFound)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: baseDomain,
			logger:     logger,
		}

		// Create test request
		req := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/test", nil)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		// Track if next handler was called
		nextCalled := false

		next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusNotFound, rec.Code, "should return 404 Not Found")
		assert.Contains(t, rec.Body.String(), "Tenant not found",
			"response should contain 'Tenant not found'")
		assert.False(t, nextCalled, "next handler should not be called")

		mockCache.AssertExpectations(t)
		mockRepo.AssertExpectations(t)
	})

	t.Run("cache miss with DB hit populates cache and succeeds", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache miss, DB hit, cache write succeeds
		mockCache.On("Get", ctx, testSlug).Return(tenant.TenantID(""), nil)
		mockRepo.On("GetBySlug", ctx, testSlug).Return(testTenant, nil)
		mockCache.On("Set", ctx, testSlug, testTenantID).Return(nil)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: baseDomain,
			logger:     logger,
		}

		// Create test request
		req := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/test", nil)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		// Track captured request
		var capturedRequest *http.Request

		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedRequest = r
			w.WriteHeader(http.StatusOK)
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusOK, rec.Code, "should return 200 OK")

		// Verify tenant ID was injected
		assert.Equal(t, string(testTenantID), capturedRequest.Header.Get(tenant.TenantIDKey),
			"x-tenant-id header should be set")

		// Verify all cache/DB calls were made
		mockCache.AssertExpectations(t)
		mockRepo.AssertExpectations(t)
	})

	t.Run("subdomain with port number is handled correctly", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache hit
		mockCache.On("Get", ctx, testSlug).Return(testTenantID, nil)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: baseDomain,
			logger:     logger,
		}

		// Create test request with port number
		req := httptest.NewRequest(http.MethodGet, "http://"+testHost+":8080/api/test", nil)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusOK, rec.Code, "should handle port in Host header")
		mockCache.AssertExpectations(t)
	})

	t.Run("multi-level subdomain is handled correctly", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		multiLevelSlug := "acme.staging"
		multiLevelHost := "acme.staging.api.meridian.io"
		multiLevelTenantID := tenant.MustNewTenantID("tenant_456")

		// Setup: Cache hit for multi-level slug
		mockCache.On("Get", ctx, multiLevelSlug).Return(multiLevelTenantID, nil)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: baseDomain,
			logger:     logger,
		}

		// Create test request
		req := httptest.NewRequest(http.MethodGet, "http://"+multiLevelHost+"/api/test", nil)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		var capturedRequest *http.Request

		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedRequest = r
			w.WriteHeader(http.StatusOK)
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusOK, rec.Code, "should handle multi-level subdomain")
		assert.Equal(t, string(multiLevelTenantID), capturedRequest.Header.Get(tenant.TenantIDKey),
			"x-tenant-id header should be set for multi-level subdomain")
		mockCache.AssertExpectations(t)
	})

	t.Run("database error returns 503", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache miss, DB error (transient failure)
		mockCache.On("Get", ctx, testSlug).Return(tenant.TenantID(""), nil)
		mockRepo.On("GetBySlug", ctx, testSlug).Return(nil, errDatabaseLost)

		// Create middleware
		middleware := &TenantResolverMiddleware{
			slugCache:  mockCache,
			tenantRepo: mockRepo,
			baseDomain: baseDomain,
			logger:     logger,
		}

		// Create test request
		req := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/test", nil)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		nextCalled := false

		next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "should return 503 on transient DB error")
		assert.Contains(t, rec.Body.String(), "Service temporarily unavailable",
			"response should contain 'Service temporarily unavailable'")
		assert.False(t, nextCalled, "next handler should not be called")

		mockCache.AssertExpectations(t)
		mockRepo.AssertExpectations(t)
	})
}

func TestIsPlatformPath(t *testing.T) {
	// REST transcoding paths
	assert.True(t, IsPlatformPath("/v1/tenants"))
	assert.True(t, IsPlatformPath("/v1/tenants/acme_corp"))

	// Connect/gRPC paths
	assert.True(t, IsPlatformPath("/meridian.tenant.v1.TenantService/ListTenants"))
	assert.True(t, IsPlatformPath("/meridian.tenant.v1.TenantService/CreateTenant"))
	assert.True(t, IsPlatformPath("/meridian.tenant.v1.TenantService/GetTenant"))

	// Dex OIDC paths are NOT platform paths (handled by HandlerOptionalTenant)
	assert.False(t, IsPlatformPath("/dex/auth"))
	assert.False(t, IsPlatformPath("/dex/callback"))
	assert.False(t, IsPlatformPath("/dex/keys"))
	assert.False(t, IsPlatformPath("/dex/token"))

	// Non-platform paths
	assert.False(t, IsPlatformPath("/v1/accounts"))
	assert.False(t, IsPlatformPath("/v1/parties"))
	assert.False(t, IsPlatformPath("/health"))
	assert.False(t, IsPlatformPath("/meridian.party.v1.PartyService/ListParties"))
}

func TestPlatformPathBypassesTenantResolution(t *testing.T) {
	middleware := &TenantResolverMiddleware{
		slugCache:  new(MockSlugCache),
		tenantRepo: new(MockTenantRepository),
		baseDomain: "api.meridian.io",
		logger:     slog.Default(),
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// POST /v1/tenants should bypass tenant resolution (REST transcoding path)
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8090/v1/tenants", nil)
	rec := httptest.NewRecorder()

	middleware.Handler(next).ServeHTTP(rec, req)

	assert.True(t, nextCalled, "next handler should be called for platform paths")
	assert.Equal(t, http.StatusOK, rec.Code)

	// Connect/gRPC path should also bypass tenant resolution
	nextCalled = false
	req = httptest.NewRequest(http.MethodPost, "http://localhost:8090/meridian.tenant.v1.TenantService/ListTenants", nil)
	rec = httptest.NewRecorder()

	middleware.Handler(next).ServeHTTP(rec, req)

	assert.True(t, nextCalled, "next handler should be called for Connect/gRPC platform paths")
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestLocalDevMode tests the X-Tenant-Slug header support in local development mode.
func TestLocalDevMode(t *testing.T) {
	ctx := context.Background()
	baseDomain := "api.meridian.io"
	testSlug := "acme"
	testTenantID := tenant.MustNewTenantID("tenant_123")

	t.Run("LOCAL_DEV_MODE=true with X-Tenant-Slug header works", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Setup: Cache hit for the header-provided slug
		mockCache.On("Get", ctx, testSlug).Return(testTenantID, nil)

		// Create middleware with localDevMode enabled
		middleware := &TenantResolverMiddleware{
			slugCache:    mockCache,
			tenantRepo:   mockRepo,
			baseDomain:   baseDomain,
			logger:       logger,
			localDevMode: true,
		}

		// Create test request with X-Tenant-Slug header (no valid subdomain)
		req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/test", nil)
		req.Header.Set(TenantSlugHeader, testSlug)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		var capturedTenantID tenant.TenantID
		var capturedTenantOk bool

		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedTenantID, capturedTenantOk = tenant.FromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusOK, rec.Code, "should return 200 OK")
		assert.True(t, capturedTenantOk, "tenant should be in context")
		assert.Equal(t, testTenantID, capturedTenantID, "tenant ID should match")
		mockCache.AssertExpectations(t)
	})

	t.Run("LOCAL_DEV_MODE=false ignores X-Tenant-Slug header", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Create middleware with localDevMode disabled
		middleware := &TenantResolverMiddleware{
			slugCache:    mockCache,
			tenantRepo:   mockRepo,
			baseDomain:   baseDomain,
			logger:       logger,
			localDevMode: false,
		}

		// Create test request with X-Tenant-Slug header but no valid subdomain
		req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/test", nil)
		req.Header.Set(TenantSlugHeader, testSlug)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		nextCalled := false

		next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			nextCalled = true
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert: Should fail because header is ignored and no valid subdomain
		assert.Equal(t, http.StatusNotFound, rec.Code, "should return 404 when header is ignored")
		assert.Contains(t, rec.Body.String(), "Invalid subdomain")
		assert.False(t, nextCalled, "next handler should not be called")
		mockCache.AssertNotCalled(t, "Get")
	})

	t.Run("header takes precedence over subdomain when both present", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		headerSlug := "header-tenant"
		headerTenantID := tenant.MustNewTenantID("tenant_header")

		// Setup: Cache hit for header slug (NOT subdomain slug)
		mockCache.On("Get", ctx, headerSlug).Return(headerTenantID, nil)

		// Create middleware with localDevMode enabled
		middleware := &TenantResolverMiddleware{
			slugCache:    mockCache,
			tenantRepo:   mockRepo,
			baseDomain:   baseDomain,
			logger:       logger,
			localDevMode: true,
		}

		// Create test request with BOTH valid subdomain AND header
		req := httptest.NewRequest(http.MethodGet, "http://acme.api.meridian.io/api/test", nil)
		req.Header.Set(TenantSlugHeader, headerSlug)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		var capturedTenantID tenant.TenantID

		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedTenantID, _ = tenant.FromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert: Header tenant should be used, not subdomain tenant
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, headerTenantID, capturedTenantID,
			"header tenant ID should be used, not subdomain tenant ID")
		mockCache.AssertExpectations(t)
		// Verify only header slug was looked up
		mockCache.AssertCalled(t, "Get", ctx, headerSlug)
	})

	t.Run("falls back to subdomain when header is empty", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		subdomainSlug := "acme"
		subdomainTenantID := tenant.MustNewTenantID("tenant_subdomain")

		// Setup: Cache hit for subdomain slug
		mockCache.On("Get", ctx, subdomainSlug).Return(subdomainTenantID, nil)

		// Create middleware with localDevMode enabled
		middleware := &TenantResolverMiddleware{
			slugCache:    mockCache,
			tenantRepo:   mockRepo,
			baseDomain:   baseDomain,
			logger:       logger,
			localDevMode: true,
		}

		// Create test request with valid subdomain but no header
		req := httptest.NewRequest(http.MethodGet, "http://acme.api.meridian.io/api/test", nil)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		var capturedTenantID tenant.TenantID

		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedTenantID, _ = tenant.FromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		// Execute
		handler := middleware.Handler(next)
		handler.ServeHTTP(rec, req)

		// Assert: Should use subdomain tenant
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, subdomainTenantID, capturedTenantID)
		mockCache.AssertExpectations(t)
	})

	t.Run("invalid slug in header returns 400 Bad Request", func(t *testing.T) {
		mockCache := new(MockSlugCache)
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		// Create middleware with localDevMode enabled
		middleware := &TenantResolverMiddleware{
			slugCache:    mockCache,
			tenantRepo:   mockRepo,
			baseDomain:   baseDomain,
			logger:       logger,
			localDevMode: true,
		}

		invalidSlugs := []string{
			"UPPERCASE",
			"-invalid",
			"invalid-",
			"invalid--slug",
			"with_underscore",
			"with spaces",
		}

		for _, invalidSlug := range invalidSlugs {
			t.Run(invalidSlug, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/test", nil)
				req.Header.Set(TenantSlugHeader, invalidSlug)
				req = req.WithContext(ctx)
				rec := httptest.NewRecorder()

				nextCalled := false
				next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
					nextCalled = true
				})

				handler := middleware.Handler(next)
				handler.ServeHTTP(rec, req)

				assert.Equal(t, http.StatusBadRequest, rec.Code,
					"should return 400 for invalid slug: %s", invalidSlug)
				assert.Contains(t, rec.Body.String(), "Invalid tenant slug")
				assert.False(t, nextCalled, "next handler should not be called")
			})
		}
	})

	t.Run("valid slug formats in header work", func(t *testing.T) {
		mockRepo := new(MockTenantRepository)
		logger := slog.Default()

		validSlugTenantID := tenant.MustNewTenantID("tenant_valid")

		validSlugs := []string{
			"acme",
			"acme123",
			"acme-corp",
			"my-company",
			"tenant1",
		}

		for _, validSlug := range validSlugs {
			t.Run(validSlug, func(t *testing.T) {
				mockCache := new(MockSlugCache)
				mockCache.On("Get", ctx, validSlug).Return(validSlugTenantID, nil)

				middleware := &TenantResolverMiddleware{
					slugCache:    mockCache,
					tenantRepo:   mockRepo,
					baseDomain:   baseDomain,
					logger:       logger,
					localDevMode: true,
				}

				req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/test", nil)
				req.Header.Set(TenantSlugHeader, validSlug)
				req = req.WithContext(ctx)
				rec := httptest.NewRecorder()

				next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				})

				handler := middleware.Handler(next)
				handler.ServeHTTP(rec, req)

				assert.Equal(t, http.StatusOK, rec.Code,
					"should accept valid slug: %s", validSlug)
				mockCache.AssertExpectations(t)
			})
		}
	})
}

func TestHandlerOptionalTenant_WithValidSubdomain(t *testing.T) {
	ctx := context.Background()
	baseDomain := "api.meridian.io"
	testSlug := "acme"
	testHost := "acme.api.meridian.io"
	testTenantID := tenant.MustNewTenantID("tenant_123")

	mockCache := new(MockSlugCache)
	mockRepo := new(MockTenantRepository)
	logger := slog.Default()

	mockCache.On("Get", ctx, testSlug).Return(testTenantID, nil)

	middleware := &TenantResolverMiddleware{
		slugCache:  mockCache,
		tenantRepo: mockRepo,
		baseDomain: baseDomain,
		logger:     logger,
	}

	req := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/test", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	var capturedTenantID tenant.TenantID
	var capturedTenantOk bool
	var capturedHeaderValue string

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaderValue = r.Header.Get(tenant.TenantIDKey)
		capturedTenantID, capturedTenantOk = tenant.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.HandlerOptionalTenant(next)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, capturedTenantOk, "tenant should be in context")
	assert.Equal(t, testTenantID, capturedTenantID)
	assert.Equal(t, string(testTenantID), capturedHeaderValue)
	mockCache.AssertExpectations(t)
}

func TestHandlerOptionalTenant_WithoutSubdomain(t *testing.T) {
	ctx := context.Background()
	baseDomain := "api.meridian.io"

	mockCache := new(MockSlugCache)
	mockRepo := new(MockTenantRepository)
	logger := slog.Default()

	middleware := &TenantResolverMiddleware{
		slugCache:  mockCache,
		tenantRepo: mockRepo,
		baseDomain: baseDomain,
		logger:     logger,
	}

	// Request with base domain only (no subdomain)
	req := httptest.NewRequest(http.MethodGet, "http://api.meridian.io/dex/auth", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	nextCalled := false
	var capturedTenantOk bool

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		_, capturedTenantOk = tenant.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.HandlerOptionalTenant(next)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, nextCalled, "next handler should be called")
	assert.False(t, capturedTenantOk, "tenant should NOT be in context")
	mockCache.AssertNotCalled(t, "Get")
	mockRepo.AssertNotCalled(t, "GetBySlug")
}

func TestHandlerOptionalTenant_WithInvalidSubdomain(t *testing.T) {
	ctx := context.Background()
	baseDomain := "api.meridian.io"

	mockCache := new(MockSlugCache)
	mockRepo := new(MockTenantRepository)
	logger := slog.Default()

	middleware := &TenantResolverMiddleware{
		slugCache:  mockCache,
		tenantRepo: mockRepo,
		baseDomain: baseDomain,
		logger:     logger,
	}

	// Request with invalid subdomain (uppercase)
	req := httptest.NewRequest(http.MethodGet, "http://INVALID.api.meridian.io/dex/auth", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	nextCalled := false
	var capturedTenantOk bool

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		_, capturedTenantOk = tenant.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.HandlerOptionalTenant(next)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, nextCalled, "next handler should be called even with invalid subdomain")
	assert.False(t, capturedTenantOk, "tenant should NOT be in context")
	mockCache.AssertNotCalled(t, "Get")
}

func TestHandlerOptionalTenant_TenantNotFoundInDB(t *testing.T) {
	ctx := context.Background()
	baseDomain := "api.meridian.io"
	testSlug := "nonexistent"
	testHost := "nonexistent.api.meridian.io"

	mockCache := new(MockSlugCache)
	mockRepo := new(MockTenantRepository)
	logger := slog.Default()

	// Cache miss, DB returns not found
	mockCache.On("Get", ctx, testSlug).Return(tenant.TenantID(""), nil)
	mockRepo.On("GetBySlug", ctx, testSlug).Return(nil, domain.ErrNotFound)

	middleware := &TenantResolverMiddleware{
		slugCache:  mockCache,
		tenantRepo: mockRepo,
		baseDomain: baseDomain,
		logger:     logger,
	}

	req := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/dex/auth", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	nextCalled := false
	var capturedTenantOk bool

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		_, capturedTenantOk = tenant.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.HandlerOptionalTenant(next)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "should pass through even when tenant not found")
	assert.True(t, nextCalled, "next handler should be called")
	assert.False(t, capturedTenantOk, "tenant should NOT be in context")
	mockCache.AssertExpectations(t)
	mockRepo.AssertExpectations(t)
}

func TestHandlerOptionalTenant_LocalDevMode_ValidSlugFromHeader(t *testing.T) {
	ctx := context.Background()
	baseDomain := "api.meridian.io"
	testSlug := "acme"
	testTenantID := tenant.MustNewTenantID("tenant_abc")
	logger := slog.Default()

	mockCache := new(MockSlugCache)
	mockRepo := new(MockTenantRepository)

	mockCache.On("Get", ctx, testSlug).Return(testTenantID, nil)

	middleware := &TenantResolverMiddleware{
		slugCache:    mockCache,
		tenantRepo:   mockRepo,
		baseDomain:   baseDomain,
		logger:       logger,
		localDevMode: true,
	}

	req := httptest.NewRequest(http.MethodGet, "http://"+baseDomain+"/api/test", nil)
	req.Header.Set(TenantSlugHeader, testSlug)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	var capturedTenantOk bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, capturedTenantOk = tenant.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.HandlerOptionalTenant(next)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, capturedTenantOk, "tenant should be resolved from header slug in local dev mode")
	mockCache.AssertExpectations(t)
}

func TestHandlerOptionalTenant_LocalDevMode_InvalidSlugFromHeader(t *testing.T) {
	ctx := context.Background()
	baseDomain := "api.meridian.io"
	logger := slog.Default()

	mockCache := new(MockSlugCache)
	mockRepo := new(MockTenantRepository)

	middleware := &TenantResolverMiddleware{
		slugCache:    mockCache,
		tenantRepo:   mockRepo,
		baseDomain:   baseDomain,
		logger:       logger,
		localDevMode: true,
	}

	req := httptest.NewRequest(http.MethodGet, "http://"+baseDomain+"/api/test", nil)
	req.Header.Set(TenantSlugHeader, "INVALID SLUG!!!")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	var capturedTenantOk bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, capturedTenantOk = tenant.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.HandlerOptionalTenant(next)
	handler.ServeHTTP(rec, req)

	// Invalid slug: falls through to subdomain extraction, which also returns empty, so no tenant
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, capturedTenantOk, "no tenant should be resolved with invalid slug")
}

func TestPlatformPathsDoesNotIncludeDex(t *testing.T) {
	assert.False(t, IsPlatformPath("/dex/auth"), "/dex/auth should not be a platform path")
	assert.False(t, IsPlatformPath("/dex/callback"), "/dex/callback should not be a platform path")
	assert.False(t, IsPlatformPath("/dex/keys"), "/dex/keys should not be a platform path")
	assert.False(t, IsPlatformPath("/dex/token"), "/dex/token should not be a platform path")
	assert.False(t, IsPlatformPath("/dex/"), "/dex/ should not be a platform path")

	// Existing platform paths should still work
	assert.True(t, IsPlatformPath("/v1/tenants"))
	assert.True(t, IsPlatformPath("/meridian.tenant.v1.TenantService/ListTenants"))
}
