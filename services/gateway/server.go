package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/meridianhub/meridian/shared/platform/defaults"
	platformgateway "github.com/meridianhub/meridian/shared/platform/gateway"
)

// Server is the HTTP server for the gateway service.
type Server struct {
	config         *Config
	logger         *slog.Logger
	httpServer     *http.Server
	httpServerMu   sync.RWMutex // Guards httpServer field
	mux            *http.ServeMux
	tenantResolver *platformgateway.TenantResolverMiddleware
}

// NewServer creates a new gateway HTTP server with the given configuration.
// The tenantResolver parameter is optional - if nil, all routes bypass tenant resolution.
func NewServer(config *Config, logger *slog.Logger, tenantResolver *platformgateway.TenantResolverMiddleware) *Server {
	mux := http.NewServeMux()

	s := &Server{
		config:         config,
		logger:         logger,
		mux:            mux,
		tenantResolver: tenantResolver,
	}

	// Register routes
	s.registerRoutes()

	return s
}

// registerRoutes sets up the HTTP routes for the gateway.
//
// CRITICAL: Health endpoints (/health, /ready) are registered directly on the main mux
// WITHOUT tenant middleware. This is required for K8s probes which do not provide
// tenant context (no subdomain/Host header).
//
// API routes go through tenant middleware to resolve and inject tenant context.
func (s *Server) registerRoutes() {
	// Health check endpoints - NO tenant middleware (required for K8s probes)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /ready", s.handleReady)

	// Legacy health endpoints for backwards compatibility
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /readyz", s.handleReady)

	// API routes - WITH tenant middleware (if tenant resolver is configured)
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/", s.handleAPI)

	if s.tenantResolver != nil {
		// Wrap API routes with tenant resolution middleware
		s.mux.Handle("/api/", s.tenantResolver.Handler(http.StripPrefix("/api", apiMux)))
	} else {
		// No tenant resolver - direct routing (useful for testing or dev mode)
		s.mux.Handle("/api/", http.StripPrefix("/api", apiMux))
	}
}

// handleHealth is the liveness probe endpoint.
// Returns 200 OK if the server is alive.
// This endpoint does NOT require tenant context.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

// handleReady is the readiness probe endpoint.
// Returns 200 OK if the server is ready to accept traffic.
// This endpoint does NOT require tenant context.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	// TODO: Add actual readiness checks (database, redis, backends)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("READY"))
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
	_, _ = w.Write([]byte(`{"error":"gateway routing not yet implemented"}`))
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

// Shutdown gracefully shuts down the HTTP server.
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

	s.logger.Info("HTTP server stopped")
	return nil
}
