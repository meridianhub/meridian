// Package middleware provides HTTP middleware for the gateway service.
package middleware

import (
	"net/http"
	"strings"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// TenantResolver extracts the tenant ID from the subdomain and injects it into the request context.
// It expects hosts in the format: {tenant}.api.meridianhub.cloud
// If no subdomain is found (e.g., api.meridianhub.cloud), it returns an error response.
type TenantResolver struct {
	// BaseDomain is the base domain suffix (e.g., "api.meridianhub.cloud").
	// Requests to this exact domain (without subdomain) are rejected.
	BaseDomain string

	// AllowedHosts is a list of hosts that bypass tenant resolution (e.g., health checks on localhost).
	// These hosts will have requests passed through without tenant context.
	AllowedHosts []string
}

// NewTenantResolver creates a new TenantResolver middleware.
func NewTenantResolver(baseDomain string, allowedHosts []string) *TenantResolver {
	return &TenantResolver{
		BaseDomain:   baseDomain,
		AllowedHosts: allowedHosts,
	}
}

// Middleware returns an HTTP middleware that extracts tenant from subdomain or X-Tenant header.
// For local development, the X-Tenant header can be used instead of subdomain routing.
func (tr *TenantResolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host

		// Strip port if present
		if colonIdx := strings.LastIndex(host, ":"); colonIdx != -1 {
			// Handle IPv6 addresses which have colons
			if bracketIdx := strings.LastIndex(host, "]"); colonIdx > bracketIdx {
				host = host[:colonIdx]
			}
		}

		// Check if this host is in the allowed list (bypass tenant resolution)
		for _, allowed := range tr.AllowedHosts {
			if host == allowed || strings.HasPrefix(host, allowed+":") {
				next.ServeHTTP(w, r)
				return
			}
		}

		var tenantSlug string
		var err error

		// Try to extract tenant from X-Tenant header first (for local development)
		if headerTenant := r.Header.Get("X-Tenant"); headerTenant != "" {
			tenantSlug = headerTenant
		} else {
			// Fall back to subdomain extraction (production mode)
			tenantSlug, err = tr.extractTenant(host)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		// Validate and create tenant ID
		tenantID, err := tenant.NewTenantID(tenantSlug)
		if err != nil {
			http.Error(w, "invalid tenant identifier in subdomain", http.StatusBadRequest)
			return
		}

		// Inject tenant into request context
		ctx := tenant.WithTenant(r.Context(), tenantID)
		r = r.WithContext(ctx)

		// Also set tenant header for downstream services
		r.Header.Set(tenant.TenantIDKey, tenantID.String())

		next.ServeHTTP(w, r)
	})
}

// extractTenant extracts the tenant identifier from the host subdomain.
// For example: "acme.api.meridianhub.cloud" -> "acme"
func (tr *TenantResolver) extractTenant(host string) (string, error) {
	// Normalize to lowercase for comparison
	hostLower := strings.ToLower(host)
	baseDomainLower := strings.ToLower(tr.BaseDomain)

	// Check if host ends with the base domain
	if !strings.HasSuffix(hostLower, baseDomainLower) {
		return "", ErrInvalidHost
	}

	// Check if host is exactly the base domain (no subdomain)
	if hostLower == baseDomainLower {
		return "", ErrMissingTenant
	}

	// Extract subdomain: host = subdomain.baseDomain
	// We need to remove the base domain and the preceding dot
	subdomainPart := host[:len(host)-len(tr.BaseDomain)-1]

	// The subdomain should be a single segment (no additional dots)
	// e.g., "acme" is valid, "acme.extra" would need different handling
	if strings.Contains(subdomainPart, ".") {
		// Take only the first segment as tenant
		parts := strings.Split(subdomainPart, ".")
		subdomainPart = parts[0]
	}

	if subdomainPart == "" {
		return "", ErrMissingTenant
	}

	return subdomainPart, nil
}
