package gateway

import (
	"log/slog"
	"testing"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTenantResolverMiddleware(t *testing.T) {
	// Setup valid dependencies for happy path tests
	validSlugCache := &service.SlugCache{}
	validTenantRepo := &persistence.Repository{}
	validBaseDomain := "meridian.com"
	validLogger := slog.Default()

	tests := []struct {
		name        string
		slugCache   *service.SlugCache
		tenantRepo  *persistence.Repository
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
			)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, middleware)
			} else {
				require.NoError(t, err)
				require.NotNil(t, middleware)

				// Verify all fields are properly initialized
				assert.Equal(t, tt.slugCache, middleware.slugCache)
				assert.Equal(t, tt.tenantRepo, middleware.tenantRepo)
				assert.Equal(t, tt.baseDomain, middleware.baseDomain)
				assert.Equal(t, tt.logger, middleware.logger)
			}
		})
	}
}

func TestNewTenantResolverMiddleware_FieldInitialization(t *testing.T) {
	slugCache := &service.SlugCache{}
	tenantRepo := &persistence.Repository{}
	baseDomain := "example.com"
	logger := slog.Default()

	middleware, err := NewTenantResolverMiddleware(
		slugCache,
		tenantRepo,
		baseDomain,
		logger,
	)

	require.NoError(t, err)
	require.NotNil(t, middleware)

	// Verify struct fields are accessible and correctly set
	assert.NotNil(t, middleware.slugCache, "slugCache field should be set")
	assert.NotNil(t, middleware.tenantRepo, "tenantRepo field should be set")
	assert.Equal(t, baseDomain, middleware.baseDomain, "baseDomain should match input")
	assert.NotNil(t, middleware.logger, "logger field should be set")
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
			name:       "IPv6 address with port",
			baseDomain: "api.meridian.io",
			host:       "[2001:db8::1]:8080",
			want:       "",
			reason:     "should return empty for IPv6 address with port",
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
