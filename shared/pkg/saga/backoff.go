// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"math/rand/v2"
	"time"
)

// DefaultRetryBaseDelay is the default base delay for exponential backoff.
// Used when neither the global ClaimConfig nor a per-handler retry policy overrides it.
const DefaultRetryBaseDelay = 1 * time.Second

// DefaultRetryMaxDelay is the default upper bound on backoff delay.
// Used when neither the global ClaimConfig nor a per-handler retry policy overrides it.
const DefaultRetryMaxDelay = 5 * time.Minute

// CalculateBackoffDelay computes the wall-clock duration a saga should wait before
// it is eligible for another reclaim attempt after a transient failure.
//
// Formula: min(baseDelay * 2^replayCount + jitter, maxDelay)
// where jitter is a uniform random value in [0, baseDelay).
//
// Bounds:
//   - replayCount is clamped to a non-negative value; negative inputs return baseDelay + jitter.
//   - baseDelay <= 0 falls back to DefaultRetryBaseDelay so callers cannot accidentally
//     produce a zero-base sequence (which would yield zero growth and a thundering herd).
//   - maxDelay <= 0 falls back to DefaultRetryMaxDelay.
//   - High replayCount values that would overflow int64 are saturated to maxDelay,
//     so this function is safe for unexpectedly large counts (e.g., after MaxReplays
//     races) without panicking.
//
// Jitter is essential to prevent thundering herd: if N sagas hit the same outage at
// the same moment, deterministic backoff would have them all retry simultaneously.
// Adding [0, baseDelay) of random spread breaks the synchronization.
func CalculateBackoffDelay(replayCount int, baseDelay, maxDelay time.Duration) time.Duration {
	if baseDelay <= 0 {
		baseDelay = DefaultRetryBaseDelay
	}
	if maxDelay <= 0 {
		maxDelay = DefaultRetryMaxDelay
	}
	// If max <= base, the cap immediately applies once jitter is added.
	// Treat that as the legitimate "max" floor rather than an error.

	count := replayCount
	if count < 0 {
		count = 0
	}

	// Overflow guard: 2^63 wraps to a negative or zero shift. Anything beyond 62
	// already exceeds any sane maxDelay, so saturate.
	const maxShift = 62
	if count > maxShift {
		return maxDelay
	}

	// Exponential component: baseDelay * 2^count.
	// We compute the multiplier first to detect overflow before applying to baseDelay.
	multiplier := int64(1) << uint(count)

	// Overflow check on the multiplication itself.
	if multiplier > 0 && int64(baseDelay) > 0 &&
		multiplier > int64(maxDelay)/int64(baseDelay)+1 {
		// Even without jitter we'd exceed maxDelay - saturate.
		return maxDelay
	}

	delay := baseDelay * time.Duration(multiplier)
	if delay < 0 || delay > maxDelay {
		// Defensive: if anything overflowed past the cap (or wrapped negative), saturate.
		return maxDelay
	}

	// Add jitter in [0, baseDelay). rand.Int64N requires n > 0; we already
	// normalised baseDelay > 0 above.
	jitter := time.Duration(rand.Int64N(int64(baseDelay)))
	delay += jitter

	if delay > maxDelay || delay < 0 {
		return maxDelay
	}
	return delay
}
