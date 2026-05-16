package saga

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCalculateBackoffDelay_ExponentialGrowth verifies the canonical doubling
// sequence with a small base and large cap so jitter remains within range.
func TestCalculateBackoffDelay_ExponentialGrowth(t *testing.T) {
	baseDelay := 1 * time.Second
	maxDelay := 1 * time.Hour

	// replayCount=0: [1s, 2s)        (base + jitter in [0, base))
	// replayCount=1: [2s, 3s)        (base*2 + jitter)
	// replayCount=2: [4s, 5s)        (base*4 + jitter)
	// replayCount=3: [8s, 9s)        (base*8 + jitter)
	cases := []struct {
		replayCount int
		minExpected time.Duration
		maxExpected time.Duration
	}{
		{0, 1 * time.Second, 2 * time.Second},
		{1, 2 * time.Second, 3 * time.Second},
		{2, 4 * time.Second, 5 * time.Second},
		{3, 8 * time.Second, 9 * time.Second},
		{4, 16 * time.Second, 17 * time.Second},
	}

	for _, c := range cases {
		// Run several iterations so we sample the jitter distribution.
		for i := 0; i < 20; i++ {
			d := CalculateBackoffDelay(c.replayCount, baseDelay, maxDelay)
			assert.GreaterOrEqual(t, d, c.minExpected,
				"replayCount=%d: delay %s should be >= %s", c.replayCount, d, c.minExpected)
			assert.Less(t, d, c.maxExpected,
				"replayCount=%d: delay %s should be < %s", c.replayCount, d, c.maxExpected)
		}
	}
}

// TestCalculateBackoffDelay_CappedAtMaxDelay verifies that very large replay
// counts saturate to maxDelay rather than overflowing.
func TestCalculateBackoffDelay_CappedAtMaxDelay(t *testing.T) {
	baseDelay := 1 * time.Second
	maxDelay := 30 * time.Second

	// replayCount=5 alone is 32s, exceeding 30s cap.
	d := CalculateBackoffDelay(5, baseDelay, maxDelay)
	assert.LessOrEqual(t, d, maxDelay, "replayCount=5 should be capped at maxDelay")

	// replayCount=10 (1024s) is far above cap.
	d = CalculateBackoffDelay(10, baseDelay, maxDelay)
	assert.LessOrEqual(t, d, maxDelay, "replayCount=10 should be capped at maxDelay")
}

// TestCalculateBackoffDelay_HighReplayCountNoOverflow verifies the overflow guard
// protects against absurdly large replay counts (defense in depth - MaxReplays
// would normally prevent these).
func TestCalculateBackoffDelay_HighReplayCountNoOverflow(t *testing.T) {
	baseDelay := 1 * time.Second
	maxDelay := 5 * time.Minute

	// Test boundary near int64 shift overflow.
	for _, replayCount := range []int{60, 62, 63, 64, 100, 1_000_000} {
		d := CalculateBackoffDelay(replayCount, baseDelay, maxDelay)
		assert.GreaterOrEqual(t, d, time.Duration(0),
			"replayCount=%d: delay must not wrap to negative", replayCount)
		assert.LessOrEqual(t, d, maxDelay,
			"replayCount=%d: delay must respect maxDelay", replayCount)
	}
}

// TestCalculateBackoffDelay_NegativeReplayCount treats negative input as zero
// (defense against caller bugs).
func TestCalculateBackoffDelay_NegativeReplayCount(t *testing.T) {
	baseDelay := 2 * time.Second
	maxDelay := 1 * time.Minute

	d := CalculateBackoffDelay(-5, baseDelay, maxDelay)
	assert.GreaterOrEqual(t, d, baseDelay, "negative replayCount should behave like 0")
	assert.Less(t, d, baseDelay+baseDelay, "should be base + jitter range")
}

// TestCalculateBackoffDelay_FallsBackOnNonPositiveBase verifies that callers passing
// zero or negative base/max do not get a zero-delay sequence (which would defeat backoff).
func TestCalculateBackoffDelay_FallsBackOnNonPositiveBase(t *testing.T) {
	// Both inputs zero - fall back to defaults.
	d := CalculateBackoffDelay(0, 0, 0)
	assert.GreaterOrEqual(t, d, DefaultRetryBaseDelay)
	assert.LessOrEqual(t, d, DefaultRetryMaxDelay)

	// Negative base - fall back.
	d = CalculateBackoffDelay(0, -1*time.Second, 10*time.Second)
	assert.GreaterOrEqual(t, d, DefaultRetryBaseDelay)
}

// TestCalculateBackoffDelay_JitterIsActuallyRandom verifies the jitter component
// produces varied delays across calls with the same inputs (thundering herd prevention).
func TestCalculateBackoffDelay_JitterIsActuallyRandom(t *testing.T) {
	baseDelay := 1 * time.Second
	maxDelay := 1 * time.Hour

	seen := make(map[time.Duration]struct{})
	for i := 0; i < 50; i++ {
		seen[CalculateBackoffDelay(0, baseDelay, maxDelay)] = struct{}{}
	}
	// 50 calls with [0, 1s) jitter at nanosecond resolution should produce
	// many distinct values; require at least 10 to flag a stuck RNG.
	assert.GreaterOrEqual(t, len(seen), 10, "jitter should produce varied delays across calls")
}
