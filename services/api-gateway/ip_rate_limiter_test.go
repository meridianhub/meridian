package gateway_test

import (
	"testing"
	"time"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/stretchr/testify/assert"
)

func TestIPRateLimiter_AllowsBurstThenThrottles(t *testing.T) {
	rl := gateway.NewPerMinuteIPRateLimiter(10, 3) // burst 3
	t.Cleanup(rl.Stop)

	allowed := 0
	for i := 0; i < 10; i++ {
		if rl.Allow("10.0.0.1") {
			allowed++
		}
	}
	assert.Equal(t, 3, allowed, "only the burst of 3 should be allowed within the window")
}

func TestIPRateLimiter_IndependentPerIP(t *testing.T) {
	rl := gateway.NewPerMinuteIPRateLimiter(10, 2) // burst 2
	t.Cleanup(rl.Stop)

	// Exhaust the burst for the first IP.
	assert.True(t, rl.Allow("10.0.0.1"))
	assert.True(t, rl.Allow("10.0.0.1"))
	assert.False(t, rl.Allow("10.0.0.1"))

	// A different IP has its own independent bucket.
	assert.True(t, rl.Allow("10.0.0.2"))
	assert.True(t, rl.Allow("10.0.0.2"))
	assert.False(t, rl.Allow("10.0.0.2"))
}

func TestIPRateLimiter_CleanupEvictsStaleEntries(t *testing.T) {
	rl := gateway.NewPerMinuteIPRateLimiter(10, 1)
	t.Cleanup(rl.Stop)

	// Exhaust the single-token burst for this IP.
	assert.True(t, rl.Allow("10.0.0.1"))
	assert.False(t, rl.Allow("10.0.0.1"))

	// A negative max-age places the eviction cutoff in the future, so every
	// entry (including one just seen) is treated as stale and evicted. The next
	// request then gets a fresh bucket with its burst allowance restored.
	rl.Cleanup(-time.Hour)
	assert.True(t, rl.Allow("10.0.0.1"), "a fresh bucket should be created after cleanup")
}

func TestNewIPRateLimiter_ClampsBurst(t *testing.T) {
	// A burst below 1 is clamped to 1 so at least one request is always allowed.
	rl := gateway.NewIPRateLimiter(0, 0)
	t.Cleanup(rl.Stop)
	assert.True(t, rl.Allow("10.0.0.1"))
	assert.False(t, rl.Allow("10.0.0.1"))
}
