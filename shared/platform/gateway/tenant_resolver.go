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
	"regexp"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// TenantSlugHeader is the header name used for local development mode.
// When LOCAL_DEV_MODE is enabled, this header can be used to specify
// the tenant slug directly, bypassing subdomain-based resolution.
const TenantSlugHeader = "X-Tenant-Slug"

// Configuration errors.
var (
	ErrNilSlugCache    = errors.New("slug cache cannot be nil")
	ErrNilTenantRepo   = errors.New("tenant repository cannot be nil")
	ErrEmptyBaseDomain = errors.New("base domain cannot be empty")
	ErrNilLogger       = errors.New("logger cannot be nil")
)

// ErrTenantNotFound is returned when a tenant cannot be found by slug.
var ErrTenantNotFound = errors.New("tenant not found")

// slugPattern validates tenant slugs extracted from subdomains.
// This pattern allows periods for multi-level subdomains (e.g., "acme.staging")
// which differs from domain.slugPattern that only validates single-level slugs.
// The gateway handles subdomain routing while the domain validates the actual tenant slug.
//
// Examples: "acme", "acme-corp", "acme.staging", "my-company.dev"
// Invalid: "-acme", "acme-", "acme--corp", ".acme", "acme.", "ACME"
var slugPattern = regexp.MustCompile(`^[a-z0-9]+([-.][a-z0-9]+)*$`)

// isValidSlug validates that a slug matches the allowed format.
// This prevents potential injection attacks from malicious subdomains.
func isValidSlug(slug string) bool {
	return slugPattern.MatchString(slug)
}

// slugCache defines the caching interface for slug-to-tenant-ID mappings.
type slugCache interface {
	Get(ctx context.Context, slug string) (tenant.TenantID, error)
	Set(ctx context.Context, slug string, tenantID tenant.TenantID) error
}

// tenantRepository defines the repository interface for tenant lookups.
type tenantRepository interface {
	GetBySlug(ctx context.Context, slug string) (*domain.Tenant, error)
}

// platformPaths lists URL path prefixes that operate at the platform level
// (e.g., tenant creation, identity provider endpoints) and do not require
// tenant context. Requests matching these prefixes bypass tenant resolution
// entirely.
var platformPaths = []string{
	"/v1/tenants",                        // REST transcoding path
	"/meridian.tenant.v1.TenantService/", // Connect/gRPC path
}

// IsPlatformPath returns true if the request path is a platform-level endpoint
// that should bypass tenant resolution and tenant authorization.
// Platform paths (e.g., ListTenants) are bootstrap endpoints that only require
// authentication — the endpoint itself handles access control based on the
// caller's identity.
func IsPlatformPath(path string) bool {
	for _, prefix := range platformPaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// TenantResolverMiddleware extracts tenant information from the Host header
// and injects the tenant ID into the request context.
//
// It follows this resolution flow:
// 0. Skip resolution for platform-level paths (e.g., /v1/tenants)
// 1. In LOCAL_DEV_MODE, check for X-Tenant-Slug header first
// 2. Extract subdomain slug from Host header (e.g., "acme" from "acme.meridian.com")
// 3. Check slug cache for tenant ID
// 4. On cache miss, query tenant repository and populate cache
// 5. Inject tenant ID into request context via x-tenant-id header
type TenantResolverMiddleware struct {
	slugCache    slugCache
	tenantRepo   tenantRepository
	baseDomain   string
	logger       *slog.Logger
	localDevMode bool
}

// NewTenantResolverMiddleware creates a new tenant resolver middleware.
// All parameters except localDevMode are required and validated.
//
// When localDevMode is true, the middleware will accept the X-Tenant-Slug header
// for tenant identification, which is useful for local development and testing.
// In production, this should always be false.
func NewTenantResolverMiddleware(
	slugCache slugCache,
	tenantRepo tenantRepository,
	baseDomain string,
	logger *slog.Logger,
	localDevMode bool,
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

	if localDevMode {
		logger.Warn("local dev mode enabled - X-Tenant-Slug header will be accepted",
			slog.String("header", TenantSlugHeader))
	}

	return &TenantResolverMiddleware{
		slugCache:    slugCache,
		tenantRepo:   tenantRepo,
		baseDomain:   baseDomain,
		logger:       logger,
		localDevMode: localDevMode,
	}, nil
}

// extractSlugFromRequest extracts and validates the tenant slug from request
// headers (local dev mode) or subdomain. Returns the slug or writes an HTTP
// error response and returns empty string.
func (m *TenantResolverMiddleware) extractSlugFromRequest(w http.ResponseWriter, r *http.Request) string {
	// Check for X-Tenant-Slug header in local dev mode
	if m.localDevMode {
		slug := r.Header.Get(TenantSlugHeader)
		if slug != "" {
			if !isValidSlug(slug) {
				m.logger.Warn("invalid slug in X-Tenant-Slug header",
					slog.String("slug", slug),
				)
				http.Error(w, "Invalid tenant slug", http.StatusBadRequest)
				return ""
			}
			m.logger.Debug("using X-Tenant-Slug header (LOCAL_DEV_MODE)",
				slog.String("slug", slug))
			return slug
		}
	}

	// Fall back to subdomain extraction
	slug := m.extractSlug(r.Host)
	if slug == "" {
		m.logger.Warn("invalid subdomain in request",
			slog.String("host", r.Host),
		)
		http.Error(w, "Invalid subdomain", http.StatusNotFound)
		return ""
	}
	return slug
}

// extractSlugFromRequestOptional extracts the tenant slug from the request
// without writing HTTP errors. Returns the slug if found, or empty string
// if no slug is present or the slug is invalid.
func (m *TenantResolverMiddleware) extractSlugFromRequestOptional(r *http.Request) string {
	// Check for X-Tenant-Slug header in local dev mode
	if m.localDevMode {
		slug := r.Header.Get(TenantSlugHeader)
		if slug != "" {
			if !isValidSlug(slug) {
				m.logger.Debug("invalid slug in X-Tenant-Slug header (optional mode)",
					slog.String("slug", slug),
				)
				return ""
			}
			m.logger.Debug("using X-Tenant-Slug header (LOCAL_DEV_MODE, optional mode)",
				slog.String("slug", slug))
			return slug
		}
	}

	// Fall back to subdomain extraction
	return m.extractSlug(r.Host)
}

// HandlerOptionalTenant returns an http.Handler that performs optional tenant resolution.
// Unlike Handler, this method:
//   - Does NOT skip resolution based on platformPaths
//   - Does NOT return errors when no tenant can be resolved
//   - Passes through to the next handler regardless of resolution outcome
//
// If a valid tenant subdomain is present, the tenant is resolved and injected
// into the request context. Otherwise, the request proceeds without tenant context.
func (m *TenantResolverMiddleware) HandlerOptionalTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := m.extractSlugFromRequestOptional(r)
		if slug == "" {
			next.ServeHTTP(w, r)
			return
		}

		ctx := r.Context()
		tenantID, err := m.resolveTenant(ctx, slug)
		if err != nil {
			m.logger.Debug("optional tenant resolution failed, proceeding without tenant",
				slog.String("slug", slug),
				slog.String("error", err.Error()),
			)
			next.ServeHTTP(w, r)
			return
		}

		m.logger.Debug("optional tenant resolved successfully",
			slog.String("tenant_slug", slug),
			slog.String("tenant_id", tenantID.String()),
		)

		r.Header.Set(tenant.TenantIDKey, string(tenantID))
		ctx = tenant.WithTenant(ctx, tenantID)
		ctx = tenant.WithSlug(ctx, slug)
		r = r.WithContext(ctx)

		next.ServeHTTP(w, r)
	})
}

