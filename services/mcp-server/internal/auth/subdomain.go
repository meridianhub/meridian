package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// Subdomain validation errors.
var (
	// ErrSubdomainMismatch is returned when the subdomain tenant does not match the JWT tenant.
	ErrSubdomainMismatch = errors.New("subdomain tenant does not match authenticated tenant")
	// ErrMissingSubdomain is returned when no tenant subdomain is present in the Host header.
	ErrMissingSubdomain = errors.New("missing tenant subdomain")
)

// ClaimsBearerValidator extends BearerValidator by also returning the tenant ID
// from the validated token. This enables subdomain-to-JWT tenant matching.
type ClaimsBearerValidator interface {
	BearerValidator
	// ValidateBearerWithTenant validates the token and returns the tenant ID claim.
	ValidateBearerWithTenant(token string) (tenantID string, err error)
}

// TenantSubdomainMiddleware validates that the request's subdomain matches
// the tenant identity in the authenticated user's JWT. This prevents a user
// authenticated for tenant A from accessing tenant B's MCP endpoint via
// tenant B's subdomain.
//
// When baseDomain is empty or the request is to localhost, subdomain validation
// is skipped (development mode).
type TenantSubdomainMiddleware struct {
	baseDomain string
	logger     *slog.Logger
}

// NewTenantSubdomainMiddleware creates a middleware that validates subdomain
// tenant matches JWT tenant. baseDomain is the root domain (e.g., "demo.meridianhub.cloud").
// When baseDomain is empty, subdomain validation is disabled.
func NewTenantSubdomainMiddleware(baseDomain string, logger *slog.Logger) *TenantSubdomainMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	if baseDomain == "" {
		logger.Warn("subdomain validation disabled — MCP_BASE_DOMAIN not set")
	}
	return &TenantSubdomainMiddleware{
		baseDomain: baseDomain,
		logger:     logger,
	}
}

// Handler wraps an http.Handler, enforcing that the subdomain tenant matches
// the JWT tenant claim. If baseDomain is not configured, validation is skipped.
func (m *TenantSubdomainMiddleware) Handler(validator ClaimsBearerValidator, meta Metadata, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If no base domain configured, skip subdomain validation (dev mode)
		if m.baseDomain == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Extract subdomain from Host header
		subdomainSlug := extractSubdomain(r.Host, m.baseDomain)
		if subdomainSlug == "" {
			// No subdomain present — request is to the base domain directly.
			// Allow it through (no tenant scoping needed).
			next.ServeHTTP(w, r)
			return
		}

		// Extract and validate bearer token to get tenant claim
		token, err := extractBearerFromHeader(r)
		if err != nil {
			m.logger.Debug("subdomain validation: no bearer token", "host", r.Host)
			writeSubdomainError(w, meta)
			return
		}

		tenantID, err := validator.ValidateBearerWithTenant(token)
		if err != nil {
			m.logger.Debug("subdomain validation: token validation failed",
				"error", err, "host", r.Host)
			writeSubdomainError(w, meta)
			return
		}

		// Compare subdomain slug against JWT tenant ID
		if tenantID != subdomainSlug {
			m.logger.Warn("subdomain tenant mismatch",
				"subdomain", subdomainSlug,
				"jwt_tenant", tenantID,
				"host", r.Host,
			)
			http.Error(w, "Forbidden: tenant mismatch", http.StatusForbidden)
			return
		}

		m.logger.Debug("subdomain tenant validated",
			"tenant", subdomainSlug,
			"host", r.Host,
		)

		next.ServeHTTP(w, r)
	})
}

// extractSubdomain extracts the tenant slug from the Host header given a base domain.
// Returns empty string if no subdomain is present or the host doesn't match the base domain.
func extractSubdomain(hostHeader, baseDomain string) string {
	if hostHeader == "" || baseDomain == "" {
		return ""
	}

	// Strip port from host
	host := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = h
	}

	// Skip localhost
	if host == "localhost" || host == "127.0.0.1" {
		return ""
	}

	// Check host ends with ".<baseDomain>"
	suffix := "." + baseDomain
	if !strings.HasSuffix(host, suffix) {
		return ""
	}

	slug := host[:len(host)-len(suffix)]
	if slug == "" {
		return ""
	}

	return slug
}

// writeSubdomainError writes a 401 with auth metadata for subdomain validation failures.
func writeSubdomainError(w http.ResponseWriter, meta Metadata) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="meridian-mcp"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(meta)
}
