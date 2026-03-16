package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// baseDomainAccessKeyType is an unexported type for the base domain access context key,
// preventing collisions with other packages' context keys.
type baseDomainAccessKeyType struct{}

var baseDomainAccessKey = baseDomainAccessKeyType{}

// IsBaseDomainAccess reports whether the request was made to the base domain
// (i.e., no tenant subdomain was present). Tools can query this to determine
// whether to operate in multi-tenant discovery mode vs. single-tenant mode.
func IsBaseDomainAccess(ctx context.Context) bool {
	v, _ := ctx.Value(baseDomainAccessKey).(bool)
	return v
}

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
// fallbackBaseURL is used when the request Host cannot be determined, to build
// dynamic 401 metadata responses.
func (m *TenantSubdomainMiddleware) Handler(validator ClaimsBearerValidator, fallbackBaseURL string, next http.Handler) http.Handler {
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
			// Annotate context so tools can detect base domain access mode.
			ctx := context.WithValue(r.Context(), baseDomainAccessKey, true)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Extract and validate bearer token to get tenant claim
		token, err := extractBearerFromHeader(r)
		if err != nil {
			m.logger.Debug("subdomain validation: no bearer token", "host", r.Host)
			writeSubdomainError(w, r, fallbackBaseURL)
			return
		}

		tenantID, err := validator.ValidateBearerWithTenant(token)
		if err != nil {
			m.logger.Debug("subdomain validation: token validation failed",
				"error", err, "host", r.Host)
			writeSubdomainError(w, r, fallbackBaseURL)
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

// writeSubdomainError writes a 401 with auth metadata derived from the request.
func writeSubdomainError(w http.ResponseWriter, r *http.Request, fallbackBaseURL string) {
	base := baseURLFromRequest(r, fallbackBaseURL)
	meta := Metadata{
		AuthorizationURL: base + "/oauth/authorize",
		TokenURL:         base + "/oauth/token",
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="meridian-mcp"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(meta)
}
