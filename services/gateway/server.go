package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/gateway/auth"
	gwhealth "github.com/meridianhub/meridian/services/gateway/health"
	"github.com/meridianhub/meridian/shared/pkg/health"
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
	healthChecker         *gwhealth.GatewayHealthChecker
	transcoderHandler     http.Handler
}

// ServerOption is a functional option for configuring the server.
type ServerOption func(*Server)

// WithAuthMiddleware sets the authentication middleware for the server.
func WithAuthMiddleware(authMiddleware *auth.CombinedAuthMiddleware) ServerOption {
	return func(s *Server) {
		s.authMiddleware = authMiddleware
	}
}

// WithHealthChecker sets the health checker for readiness probes.
func WithHealthChecker(healthChecker *gwhealth.GatewayHealthChecker) ServerOption {
	return func(s *Server) {
		s.healthChecker = healthChecker
	}
}

// WithTranscoder sets the Vanguard transcoder as the API handler, replacing
// the legacy prefix-based reverse proxy. When set, all /api/ requests are
// dispatched through the transcoder rather than NewProxyHandler.
func WithTranscoder(handler http.Handler) ServerOption {
	return func(s *Server) {
		s.transcoderHandler = handler
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
// 4. Transcoder (Vanguard REST↔gRPC transcoder) or legacy proxy handler
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

	// API routes - with auth and tenant middleware chain.
	// Prefer the Vanguard transcoder when configured; fall back to the legacy
	// prefix-based reverse proxy when Backends are provided; otherwise use a
	// placeholder that returns 501 Not Implemented.
	//
	// For the transcoder path, identityHeaderMiddleware wraps the handler to
	// strip spoofed incoming identity headers and inject authenticated identity
	// headers from the request context (the same security pattern used by
	// NewProxyHandler via its custom Director). This ensures backends always
	// receive X-User-ID, X-Tenant-ID, X-Auth-Method, and X-Auth-Roles from
	// the gateway's authentication result, never from the client.
	var apiHandler http.Handler
	switch {
	case s.transcoderHandler != nil:
		apiHandler = identityHeaderMiddleware(s.transcoderHandler)
	case len(s.config.Backends) > 0:
		apiHandler = NewProxyHandler(s.config.Backends)
	default:
		apiHandler = http.HandlerFunc(s.handleAPI)
	}

	// Build middleware chain: auth → tenant → tenant_authz → transcoder/proxy
	handler := http.StripPrefix("/api", apiHandler)

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
//
// Health status to HTTP mapping:
//   - Healthy: 200 OK (all critical dependencies up)
//   - Degraded: 200 OK (critical dependencies up, optional may be down)
//   - Unhealthy: 503 Service Unavailable (critical dependencies down)
//   - Unknown: 503 Service Unavailable (cannot determine)
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	// If no health checker configured, fail open (return ready)
	// This maintains backwards compatibility during migration
	if s.healthChecker == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("READY")); err != nil {
			s.logger.Warn("failed to write readiness response",
				"error", err,
				"endpoint", r.URL.Path,
				"remote_addr", r.RemoteAddr)
		}
		return
	}

	// Execute health checks with timeout
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	report := s.healthChecker.Check(ctx)
	overallStatus := report.OverallStatus()

	// Map health status to HTTP status
	if overallStatus == health.StatusHealthy || overallStatus == health.StatusDegraded {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("READY")); err != nil {
			s.logger.Warn("failed to write readiness response",
				"error", err,
				"endpoint", r.URL.Path,
				"remote_addr", r.RemoteAddr)
		}

		// Log degraded status as warning
		if overallStatus == health.StatusDegraded {
			s.logHealthCheckResult(ctx, report, overallStatus, slog.LevelWarn)
		}
	} else {
		// Unhealthy or Unknown - not ready for traffic
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte("NOT READY")); err != nil {
			s.logger.Warn("failed to write readiness response",
				"error", err,
				"endpoint", r.URL.Path,
				"remote_addr", r.RemoteAddr)
		}

		s.logHealthCheckResult(ctx, report, overallStatus, slog.LevelError)
	}
}

// logHealthCheckResult logs the health check result with structured details.
func (s *Server) logHealthCheckResult(ctx context.Context, report *health.Report, overallStatus health.Status, level slog.Level) {
	// Build component status map for structured logging
	componentStatuses := make(map[string]string)
	for _, comp := range report.Components {
		componentStatuses[comp.Name] = comp.Status.String()
	}

	s.logger.Log(ctx, level, "gateway readiness check",
		"overall_status", overallStatus.String(),
		"component_count", len(report.Components),
		"components", componentStatuses)

	// Log individual component failures at error level
	if level >= slog.LevelWarn {
		for _, comp := range report.Components {
			if comp.Status == health.StatusUnhealthy || comp.Status == health.StatusUnknown {
				s.logger.Error("component unhealthy",
					"component", comp.Name,
					"status", comp.Status.String(),
					"message", comp.Message,
					"response_time_ms", comp.ResponseTime.Milliseconds(),
					"error", comp.Error)
			}
		}
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
