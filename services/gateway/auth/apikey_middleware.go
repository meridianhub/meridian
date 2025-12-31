// Package auth provides authentication middleware for the gateway service.
package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// APIKeyHeader is the header name used for API key authentication.
const APIKeyHeader = "X-API-Key"

// contextKey is a type for context keys to avoid collisions.
type contextKey string

// APIKeyIdentityKey is the context key for the API key identity.
const APIKeyIdentityKey contextKey = "api_key_identity"

// GetAPIKeyIdentity retrieves the API key identity from the request context.
// Returns empty string if not present.
func GetAPIKeyIdentity(ctx context.Context) string {
	if identity, ok := ctx.Value(APIKeyIdentityKey).(string); ok {
		return identity
	}
	return ""
}

// APIKeyConfig holds configuration for API key authentication and rate limiting.
type APIKeyConfig struct {
	// APIKeys maps API key strings to their identity names.
	// Identity names are stored in context for logging/auditing.
	APIKeys map[string]string

	// RateLimitPerSecond is the number of requests allowed per second per API key.
	// Defaults to 100 if not set.
	RateLimitPerSecond float64

	// RateLimitBurst is the maximum burst size for rate limiting.
	// Defaults to 200 if not set.
	RateLimitBurst int

	// CleanupInterval is how often to clean up expired rate limiters.
	// Defaults to 5 minutes if not set.
	CleanupInterval time.Duration

	// LimiterIdleTimeout is how long a rate limiter can be idle before cleanup.
	// Defaults to 10 minutes if not set.
	LimiterIdleTimeout time.Duration

	// Logger for logging authentication events.
	Logger *slog.Logger
}

// DefaultAPIKeyConfig returns an APIKeyConfig with sensible defaults.
func DefaultAPIKeyConfig() APIKeyConfig {
	return APIKeyConfig{
		APIKeys:            make(map[string]string),
		RateLimitPerSecond: 100,
		RateLimitBurst:     200,
		CleanupInterval:    5 * time.Minute,
		LimiterIdleTimeout: 10 * time.Minute,
	}
}

// LoadAPIKeyConfigFromEnv loads API key configuration from environment variables.
// Environment variables:
//   - API_KEYS: Comma-separated list of "key:identity" pairs
//     Example: "abc123:service-a,def456:service-b"
//   - API_KEY_RATE_LIMIT_PER_SECOND: Requests per second (default: 100)
//   - API_KEY_RATE_LIMIT_BURST: Burst size (default: 200)
//   - API_KEY_CLEANUP_INTERVAL: Cleanup interval (default: 5m)
//   - API_KEY_IDLE_TIMEOUT: Idle timeout for limiters (default: 10m)
func LoadAPIKeyConfigFromEnv() APIKeyConfig {
	config := DefaultAPIKeyConfig()

	// Parse API keys
	apiKeysEnv := os.Getenv("API_KEYS")
	if apiKeysEnv != "" {
		config.APIKeys = parseAPIKeys(apiKeysEnv)
	}

	// Parse rate limit per second
	if rps := os.Getenv("API_KEY_RATE_LIMIT_PER_SECOND"); rps != "" {
		if v, err := strconv.ParseFloat(rps, 64); err == nil && v > 0 {
			config.RateLimitPerSecond = v
		}
	}

	// Parse rate limit burst
	if burst := os.Getenv("API_KEY_RATE_LIMIT_BURST"); burst != "" {
		if v, err := strconv.Atoi(burst); err == nil && v > 0 {
			config.RateLimitBurst = v
		}
	}

	// Parse cleanup interval
	if interval := os.Getenv("API_KEY_CLEANUP_INTERVAL"); interval != "" {
		if v, err := time.ParseDuration(interval); err == nil && v > 0 {
			config.CleanupInterval = v
		}
	}

	// Parse idle timeout
	if timeout := os.Getenv("API_KEY_IDLE_TIMEOUT"); timeout != "" {
		if v, err := time.ParseDuration(timeout); err == nil && v > 0 {
			config.LimiterIdleTimeout = v
		}
	}

	return config
}

// parseAPIKeys parses a comma-separated list of "key:identity" pairs.
func parseAPIKeys(env string) map[string]string {
	keys := make(map[string]string)
	pairs := strings.Split(env, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			identity := strings.TrimSpace(parts[1])
			if key != "" && identity != "" {
				keys[key] = identity
			}
		}
	}
	return keys
}

// APIKeyMiddleware provides API key authentication with per-key rate limiting.
type APIKeyMiddleware struct {
	config    APIKeyConfig
	limiters  sync.Map // map[string]*rateLimiterEntry
	logger    *slog.Logger
	stopCh    chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// rateLimiterEntry holds a rate limiter and its last access time.
type rateLimiterEntry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
	mu         sync.Mutex
}

