// Package gateway provides HTTP middleware for API gateway functionality.
package gateway

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/service"
)

// Configuration errors.
var (
	ErrNilSlugCache    = errors.New("slug cache cannot be nil")
	ErrNilTenantRepo   = errors.New("tenant repository cannot be nil")
	ErrEmptyBaseDomain = errors.New("base domain cannot be empty")
	ErrNilLogger       = errors.New("logger cannot be nil")
)

// TenantResolverMiddleware extracts tenant information from the Host header
// and injects the tenant ID into the request context.
//
// It follows this resolution flow:
// 1. Extract subdomain slug from Host header (e.g., "acme" from "acme.meridian.com")
// 2. Check slug cache for tenant ID
// 3. On cache miss, query tenant repository and populate cache
// 4. Inject tenant ID into request context via x-tenant-id header
type TenantResolverMiddleware struct {
	slugCache  *service.SlugCache
	tenantRepo *persistence.Repository
	baseDomain string
	logger     *slog.Logger
}

// NewTenantResolverMiddleware creates a new tenant resolver middleware.
// All parameters are required and validated.
func NewTenantResolverMiddleware(
	slugCache *service.SlugCache,
	tenantRepo *persistence.Repository,
	baseDomain string,
	logger *slog.Logger,
) (*TenantResolverMiddleware, error) {
	if slugCache == nil {
		return nil, ErrNilSlugCache
	}
	if tenantRepo == nil {
		return nil, ErrNilTenantRepo
	}
	if baseDomain == "" {
		return nil, ErrEmptyBaseDomain
	}
	if logger == nil {
		return nil, ErrNilLogger
	}

	return &TenantResolverMiddleware{
		slugCache:  slugCache,
		tenantRepo: tenantRepo,
		baseDomain: baseDomain,
		logger:     logger,
	}, nil
}

// ServeHTTP implements the middleware logic for tenant resolution.
// This method will extract the tenant slug from the Host header,
// resolve it to a tenant ID, and inject it into the request context.
func (m *TenantResolverMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	// TODO: Implement tenant resolution logic in subsequent subtasks:
	// 1. Extract subdomain from Host header
	// 2. Look up tenant ID from cache
	// 3. On cache miss, query repository and populate cache
	// 4. Inject tenant ID into request context
	// 5. Call next handler

	// For now, just pass through to next handler
	next.ServeHTTP(w, r)
}
