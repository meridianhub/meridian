package scheduler_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := scheduler.DefaultConfig()

	assert.Equal(t, defaults.DefaultHealthCheckTimeout, cfg.PollInterval)
	assert.Equal(t, defaults.DefaultGracefulShutdown, cfg.ShutdownTimeout)
	assert.Equal(t, 5*time.Minute, cfg.MaxCatchUpAge)
}

// TestSchedulerMetrics verifies that the scheduler metric functions do not panic.
// These are thin prometheus wrappers - the test validates correct invocation.
func TestSchedulerMetrics(t *testing.T) {
	t.Run("RecordWorkerStart", func(_ *testing.T) {
		scheduler.RecordWorkerStart("test-worker")
	})

	t.Run("RecordWorkerStop", func(_ *testing.T) {
		scheduler.RecordWorkerStop("test-worker")
	})

	t.Run("RecordShutdownDuration", func(_ *testing.T) {
		scheduler.RecordShutdownDuration("test-worker", 0.5)
	})

	t.Run("RecordShutdownTimeout", func(_ *testing.T) {
		scheduler.RecordShutdownTimeout("test-worker")
	})

	t.Run("RecordInFlightWork", func(_ *testing.T) {
		scheduler.RecordInFlightWork("test-worker", 3.0)
		scheduler.RecordInFlightWork("test-worker", 0.0)
	})

	t.Run("RecordPoll", func(_ *testing.T) {
		scheduler.RecordPoll("test-worker")
	})
}
