package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/api-gateway/auth"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	gwhealth "github.com/meridianhub/meridian/services/api-gateway/health"
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
	eventStreamHandler    *eventstream.Handler
	rawEventStreamHandler http.Handler // used by tests and WithEventStreamHandlerHTTP
	versionInfo           *VersionInfo
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
// the legacy prefix-based reverse proxy. When set, all API requests are
// dispatched through the transcoder rather than NewProxyHandler.
func WithTranscoder(handler http.Handler) ServerOption {
	return func(s *Server) {
		s.transcoderHandler = handler
	}
}

// WithEventStreamHandler sets the WebSocket event stream handler. When set,
// the GET /ws/events route is registered with the full auth middleware chain.
func WithEventStreamHandler(handler *eventstream.Handler) ServerOption {
	return func(s *Server) {
		s.eventStreamHandler = handler
	}
}

// WithEventStreamHandlerHTTP registers an arbitrary http.Handler on GET /ws/events.
// This is primarily used in tests where a real *eventstream.Handler is not needed.
// Production code should use WithEventStreamHandler.
func WithEventStreamHandlerHTTP(handler http.Handler) ServerOption {
	return func(s *Server) {
		s.rawEventStreamHandler = handler
	}
}

// WithVersionInfo sets the build version metadata returned by the /version endpoint.
func WithVersionInfo(info *VersionInfo) ServerOption {
	return func(s *Server) {
		s.versionInfo = info
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
	// Health check endpoints - NO middleware (required for K8s probes).
	// Registered without method constraints so they take precedence over the
	// "/" catch-all for ALL methods, returning 405 for non-GET requests.
	s.mux.HandleFunc("/health", s.getOnly(s.handleHealth))
	s.mux.HandleFunc("/ready", s.getOnly(s.handleReady))

	// Legacy health endpoints for backwards compatibility
	s.mux.HandleFunc("/healthz", s.getOnly(s.handleHealth))
	s.mux.HandleFunc("/readyz", s.getOnly(s.handleReady))

	// Build version endpoint - NO middleware (public, like health)
	s.mux.HandleFunc("/version", s.getOnly(s.handleVersion))

	// API routes - with auth and tenant middleware chain.
	// Prefer the Vanguard transcoder when configured; fall back to the legacy
	// prefix-based reverse proxy when Backends are provided; otherwise use a
	// fallback that returns 503 Service Unavailable (misconfiguration/degraded).
	//
	// For the transcoder path, metadataPropagationMiddleware wraps the handler to
	// strip spoofed incoming identity headers and inject authenticated identity
	// as lowercase gRPC metadata headers (x-user-id, x-tenant-id, x-auth-method,
	// x-auth-roles). Vanguard forwards these headers to the gRPC backend where
	// they are read as incoming metadata by the existing interceptor chain
	// (TenantExtractionInterceptor reads x-tenant-id).
	var apiHandler http.Handler
	switch {
	case s.transcoderHandler != nil:
		apiHandler = metadataPropagationMiddleware(s.transcoderHandler)
	case len(s.config.Backends) > 0:
		apiHandler = NewProxyHandler(s.config.Backends)
	default:
		apiHandler = http.HandlerFunc(s.handleAPI)
	}

	// Build middleware chain: auth → tenant → tenant_authz → transcoder/proxy
	s.mux.Handle("/", s.wrapWithAuthChain(apiHandler))

	// WebSocket event stream endpoint - with auth and tenant middleware chain.
	// Claims are bridged from the gateway auth context to the eventstream context
	// so that the eventstream.Handler can authorize channel subscriptions.
	//
	// rawEventStreamHandler takes precedence (used in tests). Production code uses
	// eventStreamHandler which gets wrapped with the claims bridge.
	if s.rawEventStreamHandler != nil {
		wsHandler := s.wrapWithAuthChain(s.rawEventStreamHandler)
		s.mux.Handle("GET /ws/events", wsHandler)
	} else if s.eventStreamHandler != nil {
		wsHandler := s.buildEventStreamHandler()
		s.mux.Handle("GET /ws/events", wsHandler)
	}
}

// buildEventStreamHandler wraps the eventstream.Handler with a claims bridge and
// the gateway auth middleware chain.
//
// Middleware chain: authMiddleware → tenantResolver → tenantAuthzMiddleware → claimsBridge → wsHandler
func (s *Server) buildEventStreamHandler() http.Handler {
	// Claims bridge: reads claims from gateway auth context (set by authMiddleware)
	// and injects them into the eventstream-specific context key so that the
	// eventstream.Handler can authorize channel subscriptions.
	claimsBridge := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if claims, ok := auth.GetClaimsFromContext(r.Context()); ok {
			r = r.WithContext(eventstream.ContextWithClaims(r.Context(), claims))
		}
		s.eventStreamHandler.ServeHTTP(w, r)
	})

	return s.wrapWithAuthChain(claimsBridge)
}

// wrapWithAuthChain wraps a handler with the auth middleware chain:
// authMiddleware → tenantResolver → tenantAuthzMiddleware → inner
func (s *Server) wrapWithAuthChain(inner http.Handler) http.Handler {
	handler := inner

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

	return handler
}

// getOnly wraps a handler to only accept GET (and HEAD) requests,
// returning 405 Method Not Allowed for anything else.
func (s *Server) getOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
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

// handleVersion returns build version information as JSON.
// This endpoint does NOT require authentication or tenant context.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	info := s.versionInfo
	if info == nil {
		info = &VersionInfo{Version: "dev", Commit: "unknown", BuildDate: "unknown"}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(info)
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

// handleAPI is the fallback handler for API routes when neither the Vanguard
// transcoder nor a legacy proxy backend is configured. This indicates a
// misconfiguration or degraded state (e.g. transcoder build failure).
func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	s.logger.Warn("API request reached fallback handler: no transcoder or proxy backend configured",
		"method", r.Method,
		"path", r.URL.Path,
		"host", r.Host)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	if _, err := w.Write([]byte(`{"error":"no API backend configured","detail":"neither Vanguard transcoder nor proxy backends are available; check gateway startup logs"}`)); err != nil {
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
