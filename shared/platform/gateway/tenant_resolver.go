// Package gateway provides HTTP middleware for API gateway functionality.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/service"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Configuration errors.
var (
	ErrNilSlugCache    = errors.New("slug cache cannot be nil")
	ErrNilTenantRepo   = errors.New("tenant repository cannot be nil")
	ErrEmptyBaseDomain = errors.New("base domain cannot be empty")
	ErrNilLogger       = errors.New("logger cannot be nil")
)

// slugCache defines the caching interface for slug-to-tenant-ID mappings.
type slugCache interface {
	Get(ctx context.Context, slug string) (tenant.TenantID, error)
	Set(ctx context.Context, slug string, tenantID tenant.TenantID) error
}

// tenantRepository defines the repository interface for tenant lookups.
type tenantRepository interface {
	GetBySlug(ctx context.Context, slug string) (*domain.Tenant, error)
}

// TenantResolverMiddleware extracts tenant information from the Host header
// and injects the tenant ID into the request context.
//
// It follows this resolution flow:
// 1. Extract subdomain slug from Host header (e.g., "acme" from "acme.meridian.com")
// 2. Check slug cache for tenant ID
// 3. On cache miss, query tenant repository and populate cache
// 4. Inject tenant ID into request context via x-tenant-id header
type TenantResolverMiddleware struct {
	slugCache  slugCache
	tenantRepo tenantRepository
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

// extractSlug extracts the subdomain slug from a Host header value.
//
// The method:
// 1. Strips any port number from the host (e.g., "acme.api.meridian.io:8080" → "acme.api.meridian.io")
// 2. Validates the host ends with ".<baseDomain>"
// 3. Extracts the subdomain slug by removing the base domain suffix
//
// Returns an empty string for:
// - Invalid domain patterns (doesn't match base domain)
// - No subdomain present (e.g., "api.meridian.io" when baseDomain is "api.meridian.io")
// - Direct IP addresses (IPv4 or IPv6)
// - localhost
//
// Examples (assuming baseDomain = "api.meridian.io"):
//   - "acme.api.meridian.io" → "acme"
//   - "acme.api.meridian.io:8080" → "acme"
//   - "acme.staging.api.meridian.io" → "acme.staging"
//   - "api.meridian.io" → "" (no subdomain)
//   - "invalid.com" → "" (wrong domain)
//   - "192.168.1.1" → "" (IP address)
//   - "localhost" → ""
func (m *TenantResolverMiddleware) extractSlug(host string) string {
	if host == "" {
		return ""
	}

	// Strip port if present
	hostWithoutPort := host
	if colonIndex := len(host) - 1; colonIndex >= 0 {
		for i := len(host) - 1; i >= 0; i-- {
			if host[i] == ':' {
				hostWithoutPort = host[:i]
				break
			}
			// Stop if we hit a non-digit character before finding ':'
			if host[i] < '0' || host[i] > '9' {
				break
			}
		}
	}

	// Validate host ends with ".<baseDomain>"
	expectedSuffix := "." + m.baseDomain
	if len(hostWithoutPort) <= len(expectedSuffix) {
		return ""
	}

	// Check if host ends with the expected suffix
	if hostWithoutPort[len(hostWithoutPort)-len(expectedSuffix):] != expectedSuffix {
		return ""
	}

	// Extract slug by removing the base domain suffix
	slug := hostWithoutPort[:len(hostWithoutPort)-len(expectedSuffix)]

	// Return empty string if there's no subdomain (slug would be empty)
	if slug == "" {
		return ""
	}

	return slug
}

// resolveTenant performs cache-first tenant resolution with database fallback.
//
// Resolution flow:
// 1. Check slug cache for tenant ID (fast path)
// 2. On cache miss, query database for tenant by slug
// 3. Populate cache with DB result (best-effort, errors logged but not returned)
// 4. Return tenant ID
//
// Returns persistence.ErrTenantNotFound if tenant doesn't exist in database.
// Returns wrapped errors for cache read failures or database errors.
//
//nolint:unused // Will be used in subsequent subtask for ServeHTTP implementation
func (m *TenantResolverMiddleware) resolveTenant(ctx context.Context, slug string) (tenant.TenantID, error) {
	// Step 1: Try cache first
	tenantID, err := m.slugCache.Get(ctx, slug)
	if err != nil {
		return "", fmt.Errorf("failed to get tenant from cache: %w", err)
	}

	// Cache hit - return immediately
	if !tenantID.IsEmpty() {
		return tenantID, nil
	}

	// Step 2: Cache miss - query database
	tenantEntity, err := m.tenantRepo.GetBySlug(ctx, slug)
	if err != nil {
		// Propagate not-found error directly for proper HTTP status code handling
		if errors.Is(err, persistence.ErrTenantNotFound) {
			return "", persistence.ErrTenantNotFound
		}
		return "", fmt.Errorf("failed to get tenant from database: %w", err)
	}

	// Step 3: Populate cache (best-effort)
	if cacheErr := m.slugCache.Set(ctx, slug, tenantEntity.ID); cacheErr != nil {
		// Log cache write failures but don't fail the request
		m.logger.Warn("failed to populate slug cache",
			slog.String("slug", slug),
			slog.String("tenant_id", tenantEntity.ID.String()),
			slog.String("error", cacheErr.Error()),
		)
	}

	// Step 4: Return tenant ID from database
	return tenantEntity.ID, nil
}
