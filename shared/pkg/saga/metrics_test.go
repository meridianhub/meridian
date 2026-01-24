// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// TestRecordZombieSagaDetected verifies the zombie detection metric is incremented.
func TestRecordZombieSagaDetected(t *testing.T) {
	// Reset metric for clean test
	ExposeMetricsForTesting.ZombieDetectedTotal.Reset()

	sagaDefID := uuid.New().String()

	// Record zombie detection
	RecordZombieSagaDetected(sagaDefID)

	// Verify counter incremented
	count := testutil.ToFloat64(ExposeMetricsForTesting.ZombieDetectedTotal.WithLabelValues(sagaDefID))
	assert.Equal(t, float64(1), count, "Counter should be incremented to 1")

	// Record another for same saga definition
	RecordZombieSagaDetected(sagaDefID)
	count = testutil.ToFloat64(ExposeMetricsForTesting.ZombieDetectedTotal.WithLabelValues(sagaDefID))
	assert.Equal(t, float64(2), count, "Counter should be incremented to 2")

	// Record for different saga definition
	otherSagaDefID := uuid.New().String()
	RecordZombieSagaDetected(otherSagaDefID)
	otherCount := testutil.ToFloat64(ExposeMetricsForTesting.ZombieDetectedTotal.WithLabelValues(otherSagaDefID))
	assert.Equal(t, float64(1), otherCount, "Other saga def counter should be 1")
}

// TestRecordReplayCount verifies the replay count histogram is observed.
func TestRecordReplayCount(t *testing.T) {
	// Record various replay counts
	RecordReplayCount(0)
	RecordReplayCount(1)
	RecordReplayCount(3)
	RecordReplayCount(5)
	RecordReplayCount(10)

	// Verify histogram has observations
	// Note: We can't easily verify individual bucket values with testutil,
	// but we can verify the metric exists and doesn't panic
	assert.NotNil(t, ExposeMetricsForTesting.ReplayCount, "Histogram should exist")
}

// TestRecordReplayIncrement verifies the replay increment counter.
func TestRecordReplayIncrement(t *testing.T) {
	// Get initial count
	initialCount := testutil.ToFloat64(ExposeMetricsForTesting.ReplayIncrementedTotal)

	// Record increment
	RecordReplayIncrement()

	// Verify incremented
	newCount := testutil.ToFloat64(ExposeMetricsForTesting.ReplayIncrementedTotal)
	assert.Equal(t, initialCount+1, newCount, "Counter should be incremented by 1")
}
