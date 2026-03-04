package dispatch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCalculateNextRetry_FirstAttempt(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:       5,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        1 * time.Minute,
		BackoffMultiplier: 2.0,
	}

	before := time.Now()
	result := CalculateNextRetry(1, policy)
	after := time.Now()

	// First attempt (attempt=1) should use InitialBackoff * 2^0 = 1s
	assert.True(t, result.After(before.Add(900*time.Millisecond)), "retry should be at least ~1s in future")
	assert.True(t, result.Before(after.Add(2*time.Second)), "retry should not be too far in future")
}

func TestCalculateNextRetry_ExponentialGrowth(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:       5,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        1 * time.Minute,
		BackoffMultiplier: 2.0,
	}

	now := time.Now()
	retry1 := CalculateNextRetry(1, policy)
	retry2 := CalculateNextRetry(2, policy)
	retry3 := CalculateNextRetry(3, policy)

	// attempt=1: 1s, attempt=2: 2s, attempt=3: 4s
	assert.InDelta(t, 1*time.Second, retry1.Sub(now), float64(500*time.Millisecond))
	assert.InDelta(t, 2*time.Second, retry2.Sub(now), float64(500*time.Millisecond))
	assert.InDelta(t, 4*time.Second, retry3.Sub(now), float64(500*time.Millisecond))
}

func TestCalculateNextRetry_CapsAtMaxBackoff(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:       10,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        10 * time.Second,
		BackoffMultiplier: 2.0,
	}

	now := time.Now()
	// attempt=5: 1s * 2^4 = 16s, should be capped to 10s
	result := CalculateNextRetry(5, policy)
	wait := result.Sub(now)
	assert.InDelta(t, 10*time.Second, wait, float64(500*time.Millisecond))
}

func TestCalculateNextRetry_OverflowSafeForExtremeAttempts(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:       100,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        5 * time.Minute,
		BackoffMultiplier: 2.0,
	}

	now := time.Now()
	// attempt=100: 2^99 would overflow int64, should be capped to MaxBackoff
	result := CalculateNextRetry(100, policy)
	wait := result.Sub(now)
	assert.InDelta(t, 5*time.Minute, wait, float64(500*time.Millisecond))
	assert.True(t, wait > 0, "backoff should never be negative even for extreme attempts")
}

func TestCalculateNextRetry_DefaultsForZeroValues(t *testing.T) {
	// Zero-value policy should use sensible defaults
	policy := RetryPolicy{}

	before := time.Now()
	result := CalculateNextRetry(1, policy)
	after := time.Now()

	// Should return a time in the future (defaults: 1s backoff, 2.0 multiplier)
	assert.True(t, result.After(before), "retry should be in the future")
	assert.True(t, result.Before(after.Add(10*time.Second)), "retry should use reasonable defaults")
}

func TestCalculateNextRetry_MultiplierLessThanOne(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:       5,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        1 * time.Minute,
		BackoffMultiplier: 0.5, // invalid, should use default 2.0
	}

	now := time.Now()
	retry2 := CalculateNextRetry(2, policy)
	// With default multiplier 2.0: 1s * 2^1 = 2s
	assert.InDelta(t, 2*time.Second, retry2.Sub(now), float64(500*time.Millisecond))
}

func TestCalculateNextRetryWithJitter(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:       5,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        1 * time.Minute,
		BackoffMultiplier: 2.0,
	}

	now := time.Now()
	// With jitter, the result should vary between calls but stay within bounds
	// The base wait for attempt=1 is 1s; jitter adds 0-100% of that
	results := make([]time.Time, 100)
	for i := range results {
		results[i] = CalculateNextRetryWithJitter(1, policy)
	}

	for _, result := range results {
		wait := result.Sub(now)
		// Jitter: result should be between base and 2*base
		assert.True(t, wait >= 500*time.Millisecond, "jittered retry should be at least 500ms (base minus margin)")
		assert.True(t, wait <= 3*time.Second, "jittered retry should not exceed 2x base + margin")
	}

	// Check that there's variance (not all identical)
	allSame := true
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			allSame = false
			break
		}
	}
	assert.False(t, allSame, "jittered results should have variance")
}
