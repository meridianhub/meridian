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
	"github.com/meridianhub/meridian/shared/platform/defaults"
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
	// RateLimitMaxEntries is the max number of IPs to track for rate limiting.
	// When exceeded, oldest entries are evicted. Defaults to 10000.
	RateLimitMaxEntries int
	// TrustProxyHeaders controls whether to trust X-Forwarded-For and X-Real-IP headers.
	// Only enable this when running behind a trusted reverse proxy.
	TrustProxyHeaders bool
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
		Port:                8080,
		RateLimitPerSecond:  100,
		RateLimitBurst:      200,
		RateLimitMaxEntries: 10000,
		TrustProxyHeaders:   false,
		ReadTimeout:         defaults.DefaultHTTPReadHeaderTimeout,
		WriteTimeout:        defaults.DefaultHTTPWriteTimeout,
		IdleTimeout:         defaults.DefaultHTTPIdleTimeout,
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
	if cfg.RateLimitMaxEntries <= 0 {
		cfg.RateLimitMaxEntries = 10000
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = defaults.DefaultHTTPReadTimeout
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = defaults.DefaultHTTPWriteTimeout
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaults.DefaultHTTPIdleTimeout
	}

	// Create rate limiter with max entries to prevent unbounded growth
	rateLimiter := newIPRateLimiter(rate.Limit(cfg.RateLimitPerSecond), cfg.RateLimitBurst, cfg.RateLimitMaxEntries)

	// Create mux and register handlers
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook/payment-gateway", cfg.WebhookHandler.HandleWebhook)
	mux.HandleFunc("GET /health", healthHandler)

	// Apply middleware chain
	handler := chainMiddleware(
		mux,
		requestIDMiddleware(logger),
		loggingMiddleware(logger, cfg.TrustProxyHeaders),
		rateLimitMiddleware(rateLimiter, logger, cfg.TrustProxyHeaders),
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
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(`{"status":"healthy"}`)); err != nil {
		slog.Warn("failed to write health response",
			"error", err,
			"endpoint", r.URL.Path,
			"remote_addr", r.RemoteAddr)
	}
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
func loggingMiddleware(logger *slog.Logger, trustProxyHeaders bool) middleware {
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
				"remote_addr", getClientIP(r, trustProxyHeaders),
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
func rateLimitMiddleware(limiter *ipRateLimiter, logger *slog.Logger, trustProxyHeaders bool) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := getClientIP(r, trustProxyHeaders)
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
// When trustProxyHeaders is true, it checks X-Forwarded-For and X-Real-IP headers.
// When false, it only uses RemoteAddr to prevent IP spoofing attacks.
func getClientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
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
	}

	// Fall back to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// ipRateLimiter provides per-IP rate limiting using token bucket.
// It maintains a bounded map of limiters with LRU-style eviction.
type ipRateLimiter struct {
	limiters   map[string]*rateLimiterEntry
	mu         sync.Mutex
	r          rate.Limit
	b          int
	maxEntries int
}

// rateLimiterEntry holds a rate limiter and its last access time.
type rateLimiterEntry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// newIPRateLimiter creates a new IP-based rate limiter with bounded entries.
func newIPRateLimiter(r rate.Limit, b int, maxEntries int) *ipRateLimiter {
	return &ipRateLimiter{
		limiters:   make(map[string]*rateLimiterEntry),
		r:          r,
		b:          b,
		maxEntries: maxEntries,
	}
}

// getLimiter returns the rate limiter for the given IP.
func (l *ipRateLimiter) getLimiter(ip string) *rate.Limiter {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if entry, exists := l.limiters[ip]; exists {
		entry.lastAccess = now
		return entry.limiter
	}

	// Evict oldest entries if at capacity
	if len(l.limiters) >= l.maxEntries {
		l.evictOldest()
	}

	entry := &rateLimiterEntry{
		limiter:    rate.NewLimiter(l.r, l.b),
		lastAccess: now,
	}
	l.limiters[ip] = entry
	return entry.limiter
}

// evictOldest removes the oldest entry from the limiters map.
// Must be called with write lock held.
func (l *ipRateLimiter) evictOldest() {
	var oldestIP string
	var oldestTime time.Time

	for ip, entry := range l.limiters {
		if oldestIP == "" || entry.lastAccess.Before(oldestTime) {
			oldestIP = ip
			oldestTime = entry.lastAccess
		}
	}

	if oldestIP != "" {
		delete(l.limiters, oldestIP)
	}
}

// allow checks if the request from the given IP should be allowed.
func (l *ipRateLimiter) allow(ip string) bool {
	return l.getLimiter(ip).Allow()
}
