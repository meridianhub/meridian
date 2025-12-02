// Package http provides the HTTP adapter for receiving payment gateway webhooks.
package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// Configuration errors.
var (
	ErrNilWebhookHandler = errors.New("webhook handler cannot be nil")
	ErrInvalidPort       = errors.New("invalid port number")
)

// Server wraps an HTTP server with middleware and graceful shutdown.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// ServerConfig contains configuration for creating an HTTP server.
type ServerConfig struct {
	// Port is the HTTP port to listen on.
	Port int
	// WebhookHandler handles incoming webhooks.
	WebhookHandler *WebhookHandler
	// Logger for request logging.
	Logger *slog.Logger
	// RateLimitPerSecond is the max requests per second per IP.
	RateLimitPerSecond float64
	// RateLimitBurst is the max burst size for rate limiting.
	RateLimitBurst int
	// ReadTimeout for HTTP requests.
	ReadTimeout time.Duration
	// WriteTimeout for HTTP responses.
	WriteTimeout time.Duration
	// IdleTimeout for keep-alive connections.
	IdleTimeout time.Duration
}

// DefaultServerConfig returns a ServerConfig with sensible defaults.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Port:               8080,
		RateLimitPerSecond: 100,
		RateLimitBurst:     200,
		ReadTimeout:        10 * time.Second,
		WriteTimeout:       30 * time.Second,
		IdleTimeout:        60 * time.Second,
	}
}

// NewServer creates a new HTTP server with the provided configuration.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.WebhookHandler == nil {
		return nil, ErrNilWebhookHandler
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, ErrInvalidPort
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Apply defaults for zero values
	if cfg.RateLimitPerSecond <= 0 {
		cfg.RateLimitPerSecond = 100
	}
	if cfg.RateLimitBurst <= 0 {
		cfg.RateLimitBurst = 200
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 10 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 30 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 60 * time.Second
	}

	// Create rate limiter
	rateLimiter := newIPRateLimiter(rate.Limit(cfg.RateLimitPerSecond), cfg.RateLimitBurst)

	// Create mux and register handlers
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook/payment-gateway", cfg.WebhookHandler.HandleWebhook)
	mux.HandleFunc("GET /health", healthHandler)

	// Apply middleware chain
	handler := chainMiddleware(
		mux,
		requestIDMiddleware(logger),
		loggingMiddleware(logger),
		rateLimitMiddleware(rateLimiter, logger),
		recoveryMiddleware(logger),
	)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return &Server{
		httpServer: httpServer,
		logger:     logger,
	}, nil
}

// Start begins listening for HTTP requests.
// This method blocks until the server is stopped.
func (s *Server) Start() error {
	s.logger.Info("starting HTTP server", "address", s.httpServer.Addr)
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down HTTP server")
	return s.httpServer.Shutdown(ctx)
}

// Addr returns the server's address.
// Only valid after Start() is called with a listener.
func (s *Server) Addr() string {
	return s.httpServer.Addr
}

// StartWithListener starts the server on the provided listener.
// Useful for testing with dynamically allocated ports.
func (s *Server) StartWithListener(listener net.Listener) error {
	s.logger.Info("starting HTTP server", "address", listener.Addr().String())
	err := s.httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// healthHandler provides a simple health check endpoint.
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"healthy"}`))
}

// Middleware types and helpers

type middleware func(http.Handler) http.Handler

// chainMiddleware applies middleware in order (first middleware is outermost).
func chainMiddleware(handler http.Handler, middlewares ...middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// requestIDMiddleware adds a unique request ID to each request.
func requestIDMiddleware(_ *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = uuid.New().String()
			}
			w.Header().Set("X-Request-ID", requestID)

			// Add request ID to context for downstream use
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// contextKey is a type for context keys to avoid collisions.
type contextKey string

const requestIDKey contextKey = "request_id"

// GetRequestID retrieves the request ID from context.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// loggingMiddleware logs HTTP requests.
func loggingMiddleware(logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)
			requestID := GetRequestID(r.Context())

			logger.Info("http request",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.statusCode,
				"duration_ms", duration.Milliseconds(),
				"remote_addr", getClientIP(r),
				"user_agent", r.UserAgent())
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// rateLimitMiddleware applies per-IP rate limiting.
func rateLimitMiddleware(limiter *ipRateLimiter, logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := getClientIP(r)
			if !limiter.allow(ip) {
				requestID := GetRequestID(r.Context())
				logger.Warn("rate limit exceeded",
					"request_id", requestID,
					"remote_addr", ip,
					"path", r.URL.Path)
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// recoveryMiddleware recovers from panics and returns 500.
func recoveryMiddleware(logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() { //nolint:contextcheck // recovery handler cannot accept context in defer
				if err := recover(); err != nil {
					requestID := GetRequestID(r.Context())
					logger.Error("panic recovered",
						"request_id", requestID,
						"error", err,
						"path", r.URL.Path)
					http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// getClientIP extracts the client IP from the request.
// Handles X-Forwarded-For and X-Real-IP headers for proxied requests.
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For first (may contain multiple IPs)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (original client)
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}

	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// ipRateLimiter provides per-IP rate limiting using token bucket.
type ipRateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	r        rate.Limit
	b        int
}

// newIPRateLimiter creates a new IP-based rate limiter.
func newIPRateLimiter(r rate.Limit, b int) *ipRateLimiter {
	return &ipRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		r:        r,
		b:        b,
	}
}

// getLimiter returns the rate limiter for the given IP.
func (l *ipRateLimiter) getLimiter(ip string) *rate.Limiter {
	l.mu.RLock()
	limiter, exists := l.limiters[ip]
	l.mu.RUnlock()

	if exists {
		return limiter
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists := l.limiters[ip]; exists {
		return limiter
	}

	limiter = rate.NewLimiter(l.r, l.b)
	l.limiters[ip] = limiter
	return limiter
}

// allow checks if the request from the given IP should be allowed.
func (l *ipRateLimiter) allow(ip string) bool {
	return l.getLimiter(ip).Allow()
}
