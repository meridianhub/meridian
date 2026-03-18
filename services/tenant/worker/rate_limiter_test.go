package worker

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAlertRateLimiter(t *testing.T) {
	t.Run("creates limiter with specified values", func(t *testing.T) {
		limiter := NewAlertRateLimiter(10, 10)
		require.NotNil(t, limiter)
		assert.Equal(t, 10, limiter.maxTokens)
	})

	t.Run("uses defaults for invalid values", func(t *testing.T) {
		limiter := NewAlertRateLimiter(0, 0)
		require.NotNil(t, limiter)
		assert.Equal(t, 10, limiter.maxTokens) // Default
	})
}

func TestAlertRateLimiter_Allow_BasicOperation(t *testing.T) {
	// Create a limiter with 10 alerts per minute, burst of 10
	limiter := NewAlertRateLimiter(10, 10)

	alertType := "test_alert"

	// First 10 alerts should be allowed
	for i := 0; i < 10; i++ {
		allowed := limiter.Allow(alertType)
		assert.True(t, allowed, "Alert %d should be allowed", i+1)
	}

	// 11th alert should be rate limited
	allowed := limiter.Allow(alertType)
	assert.False(t, allowed, "11th alert should be rate limited")
}

func TestAlertRateLimiter_Allow_DifferentTypes(t *testing.T) {
	limiter := NewAlertRateLimiter(5, 5)

	// Use all tokens for type A
	for i := 0; i < 5; i++ {
		limiter.Allow("type_a")
	}
	assert.False(t, limiter.Allow("type_a"), "type_a should be rate limited")

	// Type B should still have full capacity
	for i := 0; i < 5; i++ {
		allowed := limiter.Allow("type_b")
		assert.True(t, allowed, "type_b should be allowed")
	}
	assert.False(t, limiter.Allow("type_b"), "type_b should now be rate limited")
}

func TestAlertRateLimiter_Allow_TokenRefill(t *testing.T) {
	// Skip in short mode as this test relies on timing
	if testing.Short() {
		t.Skip("Skipping timing-sensitive test in short mode")
	}

	// Create limiter: 60 alerts per minute = 1 per second
	limiter := NewAlertRateLimiter(60, 2)

	alertType := "refill_test"

	// Use both tokens
	assert.True(t, limiter.Allow(alertType))
	assert.True(t, limiter.Allow(alertType))
	assert.False(t, limiter.Allow(alertType))

	time.Sleep(1100 * time.Millisecond) //nolint:forbidigo // triggers rate limiter token refill (rate limiting is time-based)

	// Should have 1 token refilled
	assert.True(t, limiter.Allow(alertType))
	assert.False(t, limiter.Allow(alertType))
}

func TestAlertRateLimiter_Allow_Callback(t *testing.T) {
	callbackCount := 0
	var callbackAlertType string
	callback := func(alertType string) {
		callbackCount++
		callbackAlertType = alertType
	}

	limiter := NewAlertRateLimiter(2, 2, WithRateLimitCallback(callback))

	// Use all tokens
	limiter.Allow("callback_test")
	limiter.Allow("callback_test")

	// This should trigger callback
	limiter.Allow("callback_test")

	assert.Equal(t, 1, callbackCount)
	assert.Equal(t, "callback_test", callbackAlertType)
}

func TestAlertRateLimiter_TokensRemaining(t *testing.T) {
	limiter := NewAlertRateLimiter(10, 10)
	alertType := "tokens_test"

	// Initially should have max tokens (new bucket)
	assert.Equal(t, 10, limiter.TokensRemaining(alertType))

	// After consuming some
	limiter.Allow(alertType)
	limiter.Allow(alertType)
	limiter.Allow(alertType)

	assert.Equal(t, 7, limiter.TokensRemaining(alertType))
}

func TestAlertRateLimiter_Reset(t *testing.T) {
	limiter := NewAlertRateLimiter(5, 5)
	alertType := "reset_test"

	// Use all tokens
	for i := 0; i < 5; i++ {
		limiter.Allow(alertType)
	}
	assert.False(t, limiter.Allow(alertType))

	// Reset
	limiter.Reset()

	// Should have full capacity again
	assert.True(t, limiter.Allow(alertType))
	assert.Equal(t, 4, limiter.TokensRemaining(alertType))
}

func TestAlertRateLimiter_ConcurrentAccess(t *testing.T) {
	limiter := NewAlertRateLimiter(100, 100)

	var wg sync.WaitGroup
	allowedCount := 0
	var mu sync.Mutex

	// Launch 200 goroutines trying to send alerts
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.Allow("concurrent_test") {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Exactly 100 should have been allowed
	assert.Equal(t, 100, allowedCount)
}
