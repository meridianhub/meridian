// Package saga provides saga orchestration runtime and persistence for durable execution.
// This file provides Prometheus metrics for saga zombie detection and replay count observability.
package saga

import (
	"time"

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

	// sagaSuspendedTotal tracks the total number of saga suspensions.
	sagaSuspendedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "saga_suspended_total",
			Help: "Total number of times sagas were suspended waiting for external events",
		},
	)

	// sagaSuspendTimeoutTotal tracks the total number of suspend timeouts.
	sagaSuspendTimeoutTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "saga_suspend_timeout_total",
			Help: "Total number of saga suspensions that timed out",
		},
	)

	// sagaResumedTotal tracks the total number of successful saga resumptions.
	sagaResumedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "saga_resumed_total",
			Help: "Total number of sagas successfully resumed from suspension",
		},
	)

	// sagaResumeIdempotentTotal tracks idempotent resume calls.
	sagaResumeIdempotentTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "saga_resume_idempotent_total",
			Help: "Total number of idempotent saga resume calls (already resumed)",
		},
	)

	// Orphan watcher metrics

	// sagaOrphanScansTotal tracks orphan scan operations.
	sagaOrphanScansTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "saga_orphan_scans_total",
			Help: "Total number of orphan scan operations performed",
		},
	)

	// sagaOrphanScanErrorsTotal tracks orphan scan failures.
	sagaOrphanScanErrorsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "saga_orphan_scan_errors_total",
			Help: "Total number of orphan scan errors",
		},
	)

	// sagaOrphansClaimedTotal tracks sagas claimed during orphan scans.
	sagaOrphansClaimedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "saga_orphans_claimed_total",
			Help: "Total number of orphaned sagas claimed",
		},
	)

	// sagaOrphanScanDuration tracks the duration of orphan scan operations.
	sagaOrphanScanDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "saga_orphan_scan_duration_seconds",
			Help:    "Duration of orphan scan operations in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
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

// RecordSuspend records that a saga was suspended.
func RecordSuspend() {
	sagaSuspendedTotal.Inc()
}

// RecordSuspendTimeout records that a saga suspension timed out.
func RecordSuspendTimeout() {
	sagaSuspendTimeoutTotal.Inc()
}

// RecordResume records that a saga was successfully resumed.
func RecordResume() {
	sagaResumedTotal.Inc()
}

// RecordResumeIdempotent records an idempotent resume call.
func RecordResumeIdempotent() {
	sagaResumeIdempotentTotal.Inc()
}

// RecordOrphanScan records an orphan scan operation with its duration.
func RecordOrphanScan(duration time.Duration) {
	sagaOrphanScansTotal.Inc()
	sagaOrphanScanDuration.Observe(duration.Seconds())
}

// RecordOrphanScanError records an orphan scan error.
func RecordOrphanScanError() {
	sagaOrphanScanErrorsTotal.Inc()
}

// RecordOrphansClaimed records the number of sagas claimed during an orphan scan.
func RecordOrphansClaimed(count int) {
	sagaOrphansClaimedTotal.Add(float64(count))
}

// ExposeMetricsForTesting provides access to the raw Prometheus metrics for testing.
// This should only be used in test code.
var ExposeMetricsForTesting = struct {
	ZombieDetectedTotal    *prometheus.CounterVec
	ReplayCount            prometheus.Histogram
	ReplayIncrementedTotal prometheus.Counter
	SuspendedTotal         prometheus.Counter
	SuspendTimeoutTotal    prometheus.Counter
	ResumedTotal           prometheus.Counter
	ResumeIdempotentTotal  prometheus.Counter
}{
	ZombieDetectedTotal:    sagaZombieDetectedTotal,
	ReplayCount:            sagaReplayCount,
	ReplayIncrementedTotal: sagaReplayIncrementedTotal,
	SuspendedTotal:         sagaSuspendedTotal,
	SuspendTimeoutTotal:    sagaSuspendTimeoutTotal,
	ResumedTotal:           sagaResumedTotal,
	ResumeIdempotentTotal:  sagaResumeIdempotentTotal,
}
