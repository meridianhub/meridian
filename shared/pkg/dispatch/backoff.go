package dispatch

import (
	"math"
	"math/rand/v2"
	"time"
)

// CalculateNextRetry computes the next retry time using exponential backoff with a cap.
// attempt is the 1-based attempt number that just failed.
// Uses policy defaults for zero-valued fields: 1s initial backoff, 2.0 multiplier, 5m max.
func CalculateNextRetry(attempt int, policy RetryPolicy) time.Time {
	return time.Now().Add(calculateBackoff(attempt, policy))
}

// CalculateNextRetryWithJitter computes the next retry time using exponential backoff
// with added jitter to prevent thundering herd problems. The jitter adds a random
// duration between 0 and the calculated backoff, resulting in a total wait between
// 1x and 2x the base backoff.
func CalculateNextRetryWithJitter(attempt int, policy RetryPolicy) time.Time {
	backoff := calculateBackoff(attempt, policy)
	jitter := time.Duration(rand.Int64N(int64(backoff) + 1))
	return time.Now().Add(backoff + jitter)
}

// calculateBackoff computes the raw backoff duration for a given attempt and policy.
func calculateBackoff(attempt int, policy RetryPolicy) time.Duration {
	backoff := policy.InitialBackoff
	if backoff <= 0 {
		backoff = 1 * time.Second
	}

	multiplier := policy.BackoffMultiplier
	if multiplier < 1.0 {
		multiplier = 2.0
	}

	maxBackoff := policy.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 5 * time.Minute
	}

	// Exponential backoff: initialBackoff * multiplier^(attempt-1)
	// attempt is 1-based; first retry (attempt=1) uses initialBackoff directly.
	factor := math.Pow(multiplier, float64(attempt-1))
	waitFloat := float64(backoff) * factor
	// Cap in float64 space before converting to time.Duration to avoid
	// int64 overflow for extreme attempt counts.
	if waitFloat > float64(maxBackoff) {
		return maxBackoff
	}
	return time.Duration(waitFloat)
}
