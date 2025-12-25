package server

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/gateway/internal/config"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ErrNoBackendFound is returned when no backend matches the request path.
var ErrNoBackendFound = errors.New("no backend found for path")

// ProxyHandler handles proxying requests to backend gRPC services.
// Currently implements basic HTTP proxying; gRPC-web or connect-go can be added later.
type ProxyHandler struct {
	config *config.Config
	client *http.Client
	logger *slog.Logger
}

// NewProxyHandler creates a new proxy handler.
func NewProxyHandler(cfg *config.Config, logger *slog.Logger) *ProxyHandler {
	return &ProxyHandler{
		config: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		logger: logger,
	}
}

// ServeHTTP handles incoming HTTP requests and routes them to backend services.
func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Get tenant from context (should be set by TenantResolver middleware)
	tenantID, ok := tenant.FromContext(r.Context())
	if !ok {
		http.Error(w, "tenant context required", http.StatusBadRequest)
		return
	}

	// Route to appropriate backend based on path
	backend, err := p.resolveBackend(r.URL.Path)
	if err != nil {
		p.logger.Debug("no backend found for path",
			"path", r.URL.Path,
			"tenant", tenantID.String())
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}

	// Build target URL
	targetURL := fmt.Sprintf("http://%s%s", backend.Target, r.URL.Path)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// Create proxied request
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		p.logger.Error("failed to create proxy request",
			"error", err,
			"target", targetURL)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Copy headers from original request
	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Ensure tenant header is set for downstream services
	proxyReq.Header.Set(tenant.TenantIDKey, tenantID.String())

	// Add forwarding headers
	proxyReq.Header.Set("X-Forwarded-For", r.RemoteAddr)
	proxyReq.Header.Set("X-Forwarded-Host", r.Host)
	proxyReq.Header.Set("X-Forwarded-Proto", getScheme(r))

	// Execute proxy request
	resp, err := p.client.Do(proxyReq)
	if err != nil {
		p.logger.Error("proxy request failed",
			"error", err,
			"target", targetURL,
			"tenant", tenantID.String())
		http.Error(w, "backend service unavailable", http.StatusBadGateway)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	if _, err := io.Copy(w, resp.Body); err != nil {
		p.logger.Error("failed to copy response body",
			"error", err,
			"target", targetURL)
	}
}

// resolveBackend finds the appropriate backend for the given path.
func (p *ProxyHandler) resolveBackend(path string) (*config.BackendConfig, error) {
	for _, backend := range p.config.Backends {
		if strings.HasPrefix(path, backend.PathPrefix) {
			return &backend, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNoBackendFound, path)
}

// getScheme determines the request scheme.
func getScheme(r *http.Request) string {
	// Check common proxy headers
	if scheme := r.Header.Get("X-Forwarded-Proto"); scheme != "" {
		return scheme
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
