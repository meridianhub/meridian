// Package saga provides saga orchestration runtime and persistence for durable execution.
// This file provides Prometheus metrics for saga zombie detection and replay count observability.
package saga

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// sagaZombieDetectedTotal tracks the number of zombie sagas detected.
	// A zombie saga is one that has exceeded the maximum replay count.
	// Labels: saga_definition_id (the saga type identifier)
	sagaZombieDetectedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "saga_zombie_detected_total",
			Help: "Total number of zombie sagas detected (exceeded MAX_REPLAYS)",
		},
		[]string{"saga_definition_id"},
	)

	// sagaReplayCount tracks the distribution of replay counts.
	// This helps identify sagas that are frequently retrying.
	// Buckets: 0, 1, 2, 3, 5, 10, 20 to capture distribution of retries.
	sagaReplayCount = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "saga_replay_count",
			Help:    "Distribution of replay counts before saga success or failure",
			Buckets: []float64{0, 1, 2, 3, 5, 10, 20},
		},
	)

	// sagaReplayIncrementedTotal tracks when replay counts are incremented.
	// This is useful for monitoring saga retry patterns.
	sagaReplayIncrementedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "saga_replay_incremented_total",
			Help: "Total number of times saga replay counts were incremented",
		},
	)
)

// RecordZombieSagaDetected records that a zombie saga was detected.
// sagaDefinitionID is the UUID string of the saga definition.
func RecordZombieSagaDetected(sagaDefinitionID string) {
	sagaZombieDetectedTotal.WithLabelValues(sagaDefinitionID).Inc()
}

// RecordReplayCount records the replay count for observability.
// This should be called when a saga is claimed for replay.
func RecordReplayCount(count int) {
	sagaReplayCount.Observe(float64(count))
}

// RecordReplayIncrement records that a saga's replay count was incremented.
func RecordReplayIncrement() {
	sagaReplayIncrementedTotal.Inc()
}

// ExposeMetricsForTesting provides access to the raw Prometheus metrics for testing.
// This should only be used in test code.
var ExposeMetricsForTesting = struct {
	ZombieDetectedTotal    *prometheus.CounterVec
	ReplayCount            prometheus.Histogram
	ReplayIncrementedTotal prometheus.Counter
}{
	ZombieDetectedTotal:    sagaZombieDetectedTotal,
	ReplayCount:            sagaReplayCount,
	ReplayIncrementedTotal: sagaReplayIncrementedTotal,
}