// Handler returns an http.Handler that performs tenant resolution.
// This method will extract the tenant slug from the Host header,
// resolve it to a tenant ID, and inject it into the request context.
//
// Platform-level paths (e.g., /v1/tenants) bypass tenant resolution entirely.
// In local dev mode (LOCAL_DEV_MODE=true), the X-Tenant-Slug header is checked first.
// If the header is present, it takes precedence over subdomain-based resolution.
func (m *TenantResolverMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip tenant resolution for platform-level endpoints
		if IsPlatformPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		slug := m.extractSlugFromRequest(w, r)
		if slug == "" {
			return // error already written by extractSlugFromRequest
		}

		// Resolve tenant ID (cache-first with DB fallback)
		ctx := r.Context()
		startTime := time.Now()
		tenantID, err := m.resolveTenant(ctx, slug)
		resolutionTimeMs := time.Since(startTime).Milliseconds()

		if err != nil {
			m.handleResolutionError(w, slug, err, resolutionTimeMs)
			return
		}

		m.logger.Debug("tenant resolved successfully",
			slog.String("tenant_slug", slug),
			slog.String("tenant_id", tenantID.String()),
			slog.Int64("resolution_time_ms", resolutionTimeMs),
		)

		// Step 5: Inject tenant ID into request header
		r.Header.Set(tenant.TenantIDKey, string(tenantID))

		// Step 6: Add tenant ID and slug to context
		ctx = tenant.WithTenant(ctx, tenantID)
		ctx = tenant.WithSlug(ctx, slug)

		// Step 7: Update request with new context
		r = r.WithContext(ctx)

		// Step 8: Call next handler
		next.ServeHTTP(w, r)
	})
}

// handleResolutionError writes the appropriate HTTP error for a tenant resolution failure.
func (m *TenantResolverMiddleware) handleResolutionError(w http.ResponseWriter, slug string, err error, resolutionTimeMs int64) {
	if errors.Is(err, ErrTenantNotFound) {
		m.logger.Warn("tenant not found",
			slog.String("tenant_slug", slug),
			slog.Int64("resolution_time_ms", resolutionTimeMs),
		)
		http.Error(w, "Tenant not found", http.StatusNotFound)
	} else {
		m.logger.Error("database error during tenant resolution",
			slog.String("tenant_slug", slug),
			slog.String("error", err.Error()),
			slog.Int64("resolution_time_ms", resolutionTimeMs),
		)
		http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
	}
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
	if !strings.HasSuffix(host, expectedSuffix) {
		return ""
	}

	// Extract slug by removing the base domain suffix
	slug := host[:len(host)-len(expectedSuffix)]

	// Return empty string if there's no subdomain (slug would be empty)
	if slug == "" {
		return ""
	}

	// Validate slug format for security
	if !isValidSlug(slug) {
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
		// Check for domain-layer not-found error and wrap it in gateway error
		if errors.Is(err, domain.ErrNotFound) {
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
