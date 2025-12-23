// Package gateway provides HTTP middleware for API gateway functionality.
//
// Example usage:
//
//	resolver, err := NewTenantResolverMiddleware(cache, repo, "api.meridian.io", logger)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	handler := resolver.Handler(appHandler)
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Configuration errors.
var (
	ErrNilSlugCache    = errors.New("slug cache cannot be nil")
	ErrNilTenantRepo   = errors.New("tenant repository cannot be nil")
	ErrEmptyBaseDomain = errors.New("base domain cannot be empty")
	ErrNilLogger       = errors.New("logger cannot be nil")
)

// ErrTenantNotFound is returned when a tenant cannot be found by slug.
var ErrTenantNotFound = errors.New("tenant not found")

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
	slugCache slugCache,
	tenantRepo tenantRepository,
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

// Handler returns an http.Handler that performs tenant resolution.
// This method will extract the tenant slug from the Host header,
// resolve it to a tenant ID, and inject it into the request context.
func (m *TenantResolverMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Step 1: Extract slug from Host header
		slug := m.extractSlug(r.Host)
		if slug == "" {
			m.logger.Warn("invalid subdomain in request",
				slog.String("host", r.Host),
			)
			http.Error(w, "Invalid subdomain", http.StatusNotFound)
			return
		}

		// Step 2: Resolve tenant ID (cache-first with DB fallback)
		startTime := time.Now()
		tenantID, err := m.resolveTenant(ctx, slug)
		resolutionTimeMs := time.Since(startTime).Milliseconds()

		if err != nil {
			// Log resolution failure with structured fields
			m.logger.Warn("tenant resolution failed",
				slog.String("tenant_slug", slug),
				slog.String("error", err.Error()),
				slog.Int64("resolution_time_ms", resolutionTimeMs),
			)
			http.Error(w, "Tenant not found", http.StatusNotFound)
			return
		}

		// Step 3: Log successful resolution
		m.logger.Debug("tenant resolved successfully",
			slog.String("tenant_slug", slug),
			slog.String("tenant_id", tenantID.String()),
			slog.Int64("resolution_time_ms", resolutionTimeMs),
		)

		// Step 4: Inject tenant ID into request header
		r.Header.Set(tenant.TenantIDKey, string(tenantID))

		// Step 5: Add tenant to context
		ctx = tenant.WithTenant(ctx, tenantID)

		// Step 6: Update request with new context
		r = r.WithContext(ctx)

		// Step 7: Call next handler
		next.ServeHTTP(w, r)
	})
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
func (m *TenantResolverMiddleware) extractSlug(hostHeader string) string {
	if hostHeader == "" {
		return ""
	}

	// Use net.SplitHostPort for robust port handling (IPv6-safe)
	host := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = h
	}
	// Note: SplitHostPort returns error for hosts without ports, which is fine

	// Validate host ends with ".<baseDomain>"
	expectedSuffix := "." + m.baseDomain
	if len(host) <= len(expectedSuffix) {
		return ""
	}

	// Check if host ends with the expected suffix
	if host[len(host)-len(expectedSuffix):] != expectedSuffix {
		return ""
	}

	// Extract slug by removing the base domain suffix
	slug := host[:len(host)-len(expectedSuffix)]

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
// 2. On cache read failure, log warning and fall through to database lookup
// 3. On cache miss, query database for tenant by slug
// 4. Populate cache with DB result (best-effort, errors logged but not returned)
// 5. Return tenant ID
//
// Returns ErrTenantNotFound if tenant doesn't exist in database.
// Returns wrapped errors for database errors.
func (m *TenantResolverMiddleware) resolveTenant(ctx context.Context, slug string) (tenant.TenantID, error) {
	if slug == "" {
		return "", ErrTenantNotFound // fast-fail for empty slug
	}

	// Step 1: Try cache first (best-effort)
	tenantID, err := m.slugCache.Get(ctx, slug)
	if err != nil {
		// Log cache read failure but don't fail the request
		// Fall through to database lookup for resilience
		m.logger.Warn("cache read failed, falling back to database",
			slog.String("slug", slug),
			slog.String("error", err.Error()),
		)
	} else if !tenantID.IsEmpty() {
		// Cache hit - return immediately
		return tenantID, nil
	}

	// Step 2: Cache miss or error - query database
	tenantEntity, err := m.tenantRepo.GetBySlug(ctx, slug)
	if err != nil {
		// Propagate not-found error directly for proper HTTP status code handling
		if errors.Is(err, ErrTenantNotFound) {
			return "", ErrTenantNotFound
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
