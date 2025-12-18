// Package audit provides background processing for audit outbox entries.
// It includes metrics collection, worker processing, and graceful shutdown capabilities.
package audit

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// outboxDepthBySchema tracks the number of pending entries per schema
	outboxDepthBySchema = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "outbox_depth",
		Help:      "Number of pending entries in the audit outbox by schema",
	}, []string{"schema"})

	// outboxProcessedBySchema counts successfully processed audit entries per schema
	outboxProcessedBySchema = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "outbox_processed_total",
		Help:      "Total number of successfully processed audit entries by schema",
	}, []string{"schema"})

	// outboxFailedBySchema counts failed audit entries per schema
	outboxFailedBySchema = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "outbox_failed_total",
		Help:      "Total number of failed audit entries by schema",
	}, []string{"schema"})

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

// RecordOutboxDepthBySchema updates the gauge for the number of pending entries for a specific schema.
func RecordOutboxDepthBySchema(schema string, depth int) {
	outboxDepthBySchema.WithLabelValues(schema).Set(float64(depth))
}

// RecordProcessedBySchema increments the counter for successfully processed entries for a specific schema.
func RecordProcessedBySchema(schema string) {
	outboxProcessedBySchema.WithLabelValues(schema).Inc()
}

// RecordFailedBySchema increments the counter for failed entries for a specific schema.
func RecordFailedBySchema(schema string) {
	outboxFailedBySchema.WithLabelValues(schema).Inc()
}

// RecordProcessingDuration observes the duration of a batch processing operation
func RecordProcessingDuration(seconds float64) {
	processingDuration.Observe(seconds)
}

// RecordEntryAge observes the age of an entry when it is processed
func RecordEntryAge(ageSeconds float64) {
	entryAge.Observe(ageSeconds)
}
