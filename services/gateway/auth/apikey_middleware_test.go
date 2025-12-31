package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPIKeyMiddleware_ValidKeyAcceptsRequest verifies that a valid API key
// allows the request to proceed and stores identity in context.
func TestAPIKeyMiddleware_ValidKeyAcceptsRequest(t *testing.T) {
	config := DefaultAPIKeyConfig()
	config.APIKeys = map[string]string{
		"valid-key-123": "service-a",
	}

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	var capturedIdentity string
	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIdentity = GetAPIKeyIdentity(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(APIKeyHeader, "valid-key-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "service-a", capturedIdentity)
}

// TestAPIKeyMiddleware_InvalidKeyReturns401 verifies that an invalid API key
// is rejected with 401 Unauthorized.
func TestAPIKeyMiddleware_InvalidKeyReturns401(t *testing.T) {
	config := DefaultAPIKeyConfig()
	config.APIKeys = map[string]string{
		"valid-key-123": "service-a",
	}

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	called := false
	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(APIKeyHeader, "invalid-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, called, "handler should not be called for invalid API key")
	assert.Contains(t, rec.Body.String(), "invalid API key")
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
}

// TestAPIKeyMiddleware_MissingKeyReturns401 verifies that a missing API key
// is rejected with 401 Unauthorized.
func TestAPIKeyMiddleware_MissingKeyReturns401(t *testing.T) {
	config := DefaultAPIKeyConfig()
	config.APIKeys = map[string]string{
		"valid-key-123": "service-a",
	}

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	called := false
	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	// No API key header set
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, called, "handler should not be called for missing API key")
	assert.Contains(t, rec.Body.String(), "missing API key")
}

// TestAPIKeyMiddleware_RateLimiterAllowsThenRejects verifies that the rate limiter
// allows N requests then returns 429 when exceeded.
func TestAPIKeyMiddleware_RateLimiterAllowsThenRejects(t *testing.T) {
	config := DefaultAPIKeyConfig()
	config.APIKeys = map[string]string{
		"rate-limited-key": "service-a",
	}
	// Very restrictive rate limit for testing: 1 request per second, burst of 3
	config.RateLimitPerSecond = 1
	config.RateLimitBurst = 3

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 3 requests should succeed (burst)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set(APIKeyHeader, "rate-limited-key")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "request %d should succeed", i+1)
	}

	// 4th request should fail (rate limit exceeded)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(APIKeyHeader, "rate-limited-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Contains(t, rec.Body.String(), "rate limit exceeded")
}

