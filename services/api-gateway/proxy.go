package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"

	"github.com/meridianhub/meridian/services/api-gateway/auth"
)

// Identity headers forwarded to backend services.
// These headers are set by the gateway after successful authentication
// and can be trusted by backend services.
const (
	// HeaderUserID contains the authenticated user identifier from JWT claims.
	HeaderUserID = "X-User-ID"
	// HeaderTenantID contains the tenant identifier from JWT claims or API key metadata.
	HeaderTenantID = "X-Tenant-ID"
	// HeaderAuthMethod indicates the authentication method used ("jwt" or "apikey").
	HeaderAuthMethod = "X-Auth-Method"
	// HeaderAuthRoles contains comma-separated roles from JWT claims (optional).
	HeaderAuthRoles = "X-Auth-Roles"
)

// AuthMethod constants for the X-Auth-Method header.
const (
	AuthMethodJWT    = "jwt"
	AuthMethodAPIKey = "apikey"
)

// ProxyHandler routes incoming HTTP requests to backend gRPC services
// based on URL path prefix matching. It supports the Connect protocol
// for HTTP-to-gRPC communication.
type ProxyHandler struct {
	routes []proxyRoute
}

// proxyRoute represents a single routing rule mapping a URL prefix to a backend.
type proxyRoute struct {
	prefix string
	proxy  *httputil.ReverseProxy
}

// NewProxyHandler creates a new ProxyHandler configured with the given backend routes.
// Routes are sorted by prefix length (longest first) to ensure most specific matching.
func NewProxyHandler(backends []BackendRoute) *ProxyHandler {
	routes := make([]proxyRoute, 0, len(backends))

	for _, b := range backends {
		target, err := url.Parse(fmt.Sprintf("http://%s", b.Target))
		if err != nil {
			slog.Warn("skipping invalid backend URL",
				"prefix", b.Prefix,
				"target", b.Target,
				"error", err)
			continue
		}

		// Create ReverseProxy with Rewrite (not the deprecated Director API).
		// Consider adding configurable timeout settings for production resilience:
		// ResponseHeaderTimeout, IdleConnTimeout, MaxIdleConnsPerHost
		//
		// Connect protocol headers (Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms)
		// are standard headers (not hop-by-hop) and are preserved by httputil.ReverseProxy.
		proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
			// Capture any existing X-Forwarded-Host before SetXForwarded overwrites it
			existingXFH := r.In.Header.Get("X-Forwarded-Host")

			r.SetURL(target)
			r.Out.Host = r.In.Host // Preserve original Host (SetURL overwrites it)
			r.SetXForwarded()

			// Restore pre-existing X-Forwarded-Host if the client already set one
			if existingXFH != "" {
				r.Out.Header.Set("X-Forwarded-Host", existingXFH)
			}

			// SECURITY: Strip any incoming identity headers to prevent spoofing.
			// These headers are set only by the gateway after successful authentication.
			r.Out.Header.Del(HeaderUserID)
			r.Out.Header.Del(HeaderTenantID)
			r.Out.Header.Del(HeaderAuthMethod)
			r.Out.Header.Del(HeaderAuthRoles)

			// SECURITY: Strip X-API-Key header to prevent credential leakage to backends.
			r.Out.Header.Del(auth.APIKeyHeader)

			// Add identity headers if the request was authenticated
			addIdentityHeaders(r.Out)
		}}

		routes = append(routes, proxyRoute{
			prefix: b.Prefix,
			proxy:  proxy,
		})
	}

	// Sort routes by prefix length descending (longest prefix first)
	// This ensures most specific routes are matched first
	sort.Slice(routes, func(i, j int) bool {
		return len(routes[i].prefix) > len(routes[j].prefix)
	})

	return &ProxyHandler{routes: routes}
}

// ServeHTTP implements http.Handler and routes requests to the appropriate backend.
// It matches the request path against configured prefixes and forwards to the
// backend with the longest matching prefix. Returns 404 if no route matches.
func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Find matching route by longest prefix (routes are pre-sorted)
	for _, rt := range h.routes {
		if strings.HasPrefix(r.URL.Path, rt.prefix) {
			rt.proxy.ServeHTTP(w, r)
			return
		}
	}

	// No matching route found
	http.Error(w, "Not Found", http.StatusNotFound)
}

// MatchRoute returns the matched prefix for a given path, or empty string if no match.
// This is useful for testing and debugging route matching behavior.
func (h *ProxyHandler) MatchRoute(path string) string {
	for _, rt := range h.routes {
		if strings.HasPrefix(path, rt.prefix) {
			return rt.prefix
		}
	}
	return ""
}

// RouteCount returns the number of configured routes.
func (h *ProxyHandler) RouteCount() int {
	return len(h.routes)
}

// addIdentityHeaders extracts authenticated identity from request context
// and adds the corresponding headers to the outgoing request.
//
// For JWT authentication, it adds:
//   - X-User-ID: user identifier from JWT claims
//   - X-Tenant-ID: tenant identifier from JWT claims
//   - X-Auth-Method: "jwt"
//   - X-Auth-Roles: comma-separated roles from JWT claims (if present)
//
// For API key authentication, it adds:
//   - X-User-ID: API key identity (e.g., "service-a")
//   - X-Auth-Method: "apikey"
//
// If the request is not authenticated (no identity in context), no headers are added.
func addIdentityHeaders(req *http.Request) {
	ctx := req.Context()

	// Check for JWT authentication first
	if userID, ok := auth.GetUserIDFromContext(ctx); ok && userID != "" {
		req.Header.Set(HeaderUserID, userID)
		req.Header.Set(HeaderAuthMethod, AuthMethodJWT)

		// Add tenant ID if present
		if tenantID, ok := auth.GetTenantIDFromContext(ctx); ok && tenantID != "" {
			req.Header.Set(HeaderTenantID, tenantID)
		}

		// Add roles if present (comma-separated)
		if roles, ok := auth.GetRolesFromContext(ctx); ok && len(roles) > 0 {
			req.Header.Set(HeaderAuthRoles, strings.Join(roles, ","))
		}

		return
	}

	// Check for API key authentication
	if identity := auth.GetAPIKeyIdentity(ctx); identity != "" {
		req.Header.Set(HeaderUserID, identity)
		req.Header.Set(HeaderAuthMethod, AuthMethodAPIKey)

		// For RPC-validated API keys, tenant ID is available in context
		if tenantID, ok := auth.GetTenantIDFromContext(ctx); ok && tenantID != "" {
			req.Header.Set(HeaderTenantID, tenantID)
		}

		return
	}

	// No authenticated identity - headers remain unset
}
