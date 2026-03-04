// Package observability provides Prometheus metrics for the event-router saga dispatch pipeline.
//
// Namespace: "meridian", Subsystem: "event_router"
//
// Metrics:
//   - events_received_total (counter, label: channel) — events entering the dispatch handler
//   - sagas_triggered_total (counter, labels: saga_name, channel) — successful saga triggers
//   - filter_evaluation_duration_seconds (histogram, label: saga_name) — CEL evaluation latency
//   - chain_depth_exceeded_total (counter) — events dropped due to chain-depth limit
//   - duplicate_events_total (counter, label: saga_name) — idempotency key collisions
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// eventsReceivedTotal counts all events entering the saga dispatch handler.
	// Label: channel — the event channel (e.g., "payments", "accounts").
	eventsReceivedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "event_router",
			Name:      "events_received_total",
			Help:      "Total number of events received by the saga dispatch handler",
		},
		[]string{"channel"},
	)

	// sagasTriggeredTotal counts successful saga trigger calls.
	// Labels: saga_name — the saga definition name; channel — the event channel.
	sagasTriggeredTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "event_router",
			Name:      "sagas_triggered_total",
			Help:      "Total number of sagas successfully triggered by the dispatch handler",
		},
		[]string{"saga_name", "channel"},
	)

	// filterEvaluationDuration tracks per-saga CEL filter evaluation latency.
	// Label: saga_name — the saga whose filter was evaluated.
	// Buckets cover the expected sub-millisecond to low-millisecond range.
	filterEvaluationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "meridian",
			Subsystem: "event_router",
			Name:      "filter_evaluation_duration_seconds",
			Help:      "Duration of CEL filter evaluation per saga in seconds",
			Buckets:   []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .25},
		},
		[]string{"saga_name"},
	)

	// chainDepthExceededTotal counts events dropped because the saga chain depth
	// reached or exceeded the configured maximum.
	chainDepthExceededTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "event_router",
			Name:      "chain_depth_exceeded_total",
			Help:      "Total number of events dropped due to saga chain depth limit",
		},
	)

	// duplicateEventsTotal counts saga trigger calls that were rejected as duplicates
	// by the idempotency key check.
	// Label: saga_name — the saga for which the duplicate was detected.
	duplicateEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "event_router",
			Name:      "duplicate_events_total",
			Help:      "Total number of duplicate saga trigger attempts detected by idempotency key",
		},
		[]string{"saga_name"},
	)
)

// RecordEventReceived increments the events received counter for the given channel.
func RecordEventReceived(channel string) {
	eventsReceivedTotal.WithLabelValues(channel).Inc()
}

// RecordSagaTriggered increments the sagas triggered counter.
func RecordSagaTriggered(sagaName, channel string) {
	sagasTriggeredTotal.WithLabelValues(sagaName, channel).Inc()
}

// RecordFilterEvaluationDuration observes the CEL filter evaluation duration for a saga.
func RecordFilterEvaluationDuration(sagaName string, durationSeconds float64) {
	filterEvaluationDuration.WithLabelValues(sagaName).Observe(durationSeconds)
}

// RecordChainDepthExceeded increments the chain depth exceeded counter.
func RecordChainDepthExceeded() {
	chainDepthExceededTotal.Inc()
}

// RecordDuplicateEvent increments the duplicate events counter for the given saga.
func RecordDuplicateEvent(sagaName string) {
	duplicateEventsTotal.WithLabelValues(sagaName).Inc()
}