// NewAPIKeyMiddleware creates a new API key middleware.
// It starts a background goroutine to clean up expired rate limiters.
// Call Close() to stop the cleanup goroutine.
func NewAPIKeyMiddleware(config APIKeyConfig) *APIKeyMiddleware {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Apply defaults for zero values
	if config.RateLimitPerSecond <= 0 {
		config.RateLimitPerSecond = 100
	}
	if config.RateLimitBurst <= 0 {
		config.RateLimitBurst = 200
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = 5 * time.Minute
	}
	if config.LimiterIdleTimeout <= 0 {
		config.LimiterIdleTimeout = 10 * time.Minute
	}

	m := &APIKeyMiddleware{
		config: config,
		logger: config.Logger,
		stopCh: make(chan struct{}),
	}

	// Start cleanup goroutine
	m.wg.Add(1)
	go m.cleanupLoop()

	return m
}

// Close stops the background cleanup goroutine.
// This method is idempotent and safe to call multiple times.
func (m *APIKeyMiddleware) Close() {
	m.closeOnce.Do(func() {
		close(m.stopCh)
		m.wg.Wait()
	})
}

// cleanupLoop periodically removes idle rate limiters to prevent memory leaks.
func (m *APIKeyMiddleware) cleanupLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.cleanup()
		}
	}
}

// cleanup removes rate limiters that have been idle longer than LimiterIdleTimeout.
func (m *APIKeyMiddleware) cleanup() {
	now := time.Now()
	var removed int

	m.limiters.Range(func(key, value interface{}) bool {
		entry, ok := value.(*rateLimiterEntry)
		if !ok {
			return true
		}
		entry.mu.Lock()
		idle := now.Sub(entry.lastAccess) > m.config.LimiterIdleTimeout
		entry.mu.Unlock()

		if idle {
			m.limiters.Delete(key)
			removed++
		}
		return true
	})

	if removed > 0 {
		m.logger.Debug("cleaned up idle rate limiters", "removed", removed)
	}
}

// Handler returns an http.Handler that wraps the next handler with API key authentication.
func (m *APIKeyMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get(APIKeyHeader)

		// Check if API key is provided
		if apiKey == "" {
			m.logger.Debug("missing API key", "path", r.URL.Path)
			writeJSONError(w, "missing API key", http.StatusUnauthorized)
			return
		}

		// Validate API key
		identity, valid := m.config.APIKeys[apiKey]
		if !valid {
			m.logger.Warn("invalid API key", "path", r.URL.Path)
			writeJSONError(w, "invalid API key", http.StatusUnauthorized)
			return
		}

		// Check rate limit for this API key
		if !m.allowRequest(apiKey) {
			m.logger.Warn("rate limit exceeded",
				"identity", identity,
				"path", r.URL.Path)
			writeJSONError(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		// Store identity in context
		ctx := context.WithValue(r.Context(), APIKeyIdentityKey, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// allowRequest checks if the request from the given API key should be allowed.
// Uses lazy initialization to create rate limiters only when needed.
func (m *APIKeyMiddleware) allowRequest(apiKey string) bool {
	now := time.Now()

	// Try to load existing entry
	value, loaded := m.limiters.Load(apiKey)
	if loaded {
		entry, ok := value.(*rateLimiterEntry)
		if !ok {
			return false // Should never happen, but handle gracefully
		}
		entry.mu.Lock()
		entry.lastAccess = now
		entry.mu.Unlock()
		return entry.limiter.Allow()
	}

	// Create new limiter
	limiter := rate.NewLimiter(rate.Limit(m.config.RateLimitPerSecond), m.config.RateLimitBurst)
	entry := &rateLimiterEntry{
		limiter:    limiter,
		lastAccess: now,
	}

	// Store or get existing (handles race condition)
	actual, loaded := m.limiters.LoadOrStore(apiKey, entry)
	if loaded {
		// Another goroutine created it first, use that one
		existingEntry, ok := actual.(*rateLimiterEntry)
		if !ok {
			return false // Should never happen, but handle gracefully
		}
		existingEntry.mu.Lock()
		existingEntry.lastAccess = now
		existingEntry.mu.Unlock()
		return existingEntry.limiter.Allow()
	}

	return entry.limiter.Allow()
}

// errorResponse is the JSON structure for error responses.
type errorResponse struct {
	Error string `json:"error"`
}

// writeJSONError writes a JSON error response using proper JSON encoding.
func writeJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

// LimiterCount returns the number of active rate limiters (for testing).
func (m *APIKeyMiddleware) LimiterCount() int {
	count := 0
	m.limiters.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}
