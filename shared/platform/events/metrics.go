package events

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// outboxDepth tracks the number of pending entries per service
	outboxDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "event_outbox",
		Name:      "depth",
		Help:      "Number of pending entries in the event outbox by service",
	}, []string{"service"})

	// eventsPublished counts events published to Kafka
	eventsPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "event_outbox",
		Name:      "published_total",
		Help:      "Total number of events published from the outbox",
	}, []string{"service", "event_type", "status"})

	// dlqEntries counts entries moved to DLQ (retries exhausted)
	dlqEntries = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "event_outbox",
		Name:      "dlq_total",
		Help:      "Total number of events moved to DLQ after retries exhausted",
	}, []string{"service", "event_type"})

	// processingDuration tracks the duration of batch processing operations
	processingDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "event_outbox",
		Name:      "processing_duration_seconds",
		Help:      "Duration of batch processing operations in seconds",
		Buckets:   prometheus.DefBuckets,
	}, []string{"service"})

	// entryAge tracks how old entries are when they are processed (latency)
	entryAge = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "event_outbox",
		Name:      "entry_age_seconds",
		Help:      "Age of event entries when processed (time from creation to processing)",
		Buckets:   []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"service"})

	// stuckEntriesReset counts entries that were reset from stuck 'processing' state
	stuckEntriesReset = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "event_outbox",
		Name:      "stuck_entries_reset_total",
		Help:      "Total number of stuck entries reset from processing to pending",
	}, []string{"service"})

	// retriesTotal counts retry attempts
	retriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "event_outbox",
		Name:      "retries_total",
		Help:      "Total number of retry attempts for failed publishes",
	}, []string{"service", "event_type"})
)

// RecordOutboxDepth updates the gauge for the number of pending entries.
func RecordOutboxDepth(service string, depth int) {
	outboxDepth.WithLabelValues(service).Set(float64(depth))
}

// RecordPublished increments the counter for events published.
// status should be "success", "failure", or "timeout".
func RecordPublished(service, eventType, status string) {
	eventsPublished.WithLabelValues(service, eventType, status).Inc()
}

// RecordDLQEntry increments the counter for entries moved to DLQ.
func RecordDLQEntry(service, eventType string) {
	dlqEntries.WithLabelValues(service, eventType).Inc()
}

// RecordProcessingDuration observes the duration of a batch processing operation.
func RecordProcessingDuration(service string, seconds float64) {
	processingDuration.WithLabelValues(service).Observe(seconds)
}

// RecordEntryAge observes the age of an entry when it is processed.
func RecordEntryAge(service string, ageSeconds float64) {
	entryAge.WithLabelValues(service).Observe(ageSeconds)
}

// RecordStuckEntriesReset increments the counter for stuck entries reset.
func RecordStuckEntriesReset(service string, count int) {
	stuckEntriesReset.WithLabelValues(service).Add(float64(count))
}

// RecordRetry increments the counter for retry attempts.
func RecordRetry(service, eventType string) {
	retriesTotal.WithLabelValues(service, eventType).Inc()
}
