// Package server provides the HTTP gateway server.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/meridianhub/meridian/services/gateway/internal/config"
	"github.com/meridianhub/meridian/services/gateway/internal/middleware"
)

// Server is the HTTP gateway server.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
	logger     *slog.Logger
	config     *config.Config
}

// New creates a new gateway server.
func New(cfg *config.Config, logger *slog.Logger) (*Server, error) {
	// Create tenant resolver middleware
	tenantResolver := middleware.NewTenantResolver(
		cfg.BaseDomain,
		[]string{"localhost", "127.0.0.1"},
	)

	// Build handler chain
	mux := http.NewServeMux()

	// Health endpoint (bypasses tenant resolution via allowed hosts)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})

	// Ready endpoint
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	// Proxy handler for backend services
	proxyHandler := NewProxyHandler(cfg, logger)
	mux.Handle("/", proxyHandler)

	// Wrap with tenant resolver middleware
	handler := tenantResolver.Middleware(mux)

	// Create HTTP server with timeouts
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return &Server{
		httpServer: httpServer,
		logger:     logger,
		config:     cfg,
	}, nil
}

// Start starts the server and blocks until it's ready.
func (s *Server) Start(ctx context.Context) error {
	var err error
	s.listener, err = (&net.ListenConfig{}).Listen(ctx, "tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.httpServer.Addr, err)
	}

	s.logger.Info("gateway server started",
		"address", s.httpServer.Addr,
		"base_domain", s.config.BaseDomain)

	go func() {
		if err := s.httpServer.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("server error", "error", err)
		}
	}()

	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down gateway server...")
	return s.httpServer.Shutdown(ctx)
}

// Addr returns the server's address.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.httpServer.Addr
}