// TestAPIKeyMiddleware_RateLimiterResetsAfterTime verifies that the rate limiter
// resets after the time window expires.
func TestAPIKeyMiddleware_RateLimiterResetsAfterTime(t *testing.T) {
	config := DefaultAPIKeyConfig()
	config.APIKeys = map[string]string{
		"rate-limited-key": "service-a",
	}
	// 10 requests per second, burst of 2
	config.RateLimitPerSecond = 10
	config.RateLimitBurst = 2

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust burst
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set(APIKeyHeader, "rate-limited-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "request %d should succeed", i+1)
	}

	// Should be rate limited now
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(APIKeyHeader, "rate-limited-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	// Wait for token refill (100ms = 1 token at 10/sec)
	time.Sleep(150 * time.Millisecond)

	// Should succeed again
	req = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(APIKeyHeader, "rate-limited-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "request should succeed after rate limit reset")
}

// TestAPIKeyMiddleware_MultipleKeysIndependentRateLimits verifies that
// different API keys have independent rate limits.
func TestAPIKeyMiddleware_MultipleKeysIndependentRateLimits(t *testing.T) {
	config := DefaultAPIKeyConfig()
	config.APIKeys = map[string]string{
		"key-a": "service-a",
		"key-b": "service-b",
	}
	// 1 request per second, burst of 2
	config.RateLimitPerSecond = 1
	config.RateLimitBurst = 2

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust key-a's burst
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set(APIKeyHeader, "key-a")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}

	// key-a should be rate limited
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(APIKeyHeader, "key-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code, "key-a should be rate limited")

	// key-b should still work (independent rate limit)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set(APIKeyHeader, "key-b")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "key-b request %d should succeed", i+1)
	}
}

// TestAPIKeyMiddleware_ConcurrentRequests verifies that concurrent requests
// from the same API key respect rate limits correctly.
func TestAPIKeyMiddleware_ConcurrentRequests(t *testing.T) {
	config := DefaultAPIKeyConfig()
	config.APIKeys = map[string]string{
		"concurrent-key": "service-a",
	}
	// 10 requests per second, burst of 10
	config.RateLimitPerSecond = 10
	config.RateLimitBurst = 10

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	var successCount atomic.Int32

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		successCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	// Send 20 concurrent requests
	const numRequests = 20
	var wg sync.WaitGroup
	wg.Add(numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			req.Header.Set(APIKeyHeader, "concurrent-key")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()
	}

	wg.Wait()

	// With burst of 10, we should have exactly 10 successful requests
	assert.Equal(t, int32(10), successCount.Load(),
		"exactly burst size requests should succeed")
}

// TestAPIKeyMiddleware_CleanupRemovesIdleLimiters verifies that idle rate
// limiters are cleaned up to prevent memory leaks.
func TestAPIKeyMiddleware_CleanupRemovesIdleLimiters(t *testing.T) {
	config := DefaultAPIKeyConfig()
	config.APIKeys = map[string]string{
		"key-1": "service-1",
		"key-2": "service-2",
	}
	// Very short cleanup for testing
	config.CleanupInterval = 50 * time.Millisecond
	config.LimiterIdleTimeout = 100 * time.Millisecond

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Make requests to create limiters
	for _, key := range []string{"key-1", "key-2"} {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set(APIKeyHeader, key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Verify limiters were created
	assert.Equal(t, 2, middleware.LimiterCount(), "should have 2 limiters")

	// Wait for cleanup (idle timeout + cleanup interval + buffer)
	time.Sleep(200 * time.Millisecond)

	// Limiters should be cleaned up
	assert.Equal(t, 0, middleware.LimiterCount(), "limiters should be cleaned up")
}

// TestAPIKeyMiddleware_GetAPIKeyIdentity verifies context identity retrieval.
func TestAPIKeyMiddleware_GetAPIKeyIdentity(t *testing.T) {
	t.Run("returns empty string when not present", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		identity := GetAPIKeyIdentity(req.Context())
		assert.Empty(t, identity)
	})

	t.Run("returns identity when present", func(t *testing.T) {
		config := DefaultAPIKeyConfig()
		config.APIKeys = map[string]string{
			"key-123": "test-service",
		}

		middleware := NewAPIKeyMiddleware(config)
		defer middleware.Close()

		var capturedIdentity string
		handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedIdentity = GetAPIKeyIdentity(r.Context())
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(APIKeyHeader, "key-123")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, "test-service", capturedIdentity)
	})
}

// TestParseAPIKeys verifies parsing of API keys from environment variable format.
func TestParseAPIKeys(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: map[string]string{},
		},
		{
			name:  "single key",
			input: "abc123:service-a",
			expected: map[string]string{
				"abc123": "service-a",
			},
		},
		{
			name:  "multiple keys",
			input: "abc123:service-a,def456:service-b,ghi789:service-c",
			expected: map[string]string{
				"abc123": "service-a",
				"def456": "service-b",
				"ghi789": "service-c",
			},
		},
		{
			name:  "with spaces",
			input: "abc123 : service-a , def456 : service-b",
			expected: map[string]string{
				"abc123": "service-a",
				"def456": "service-b",
			},
		},
		{
			name:  "ignores invalid entries",
			input: "abc123:service-a,invalid,def456:service-b,:empty-key,empty-identity:",
			expected: map[string]string{
				"abc123": "service-a",
				"def456": "service-b",
			},
		},
		{
			name:  "colon in identity",
			input: "key:identity:with:colons",
			expected: map[string]string{
				"key": "identity:with:colons",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAPIKeys(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestLoadAPIKeyConfigFromEnv verifies environment variable loading.
func TestLoadAPIKeyConfigFromEnv(t *testing.T) {
	// Save and restore environment
	saveEnv := func(keys []string) map[string]string {
		saved := make(map[string]string)
		for _, key := range keys {
			saved[key] = os.Getenv(key)
		}
		return saved
	}
	restoreEnv := func(saved map[string]string) {
		for key, value := range saved {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}

	envKeys := []string{
		"API_KEYS",
		"API_KEY_RATE_LIMIT_PER_SECOND",
		"API_KEY_RATE_LIMIT_BURST",
		"API_KEY_CLEANUP_INTERVAL",
		"API_KEY_IDLE_TIMEOUT",
	}
	saved := saveEnv(envKeys)
	defer restoreEnv(saved)

	t.Run("default values when env not set", func(t *testing.T) {
		for _, key := range envKeys {
			os.Unsetenv(key)
		}

		config := LoadAPIKeyConfigFromEnv()

		assert.Empty(t, config.APIKeys)
		assert.Equal(t, 100.0, config.RateLimitPerSecond)
		assert.Equal(t, 200, config.RateLimitBurst)
		assert.Equal(t, 5*time.Minute, config.CleanupInterval)
		assert.Equal(t, 10*time.Minute, config.LimiterIdleTimeout)
	})

	t.Run("parses all environment variables", func(t *testing.T) {
		os.Setenv("API_KEYS", "key1:svc1,key2:svc2")
		os.Setenv("API_KEY_RATE_LIMIT_PER_SECOND", "50")
		os.Setenv("API_KEY_RATE_LIMIT_BURST", "100")
		os.Setenv("API_KEY_CLEANUP_INTERVAL", "2m")
		os.Setenv("API_KEY_IDLE_TIMEOUT", "5m")

		config := LoadAPIKeyConfigFromEnv()

		assert.Equal(t, map[string]string{"key1": "svc1", "key2": "svc2"}, config.APIKeys)
		assert.Equal(t, 50.0, config.RateLimitPerSecond)
		assert.Equal(t, 100, config.RateLimitBurst)
		assert.Equal(t, 2*time.Minute, config.CleanupInterval)
		assert.Equal(t, 5*time.Minute, config.LimiterIdleTimeout)
	})

	t.Run("ignores invalid values", func(t *testing.T) {
		os.Setenv("API_KEYS", "key1:svc1")
		os.Setenv("API_KEY_RATE_LIMIT_PER_SECOND", "invalid")
		os.Setenv("API_KEY_RATE_LIMIT_BURST", "invalid")
		os.Setenv("API_KEY_CLEANUP_INTERVAL", "invalid")
		os.Setenv("API_KEY_IDLE_TIMEOUT", "invalid")

		config := LoadAPIKeyConfigFromEnv()

		// Should use defaults for invalid values
		assert.Equal(t, 100.0, config.RateLimitPerSecond)
		assert.Equal(t, 200, config.RateLimitBurst)
		assert.Equal(t, 5*time.Minute, config.CleanupInterval)
		assert.Equal(t, 10*time.Minute, config.LimiterIdleTimeout)
	})
}

// TestDefaultAPIKeyConfig verifies default configuration values.
func TestDefaultAPIKeyConfig(t *testing.T) {
	config := DefaultAPIKeyConfig()

	assert.NotNil(t, config.APIKeys)
	assert.Empty(t, config.APIKeys)
	assert.Equal(t, 100.0, config.RateLimitPerSecond)
	assert.Equal(t, 200, config.RateLimitBurst)
	assert.Equal(t, 5*time.Minute, config.CleanupInterval)
	assert.Equal(t, 10*time.Minute, config.LimiterIdleTimeout)
}

// TestAPIKeyMiddleware_MultipleAPIKeys verifies that multiple valid API keys
// can be used interchangeably.
func TestAPIKeyMiddleware_MultipleAPIKeys(t *testing.T) {
	config := DefaultAPIKeyConfig()
	config.APIKeys = map[string]string{
		"key-alpha": "service-alpha",
		"key-beta":  "service-beta",
		"key-gamma": "service-gamma",
	}

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := GetAPIKeyIdentity(r.Context())
		w.Header().Set("X-Identity", identity)
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		apiKey           string
		expectedIdentity string
	}{
		{"key-alpha", "service-alpha"},
		{"key-beta", "service-beta"},
		{"key-gamma", "service-gamma"},
	}

	for _, tt := range tests {
		t.Run(tt.apiKey, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			req.Header.Set(APIKeyHeader, tt.apiKey)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.expectedIdentity, rec.Header().Get("X-Identity"))
		})
	}
}

// TestAPIKeyMiddleware_ZeroConfigValues verifies that zero config values
// are replaced with defaults.
func TestAPIKeyMiddleware_ZeroConfigValues(t *testing.T) {
	config := APIKeyConfig{
		APIKeys: map[string]string{"key": "identity"},
		// All other values are zero
	}

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	// If defaults weren't applied, the rate limiter wouldn't work properly
	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Should succeed because defaults are applied
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(APIKeyHeader, "key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// BenchmarkAPIKeyMiddleware_Authentication benchmarks the authentication path.
func BenchmarkAPIKeyMiddleware_Authentication(b *testing.B) {
	config := DefaultAPIKeyConfig()
	config.APIKeys = map[string]string{
		"bench-key": "bench-service",
	}
	// High rate limit to not interfere with benchmark
	config.RateLimitPerSecond = 1000000
	config.RateLimitBurst = 1000000

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(APIKeyHeader, "bench-key")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

// BenchmarkAPIKeyMiddleware_RateLimiter benchmarks the rate limiter performance.
func BenchmarkAPIKeyMiddleware_RateLimiter(b *testing.B) {
	config := DefaultAPIKeyConfig()
	// Create many API keys to test sync.Map performance
	config.APIKeys = make(map[string]string)
	for i := 0; i < 1000; i++ {
		config.APIKeys[fmt.Sprintf("key-%d", i)] = fmt.Sprintf("service-%d", i)
	}
	config.RateLimitPerSecond = 1000000
	config.RateLimitBurst = 1000000

	middleware := NewAPIKeyMiddleware(config)
	defer middleware.Close()

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%1000)
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			req.Header.Set(APIKeyHeader, key)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			i++
		}
	})
}
