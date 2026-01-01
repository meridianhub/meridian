package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/meridianhub/meridian/services/gateway/auth"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	platformgateway "github.com/meridianhub/meridian/shared/platform/gateway"
)

// Server is the HTTP server for the gateway service.
type Server struct {
	config                *Config
	logger                *slog.Logger
	httpServer            *http.Server
	httpServerMu          sync.RWMutex // Guards httpServer field
	mux                   *http.ServeMux
	tenantResolver        *platformgateway.TenantResolverMiddleware
	authMiddleware        *auth.CombinedAuthMiddleware
	tenantAuthzMiddleware *auth.TenantAuthorizationMiddleware
}

// ServerOption is a functional option for configuring the server.
type ServerOption func(*Server)

// WithAuthMiddleware sets the authentication middleware for the server.
func WithAuthMiddleware(authMiddleware *auth.CombinedAuthMiddleware) ServerOption {
	return func(s *Server) {
		s.authMiddleware = authMiddleware
	}
}

// NewServer creates a new gateway HTTP server with the given configuration.
// The tenantResolver parameter is optional - if nil, all routes bypass tenant resolution.
// Additional options can configure authentication middleware.
func NewServer(config *Config, logger *slog.Logger, tenantResolver *platformgateway.TenantResolverMiddleware, opts ...ServerOption) *Server {
	mux := http.NewServeMux()

	s := &Server{
		config:         config,
		logger:         logger,
		mux:            mux,
		tenantResolver: tenantResolver,
	}

	// Apply options
	for _, opt := range opts {
		opt(s)
	}

	// Create tenant authorization middleware if auth is configured
	if s.authMiddleware != nil {
		s.tenantAuthzMiddleware = auth.NewTenantAuthorizationMiddleware(logger)
	}

	// Register routes
	s.registerRoutes()

	return s
}

// registerRoutes sets up the HTTP routes for the gateway.
//
// CRITICAL: Health endpoints (/health, /ready) are registered directly on the main mux
// WITHOUT any middleware. This is required for K8s probes which do not provide
// authentication credentials or tenant context.
//
// API routes go through the middleware chain in this order:
// 1. Auth middleware (validates JWT or API key, injects identity into context)
// 2. Tenant middleware (resolves tenant from subdomain, injects tenant into context)
// 3. Tenant authorization middleware (verifies JWT tenant matches resolved tenant)
// 4. Proxy handler (routes to backend services)
//
// This order ensures:
// - 401 Unauthorized is returned for missing/invalid credentials BEFORE tenant resolution
// - 403 Forbidden is returned if authenticated but JWT tenant doesn't match subdomain tenant
// - API keys bypass tenant authorization (service-to-service auth)
func (s *Server) registerRoutes() {
	// Health check endpoints - NO middleware (required for K8s probes)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /ready", s.handleReady)

	// Legacy health endpoints for backwards compatibility
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /readyz", s.handleReady)

	// API routes - with auth and tenant middleware chain
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/", s.handleAPI)

	// Build middleware chain: auth → tenant → tenant_authz → handler
	handler := http.StripPrefix("/api", apiMux)

	// Layer 3 (innermost): Tenant authorization (verify JWT tenant matches subdomain)
	if s.tenantAuthzMiddleware != nil {
		handler = s.tenantAuthzMiddleware.Handler(handler)
	}

	// Layer 2: Tenant resolution (extract tenant from subdomain/header)
	if s.tenantResolver != nil {
		handler = s.tenantResolver.Handler(handler)
	}

	// Layer 1 (outermost): Authentication (validate JWT or API key)
	if s.authMiddleware != nil {
		handler = s.authMiddleware.Handler(handler)
	}

	s.mux.Handle("/api/", handler)
}

// handleHealth is the liveness probe endpoint.
// Returns 200 OK if the server is alive.
// This endpoint does NOT require tenant context.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		s.logger.Warn("failed to write health response",
			"error", err,
			"endpoint", r.URL.Path,
			"remote_addr", r.RemoteAddr)
	}
}

// handleReady is the readiness probe endpoint.
// Returns 200 OK if the server is ready to accept traffic.
// This endpoint does NOT require tenant context.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	// TODO: Add actual readiness checks (database, redis, backends)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("READY")); err != nil {
		s.logger.Warn("failed to write readiness response",
			"error", err,
			"endpoint", r.URL.Path,
			"remote_addr", r.RemoteAddr)
	}
}

// handleAPI is a placeholder handler for API routes.
// This will be replaced with actual routing logic in future tasks.
func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("received API request",
		"method", r.Method,
		"path", r.URL.Path,
		"host", r.Host)

	// Placeholder response - actual routing will be implemented in future tasks
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"gateway routing not yet implemented"}`)); err != nil {
		s.logger.Warn("failed to write API response",
			"error", err,
			"endpoint", r.URL.Path,
			"remote_addr", r.RemoteAddr)
	}
}

// Start starts the HTTP server and blocks until the server stops.
// It returns an error if the server fails to start or encounters an error.
func (s *Server) Start(ctx context.Context) error {
	address := fmt.Sprintf(":%d", s.config.Port)

	httpServer := &http.Server{
		Addr:              address,
		Handler:           s.mux,
		ReadHeaderTimeout: defaults.DefaultHTTPReadHeaderTimeout,
		ReadTimeout:       defaults.DefaultHTTPReadTimeout,
		WriteTimeout:      defaults.DefaultHTTPWriteTimeout,
		IdleTimeout:       2 * defaults.DefaultHTTPIdleTimeout, // Extended for gateway proxying
	}

	// Store httpServer with mutex protection
	s.httpServerMu.Lock()
	s.httpServer = httpServer
	s.httpServerMu.Unlock()

	// Create listener first to ensure port binding before signaling ready
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to bind to %s: %w", address, err)
	}

	s.logger.Info("starting HTTP server",
		"address", address,
		"local_dev_mode", s.config.LocalDevMode,
		"base_domain", s.config.BaseDomain,
		"backend_routes", len(s.config.Backends))

	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP server error: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the HTTP server and cleans up resources.
func (s *Server) Shutdown(ctx context.Context) error {
	s.httpServerMu.RLock()
	httpServer := s.httpServer
	s.httpServerMu.RUnlock()

	if httpServer == nil {
		return nil
	}

	s.logger.Info("shutting down HTTP server...")

	if err := httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %w", err)
	}

	// Clean up auth middleware resources
	if s.authMiddleware != nil {
		s.authMiddleware.Close()
	}

	s.logger.Info("HTTP server stopped")
	return nil
}
