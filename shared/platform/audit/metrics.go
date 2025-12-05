// Package audit provides background processing for audit outbox entries.
// It includes metrics collection, worker processing, and graceful shutdown capabilities.
package audit

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// outboxDepth tracks the number of pending entries in the audit outbox
	outboxDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "outbox_depth_total",
		Help:      "Number of pending entries in the audit outbox",
	})

	// outboxProcessed counts successfully processed audit entries
	outboxProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "outbox_processed_total",
		Help:      "Total number of successfully processed audit entries",
	})

	// outboxFailed counts failed audit entries (retries exhausted)
	outboxFailed = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "outbox_failed_total",
		Help:      "Total number of failed audit entries (retries exhausted)",
	})

	// processingDuration tracks the duration of batch processing operations
	processingDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "processing_duration_seconds",
		Help:      "Duration of batch processing operations in seconds",
		Buckets:   prometheus.DefBuckets,
	})

	// entryAge tracks how old entries are when they are processed (latency)
	entryAge = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "entry_age_seconds",
		Help:      "Age of audit entries when processed (time from creation to processing)",
		Buckets:   prometheus.DefBuckets,
	})
)

// RecordOutboxDepth updates the gauge for the number of pending entries
func RecordOutboxDepth(depth int) {
	outboxDepth.Set(float64(depth))
}

// RecordProcessed increments the counter for successfully processed entries
func RecordProcessed() {
	outboxProcessed.Inc()
}

// RecordFailed increments the counter for failed entries (retries exhausted)
func RecordFailed() {
	outboxFailed.Inc()
}

// RecordProcessingDuration observes the duration of a batch processing operation
func RecordProcessingDuration(seconds float64) {
	processingDuration.Observe(seconds)
}

// RecordEntryAge observes the age of an entry when it is processed
func RecordEntryAge(ageSeconds float64) {
	entryAge.Observe(ageSeconds)
}
