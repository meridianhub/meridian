package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/stretchr/testify/assert"
)

func TestRegistrationRateLimiter_AllowsUpToBurst(t *testing.T) {
	rl := gateway.NewRegistrationRateLimiter(3)

	// First 3 requests from the same IP should be allowed (burst).
	for i := 0; i < 3; i++ {
		assert.True(t, rl.Allow("192.168.1.1"), "request %d should be allowed", i+1)
	}

	// 4th request should be denied.
	assert.False(t, rl.Allow("192.168.1.1"), "4th request should be denied")
}

func TestRegistrationRateLimiter_DifferentIPsAreIndependent(t *testing.T) {
	rl := gateway.NewRegistrationRateLimiter(1)

	assert.True(t, rl.Allow("10.0.0.1"))
	assert.False(t, rl.Allow("10.0.0.1"))

	// Different IP is unaffected.
	assert.True(t, rl.Allow("10.0.0.2"))
}

func TestRegistrationRateLimiter_Cleanup(t *testing.T) {
	rl := gateway.NewRegistrationRateLimiter(5)

	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.2")

	// Cleanup with zero max age should remove all entries.
	rl.Cleanup(0)

	// After cleanup, IPs get fresh limiters (burst resets).
	for i := 0; i < 5; i++ {
		assert.True(t, rl.Allow("10.0.0.1"), "request %d should be allowed after cleanup", i+1)
	}
}

func TestClientIP_XForwardedFor(t *testing.T) {
	tests := []struct {
		name     string
		xff      string
		remote   string
		expected string
	}{
		{
			name:     "single IP in XFF",
			xff:      "203.0.113.50",
			remote:   "10.0.0.1:1234",
			expected: "203.0.113.50",
		},
		{
			name:     "multiple IPs in XFF, takes first",
			xff:      "203.0.113.50, 70.41.3.18, 150.172.238.178",
			remote:   "10.0.0.1:1234",
			expected: "203.0.113.50",
		},
		{
			name:     "no XFF, falls back to RemoteAddr",
			xff:      "",
			remote:   "192.168.1.100:5678",
			expected: "192.168.1.100",
		},
		{
			name:     "no XFF, RemoteAddr without port",
			xff:      "",
			remote:   "192.168.1.100",
			expected: "192.168.1.100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/register", nil)
			r.RemoteAddr = tt.remote
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			assert.Equal(t, tt.expected, gateway.ClientIP(r))
		})
	}
}

func TestRegistrationRateLimiter_CleanupPreservesRecent(t *testing.T) {
	rl := gateway.NewRegistrationRateLimiter(2)

	rl.Allow("10.0.0.1")

	// Cleanup entries older than 1 hour - recent entry should survive.
	rl.Cleanup(time.Hour)

	// The limiter should still exist with consumed tokens.
	assert.True(t, rl.Allow("10.0.0.1"), "should allow second request (burst=2)")
	assert.False(t, rl.Allow("10.0.0.1"), "should deny third request")
}
