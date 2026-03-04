// Package observability provides Prometheus metrics for the event-router saga dispatch pipeline.
//
// Namespace: "meridian", Subsystem: "event_router"
//
// Metrics:
//   - events_received_total (counter, label: channel) — events entering the dispatch handler
//   - sagas_triggered_total (counter, labels: saga_name, channel) — successful saga triggers
//   - filter_evaluation_duration_seconds (histogram, label: saga_name) — CEL evaluation latency
//   - filter_evaluation_errors_total (counter, label: saga_name) — CEL evaluation failures
//   - chain_depth_exceeded_total (counter) — events dropped due to chain-depth limit
//   - saga_trigger_failures_total (counter, labels: saga_name, channel) — trigger infrastructure failures
//   - duplicate_events_total (counter, label: saga_name) — idempotency key collisions (reported by gRPC client)
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

	// filterEvaluationErrorsTotal counts CEL filter evaluation failures.
	// A non-zero rate indicates saga definitions contain invalid filter expressions.
	// Label: saga_name — the saga whose filter failed to evaluate.
	filterEvaluationErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "event_router",
			Name:      "filter_evaluation_errors_total",
			Help:      "Total number of CEL filter evaluation errors (saga skipped on error)",
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

	// sagaTriggerFailuresTotal counts saga trigger calls that returned an error
	// after all retries. These are infrastructure-level failures (gRPC errors).
	// Labels: saga_name — the saga that failed to trigger; channel — the event channel.
	sagaTriggerFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "event_router",
			Name:      "saga_trigger_failures_total",
			Help:      "Total number of saga trigger failures after all retries exhausted",
		},
		[]string{"saga_name", "channel"},
	)

	// duplicateEventsTotal counts saga trigger calls where the control-plane
	// responded with WasDuplicate=true (idempotency key already seen).
	// This counter is incremented by the gRPC adapter, not the dispatch handler,
	// because duplicate detection happens inside TriggerSaga after all retries.
	// Label: saga_name — the saga for which the duplicate was detected.
	duplicateEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "event_router",
			Name:      "duplicate_events_total",
			Help:      "Total number of saga trigger attempts that were idempotency duplicates",
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

// RecordFilterEvaluationError increments the filter evaluation error counter for a saga.
// Called when CEL evaluation returns an error (saga is skipped, not retried).
func RecordFilterEvaluationError(sagaName string) {
	filterEvaluationErrorsTotal.WithLabelValues(sagaName).Inc()
}

// RecordChainDepthExceeded increments the chain depth exceeded counter.
func RecordChainDepthExceeded() {
	chainDepthExceededTotal.Inc()
}

// RecordSagaTriggerFailure increments the saga trigger failure counter.
// Called when TriggerSaga returns a non-nil error after all retries are exhausted.
func RecordSagaTriggerFailure(sagaName, channel string) {
	sagaTriggerFailuresTotal.WithLabelValues(sagaName, channel).Inc()
}

// RecordDuplicateEvent increments the duplicate events counter for the given saga.
// This should be called by the gRPC adapter when the control-plane responds with
// WasDuplicate=true, not by the dispatch handler directly.
func RecordDuplicateEvent(sagaName string) {
	duplicateEventsTotal.WithLabelValues(sagaName).Inc()
}
