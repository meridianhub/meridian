package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
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

		proxy := httputil.NewSingleHostReverseProxy(target)

		// TODO(8-multi-tenancy.89): Add configurable timeout settings for production resilience
		// Consider: ResponseHeaderTimeout, IdleConnTimeout, MaxIdleConnsPerHost
		// See: https://github.com/meridianhub/meridian/pull/439#discussion_r1901972279

		// Configure the proxy director to add X-Forwarded-Host.
		// Connect protocol headers (Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms)
		// are standard headers (not hop-by-hop) and are preserved by httputil.ReverseProxy.
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			// Set X-Forwarded-Host so backends know the original Host header
			if req.Header.Get("X-Forwarded-Host") == "" {
				req.Header.Set("X-Forwarded-Host", req.Host)
			}
		}

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
