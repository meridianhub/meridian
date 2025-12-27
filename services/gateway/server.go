package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server is the HTTP server for the gateway service.
type Server struct {
	config     *Config
	logger     *slog.Logger
	httpServer *http.Server
	mux        *http.ServeMux
}

// NewServer creates a new gateway HTTP server with the given configuration.
func NewServer(config *Config, logger *slog.Logger) *Server {
	mux := http.NewServeMux()

	s := &Server{
		config: config,
		logger: logger,
		mux:    mux,
	}

	// Register routes
	s.registerRoutes()

	return s
}

// registerRoutes sets up the HTTP routes for the gateway.
func (s *Server) registerRoutes() {
	// Health check endpoints
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)

	// Root handler for API routing (placeholder for future implementation)
	s.mux.HandleFunc("/", s.handleRoot)
}

// handleHealthz is the liveness probe endpoint.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

// handleReadyz is the readiness probe endpoint.
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	// TODO: Add actual readiness checks (database, redis, backends)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

// handleRoot is a placeholder handler for the root path.
// This will be replaced with actual routing logic in future tasks.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("received request",
		"method", r.Method,
		"path", r.URL.Path,
		"host", r.Host)

	// Placeholder response - actual routing will be implemented in Task 88
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte(`{"error":"gateway routing not yet implemented"}`))
}

// Start starts the HTTP server and blocks until the server stops.
// It returns an error if the server fails to start or encounters an error.
func (s *Server) Start(ctx context.Context) error {
	address := fmt.Sprintf(":%d", s.config.Port)

	s.httpServer = &http.Server{
		Addr:              address,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

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

	if err := s.httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP server error: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}

	s.logger.Info("shutting down HTTP server...")

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %w", err)
	}

	s.logger.Info("HTTP server stopped")
	return nil
}
