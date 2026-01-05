// Package worker provides background workers for the position-keeping service.
package worker

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Compaction error type constants for bounded label cardinality.
// Using constants prevents metric cardinality explosion from arbitrary error messages.
const (
	// ErrorTypeScan indicates an error scanning for fragmented buckets.
	ErrorTypeScan = "scan_error"
	// ErrorTypeLock indicates an error acquiring row locks.
	ErrorTypeLock = "lock_error"
	// ErrorTypeInsert indicates an error inserting consolidated row.
	ErrorTypeInsert = "insert_error"
	// ErrorTypeDelete indicates an error soft-deleting original rows.
	ErrorTypeDelete = "delete_error"
	// ErrorTypeTx indicates a transaction error.
	ErrorTypeTx = "tx_error"
)

var (
	// compactionRunsTotal counts total compaction runs.
	compactionRunsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "position_keeping",
			Name:      "compaction_runs_total",
			Help:      "Total number of compaction runs",
		},
	)

	// compactionBucketsCompactedTotal counts buckets compacted.
	compactionBucketsCompactedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "position_keeping",
			Name:      "compaction_buckets_compacted_total",
			Help:      "Total number of buckets compacted",
		},
	)

	// compactionRowsConsolidatedTotal counts rows consolidated.
	compactionRowsConsolidatedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "position_keeping",
			Name:      "compaction_rows_consolidated_total",
			Help:      "Total number of position rows consolidated",
		},
	)

	// compactionErrorsTotal counts compaction errors by error type.
	compactionErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "position_keeping",
			Name:      "compaction_errors_total",
			Help:      "Total number of compaction errors by error type",
		},
		[]string{"error_type"},
	)

	// compactionDurationSeconds tracks compaction duration.
	compactionDurationSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "meridian",
			Subsystem: "position_keeping",
			Name:      "compaction_duration_seconds",
			Help:      "Duration of compaction runs in seconds",
			Buckets:   []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
	)

	// fragmentedBucketsGauge shows current fragmented bucket count.
	fragmentedBucketsGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "position_keeping",
			Name:      "fragmented_buckets",
			Help:      "Current number of fragmented buckets above threshold",
		},
	)
)

// RecordCompactionRun increments the counter for total compaction runs.
func RecordCompactionRun() {
	compactionRunsTotal.Inc()
}

// RecordBucketCompacted increments the counter for buckets compacted.
func RecordBucketCompacted() {
	compactionBucketsCompactedTotal.Inc()
}

// RecordRowsConsolidated adds the count of rows consolidated to the counter.
func RecordRowsConsolidated(count int) {
	compactionRowsConsolidatedTotal.Add(float64(count))
}

// RecordCompactionError increments the error counter for the given error type.
// errorType should be one of the ErrorType* constants defined in this package.
func RecordCompactionError(errorType string) {
	compactionErrorsTotal.WithLabelValues(errorType).Inc()
}

// ObserveCompactionDuration records the duration of a compaction run.
func ObserveCompactionDuration(seconds float64) {
	compactionDurationSeconds.Observe(seconds)
}

// SetFragmentedBucketsCount sets the current number of fragmented buckets.
func SetFragmentedBucketsCount(count int) {
	fragmentedBucketsGauge.Set(float64(count))
}

// ExposeMetricsForTesting provides access to the raw Prometheus metrics for testing.
// This should only be used in test code.
var ExposeMetricsForTesting = struct {
	CompactionRunsTotal             prometheus.Counter
	CompactionBucketsCompactedTotal prometheus.Counter
	CompactionRowsConsolidatedTotal prometheus.Counter
	CompactionErrorsTotal           *prometheus.CounterVec
	CompactionDurationSeconds       prometheus.Histogram
	FragmentedBucketsGauge          prometheus.Gauge
}{
	CompactionRunsTotal:             compactionRunsTotal,
	CompactionBucketsCompactedTotal: compactionBucketsCompactedTotal,
	CompactionRowsConsolidatedTotal: compactionRowsConsolidatedTotal,
	CompactionErrorsTotal:           compactionErrorsTotal,
	CompactionDurationSeconds:       compactionDurationSeconds,
	FragmentedBucketsGauge:          fragmentedBucketsGauge,
}
