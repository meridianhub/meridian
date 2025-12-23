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
