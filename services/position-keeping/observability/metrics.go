package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// NoOp fallback metrics - indicates degraded service functionality
	noopIdempotencyActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "position_keeping_noop_idempotency_active",
			Help: "1 if NoOp idempotency service is active (production risk), 0 otherwise",
		},
	)

	serviceDegradationEvents = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "position_keeping_service_degradation_events_total",
			Help: "Total number of service degradation events by component",
		},
		[]string{"component", "reason"},
	)
)

// Service component constants for degradation metrics.
const (
	ComponentIdempotency = "idempotency"
)

// Degradation reason constants.
const (
	DegradationReasonStartupFallback = "startup_fallback"
)

// SetNoopIdempotencyActive sets the gauge indicating whether NoOp idempotency is active.
// This metric MUST trigger a critical alert in production environments.
//
// ALERTING: This metric MUST have a Prometheus alert configured:
//
//	alert: NoopIdempotencyActiveInProduction
//	expr: position_keeping_noop_idempotency_active == 1 AND environment == "production"
//	severity: critical
//	runbook: docs/runbooks/noop-fallback-active.md
func SetNoopIdempotencyActive(active bool) {
	if active {
		noopIdempotencyActive.Set(1)
	} else {
		noopIdempotencyActive.Set(0)
	}
}

// RecordServiceDegradation records a service degradation event.
func RecordServiceDegradation(component, reason string) {
	serviceDegradationEvents.WithLabelValues(component, reason).Inc()
}
