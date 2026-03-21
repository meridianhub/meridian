package audit

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/stretchr/testify/assert"
)

// newTestWorker creates a Worker with nil db for unit testing option functions.
// Do not call Start/Stop on this worker.
func newTestWorker() *Worker {
	return &Worker{
		batchSize:       defaultBatchSize,
		pollInterval:    defaultPollInterval,
		maxRetries:      defaultMaxRetries,
		minPollInterval: defaultMinPollInterval,
		maxPollInterval: defaultMaxPollInterval,
		shutdown:        make(chan struct{}),
	}
}

func TestWithBatchSize(t *testing.T) {
	t.Run("positive value is applied", func(t *testing.T) {
		w := newTestWorker()
		WithBatchSize(50)(w)
		assert.Equal(t, 50, w.batchSize)
	})

	t.Run("zero value is ignored", func(t *testing.T) {
		w := newTestWorker()
		WithBatchSize(0)(w)
		assert.Equal(t, defaultBatchSize, w.batchSize)
	})

	t.Run("negative value is ignored", func(t *testing.T) {
		w := newTestWorker()
		WithBatchSize(-10)(w)
		assert.Equal(t, defaultBatchSize, w.batchSize)
	})
}

func TestWithPollInterval(t *testing.T) {
	t.Run("positive duration is applied", func(t *testing.T) {
		w := newTestWorker()
		WithPollInterval(10 * time.Second)(w)
		assert.Equal(t, 10*time.Second, w.pollInterval)
	})

	t.Run("zero duration is ignored", func(t *testing.T) {
		w := newTestWorker()
		WithPollInterval(0)(w)
		assert.Equal(t, defaultPollInterval, w.pollInterval)
	})

	t.Run("negative duration is ignored", func(t *testing.T) {
		w := newTestWorker()
		WithPollInterval(-1 * time.Second)(w)
		assert.Equal(t, defaultPollInterval, w.pollInterval)
	})
}

func TestWithMaxRetries(t *testing.T) {
	t.Run("positive value is applied", func(t *testing.T) {
		w := newTestWorker()
		WithMaxRetries(5)(w)
		assert.Equal(t, 5, w.maxRetries)
	})

	t.Run("zero is accepted", func(t *testing.T) {
		w := newTestWorker()
		WithMaxRetries(0)(w)
		assert.Equal(t, 0, w.maxRetries)
	})

	t.Run("negative value is ignored", func(t *testing.T) {
		w := newTestWorker()
		WithMaxRetries(-1)(w)
		assert.Equal(t, defaultMaxRetries, w.maxRetries)
	})
}

func TestWithAdaptivePolling(t *testing.T) {
	t.Run("valid min and max are applied", func(t *testing.T) {
		w := newTestWorker()
		WithAdaptivePolling(100*time.Millisecond, 30*time.Second)(w)
		assert.True(t, w.adaptivePolling)
		assert.Equal(t, 100*time.Millisecond, w.minPollInterval)
		assert.Equal(t, 30*time.Second, w.maxPollInterval)
	})

	t.Run("inverted min/max are swapped", func(t *testing.T) {
		w := newTestWorker()
		WithAdaptivePolling(30*time.Second, 100*time.Millisecond)(w)
		assert.True(t, w.adaptivePolling)
		assert.Equal(t, 100*time.Millisecond, w.minPollInterval)
		assert.Equal(t, 30*time.Second, w.maxPollInterval)
	})

	t.Run("zero min interval is ignored", func(t *testing.T) {
		w := newTestWorker()
		WithAdaptivePolling(0, 30*time.Second)(w)
		assert.True(t, w.adaptivePolling)
		assert.Equal(t, defaultMinPollInterval, w.minPollInterval)
		assert.Equal(t, 30*time.Second, w.maxPollInterval)
	})

	t.Run("zero max interval is ignored", func(t *testing.T) {
		w := newTestWorker()
		WithAdaptivePolling(100*time.Millisecond, 0)(w)
		assert.True(t, w.adaptivePolling)
		assert.Equal(t, 100*time.Millisecond, w.minPollInterval)
		assert.Equal(t, defaultMaxPollInterval, w.maxPollInterval)
	})
}

func TestCalculateAdaptiveInterval(t *testing.T) {
	minInterval := defaults.DefaultRetryDelay // 100ms
	maxInterval := defaults.DefaultRPCTimeout // 30s

	t.Run("work found resets to min interval", func(t *testing.T) {
		w := newTestWorker()
		w.minPollInterval = minInterval
		w.maxPollInterval = maxInterval
		w.emptyPollCount = 5 // simulate previous idle state

		interval := w.calculateAdaptiveInterval(10)
		assert.Equal(t, minInterval, interval)
		assert.Equal(t, 0, w.emptyPollCount)
	})

	t.Run("no work increases interval exponentially", func(t *testing.T) {
		w := newTestWorker()
		w.minPollInterval = minInterval
		w.maxPollInterval = maxInterval

		// First empty poll: emptyPollCount becomes 1, multiplier = 2^1 = 2
		interval := w.calculateAdaptiveInterval(0)
		assert.Equal(t, 1, w.emptyPollCount)
		assert.Equal(t, 2*minInterval, interval)

		// Second empty poll: emptyPollCount becomes 2, multiplier = 2^2 = 4
		interval = w.calculateAdaptiveInterval(0)
		assert.Equal(t, 2, w.emptyPollCount)
		assert.Equal(t, 4*minInterval, interval)
	})

	t.Run("interval is capped at max", func(t *testing.T) {
		w := newTestWorker()
		w.minPollInterval = minInterval
		w.maxPollInterval = 200 * time.Millisecond // very small max
		w.emptyPollCount = 20                      // large count to exceed max

		interval := w.calculateAdaptiveInterval(0)
		assert.Equal(t, 200*time.Millisecond, interval)
	})
}
