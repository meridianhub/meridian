// Package observability provides Prometheus metrics and monitoring for the financial gateway service.
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// paymentsTotal counts all payment dispatches by tenant, rail, and terminal status.
	paymentsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "meridian_financial_gateway_payments_total",
			Help: "Total number of payment dispatches processed, labeled by tenant, payment rail, and terminal status.",
		},
		[]string{"tenant", "rail", "status"},
	)

	// dispatchDuration records how long a single dispatch attempt takes.
	dispatchDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "meridian_financial_gateway_dispatch_duration_seconds",
			Help:    "Duration of a dispatch attempt to an external payment rail in seconds.",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		},
		[]string{"tenant", "rail"},
	)

	// dispatchAttemptsTotal counts all dispatch attempts by tenant, rail, and outcome.
	dispatchAttemptsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "meridian_financial_gateway_dispatch_attempts_total",
			Help: "Total number of individual dispatch attempts to external payment rails.",
		},
		[]string{"tenant", "rail", "outcome"},
	)

	// circuitBreakerState tracks the circuit breaker state per payment rail provider.
	// One time series per state label: active state is 1, inactive states are 0.
	circuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "meridian_financial_gateway_circuit_breaker_state",
			Help: "One-hot circuit breaker state per payment rail provider and state label (1=active, 0=inactive).",
		},
		[]string{"tenant", "rail", "state"},
	)

	// activeDispatches tracks the number of dispatches currently in a given non-terminal status.
	activeDispatches = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "meridian_financial_gateway_active_dispatches",
			Help: "Current number of payment dispatches in non-terminal states, labeled by tenant and status.",
		},
		[]string{"tenant", "status"},
	)
)

// DispatchOutcome represents the result of a single dispatch attempt.
const (
	// DispatchOutcomeSuccess means the payment rail accepted the payment.
	DispatchOutcomeSuccess = "success"
	// DispatchOutcomeRetry means the attempt failed but will be retried.
	DispatchOutcomeRetry = "retry"
	// DispatchOutcomeFailure means the attempt failed permanently.
	DispatchOutcomeFailure = "failure"
	// DispatchOutcomeCircuitOpen means the circuit breaker blocked the attempt.
	DispatchOutcomeCircuitOpen = "circuit_open"
)

// RecordPayment increments the payments counter for the given terminal status.
// Call this when a payment reaches a terminal state (DELIVERED, ACKNOWLEDGED, FAILED).
func RecordPayment(tenant, rail, status string) {
	paymentsTotal.WithLabelValues(tenant, rail, status).Inc()
}

// RecordDispatchDuration records how long a dispatch attempt took.
func RecordDispatchDuration(tenant, rail string, duration time.Duration) {
	dispatchDuration.WithLabelValues(tenant, rail).Observe(duration.Seconds())
}

// knownDispatchOutcomes is the set of valid outcome label values.
// Any value outside this set is mapped to "unknown" to prevent cardinality blowup.
var knownDispatchOutcomes = map[string]struct{}{
	DispatchOutcomeSuccess:     {},
	DispatchOutcomeRetry:       {},
	DispatchOutcomeFailure:     {},
	DispatchOutcomeCircuitOpen: {},
}

// knownCircuitStates is the set of valid circuit breaker state label values.
var knownCircuitStates = []string{"closed", "half_open", "open"}

// RecordDispatchAttempt increments the dispatch attempts counter.
// outcome must be one of the DispatchOutcome* constants; unknown values are recorded as "unknown".
func RecordDispatchAttempt(tenant, rail, outcome string) {
	if _, ok := knownDispatchOutcomes[outcome]; !ok {
		outcome = "unknown"
	}
	dispatchAttemptsTotal.WithLabelValues(tenant, rail, outcome).Inc()
}

// RecordCircuitBreakerState updates the gauge for a specific provider's circuit breaker state.
// Resets all known states before setting the active one to guarantee the one-hot invariant.
// Unknown state values are silently ignored to prevent unbounded label cardinality.
func RecordCircuitBreakerState(tenant, rail, state string) {
	valid := false
	for _, s := range knownCircuitStates {
		if s == state {
			valid = true
			break
		}
	}
	if !valid {
		return
	}
	for _, s := range knownCircuitStates {
		circuitBreakerState.WithLabelValues(tenant, rail, s).Set(0)
	}
	circuitBreakerState.WithLabelValues(tenant, rail, state).Set(1)
}

// SetActiveDispatches sets the gauge for dispatches currently in a given status.
// count is clamped to 0 if negative to prevent invalid operational signals.
// Call this after each polling cycle with the current count per status.
func SetActiveDispatches(tenant, status string, count float64) {
	if count < 0 {
		count = 0
	}
	activeDispatches.WithLabelValues(tenant, status).Set(count)
}
