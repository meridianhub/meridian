// Package worker implements background workers for tenant provisioning.
package worker

import (
	"sync"
	"time"
)

// AlertRateLimiter implements a token bucket rate limiter for alert delivery.
// Each alert type has its own bucket to prevent one noisy alert type from
// exhausting the budget for others.
type AlertRateLimiter struct {
	mu             sync.Mutex
	maxTokens      int
	refillRate     time.Duration // How often to add a token
	buckets        map[string]*tokenBucket
	onRateLimitHit func(alertType string) // Callback when rate limit is hit
}

// tokenBucket tracks tokens for a single alert type.
type tokenBucket struct {
	tokens     int
	lastRefill time.Time
}

// RateLimiterOption configures the AlertRateLimiter.
type RateLimiterOption func(*AlertRateLimiter)

// WithRateLimitCallback sets a callback function invoked when rate limit is hit.
// Useful for emitting metrics.
func WithRateLimitCallback(fn func(alertType string)) RateLimiterOption {
	return func(r *AlertRateLimiter) {
		r.onRateLimitHit = fn
	}
}

// NewAlertRateLimiter creates a new rate limiter for alerts.
// maxPerMinute: maximum alerts per minute per alert type (e.g., 10)
// burstSize: maximum burst capacity (typically same as maxPerMinute)
func NewAlertRateLimiter(maxPerMinute, burstSize int, opts ...RateLimiterOption) *AlertRateLimiter {
	if maxPerMinute <= 0 {
		maxPerMinute = 10
	}
	if burstSize <= 0 {
		burstSize = maxPerMinute
	}

	// Calculate refill rate: 1 token per (60/maxPerMinute) seconds
	refillRate := time.Minute / time.Duration(maxPerMinute)

	r := &AlertRateLimiter{
		maxTokens:  burstSize,
		refillRate: refillRate,
		buckets:    make(map[string]*tokenBucket),
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Allow checks if an alert of the given type is allowed.
// Returns true if the alert can proceed, false if rate limited.
func (r *AlertRateLimiter) Allow(alertType string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	bucket, exists := r.buckets[alertType]
	if !exists {
		// New alert type - create bucket with full tokens
		bucket = &tokenBucket{
			tokens:     r.maxTokens,
			lastRefill: time.Now(),
		}
		r.buckets[alertType] = bucket
	}

	// Refill tokens based on elapsed time
	r.refillBucket(bucket)

	// Check if we have tokens available
	if bucket.tokens > 0 {
		bucket.tokens--
		return true
	}

	// Rate limited - invoke callback if set
	if r.onRateLimitHit != nil {
		r.onRateLimitHit(alertType)
	}

	return false
}

// refillBucket adds tokens based on elapsed time since last refill.
// Must be called with mutex held.
func (r *AlertRateLimiter) refillBucket(bucket *tokenBucket) {
	now := time.Now()
	elapsed := now.Sub(bucket.lastRefill)

	// Calculate how many tokens to add
	tokensToAdd := int(elapsed / r.refillRate)
	if tokensToAdd > 0 {
		bucket.tokens += tokensToAdd
		if bucket.tokens > r.maxTokens {
			bucket.tokens = r.maxTokens
		}
		// Advance lastRefill by the number of tokens added (not to now, to avoid losing partial time)
		bucket.lastRefill = bucket.lastRefill.Add(time.Duration(tokensToAdd) * r.refillRate)
	}
}

// Reset clears all rate limit state. Useful for testing.
func (r *AlertRateLimiter) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buckets = make(map[string]*tokenBucket)
}

// TokensRemaining returns the number of tokens remaining for the given alert type.
// Useful for testing and debugging.
func (r *AlertRateLimiter) TokensRemaining(alertType string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	bucket, exists := r.buckets[alertType]
	if !exists {
		return r.maxTokens
	}

	// Refill before reporting
	r.refillBucket(bucket)
	return bucket.tokens
}
