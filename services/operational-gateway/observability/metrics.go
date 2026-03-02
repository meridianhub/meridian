// Package observability provides Prometheus metrics and monitoring for the operational gateway service.
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// instructionsTotal counts all instructions by tenant, type, and terminal status.
	instructionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "meridian_gateway_instructions_total",
			Help: "Total number of instructions processed, labeled by tenant, instruction type, and terminal status.",
		},
		[]string{"tenant", "instruction_type", "status"},
	)

	// dispatchDuration records how long a single dispatch attempt takes.
	dispatchDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "meridian_gateway_dispatch_duration_seconds",
			Help:    "Duration of a dispatch attempt to an external provider in seconds.",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		},
		[]string{"tenant", "provider"},
	)

	// dispatchAttemptsTotal counts all dispatch attempts by tenant, provider, and outcome.
	dispatchAttemptsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "meridian_gateway_dispatch_attempts_total",
			Help: "Total number of individual dispatch attempts to external providers.",
		},
		[]string{"tenant", "provider", "outcome"},
	)

	// circuitBreakerState tracks the circuit breaker state per connection.
	// Values: 0 = CLOSED, 1 = HALF_OPEN, 2 = OPEN.
	circuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "meridian_gateway_circuit_breaker_state",
			Help: "Current circuit breaker state per provider connection (0=closed, 1=half-open, 2=open).",
		},
		[]string{"tenant", "connection_id", "state"},
	)

	// activeInstructions tracks the number of instructions currently in a given non-terminal status.
	activeInstructions = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "meridian_gateway_active_instructions",
			Help: "Current number of instructions in non-terminal states, labeled by tenant and status.",
		},
		[]string{"tenant", "status"},
	)
)

// DispatchOutcome represents the result of a single dispatch attempt.
const (
	// DispatchOutcomeSuccess means the provider accepted the instruction.
	DispatchOutcomeSuccess = "success"
	// DispatchOutcomeRetry means the attempt failed but will be retried.
	DispatchOutcomeRetry = "retry"
	// DispatchOutcomeFailure means the attempt failed permanently.
	DispatchOutcomeFailure = "failure"
	// DispatchOutcomeCircuitOpen means the circuit breaker blocked the attempt.
	DispatchOutcomeCircuitOpen = "circuit_open"
)

// CircuitBreakerStateValue encodes the circuit breaker state as a Prometheus gauge value.
type CircuitBreakerStateValue float64

const (
	// CircuitBreakerClosed represents a closed (healthy) circuit breaker.
	CircuitBreakerClosed CircuitBreakerStateValue = 0
	// CircuitBreakerHalfOpen represents a half-open (probing) circuit breaker.
	CircuitBreakerHalfOpen CircuitBreakerStateValue = 1
	// CircuitBreakerOpen represents an open (blocking) circuit breaker.
	CircuitBreakerOpen CircuitBreakerStateValue = 2
)

// RecordInstruction increments the instructions counter for the given terminal status.
// Call this when an instruction reaches a terminal state (DELIVERED, FAILED, EXPIRED, CANCELLED, ACKNOWLEDGED).
func RecordInstruction(tenant, instructionType, status string) {
	instructionsTotal.WithLabelValues(tenant, instructionType, status).Inc()
}

// RecordDispatchDuration records how long a dispatch attempt took.
func RecordDispatchDuration(tenant, provider string, duration time.Duration) {
	dispatchDuration.WithLabelValues(tenant, provider).Observe(duration.Seconds())
}

// RecordDispatchAttempt increments the dispatch attempts counter.
// outcome should be one of the DispatchOutcome* constants.
func RecordDispatchAttempt(tenant, provider, outcome string) {
	dispatchAttemptsTotal.WithLabelValues(tenant, provider, outcome).Inc()
}

// RecordCircuitBreakerState updates the gauge for a specific connection's circuit breaker state.
// state should be one of the CircuitBreaker* constants.
func RecordCircuitBreakerState(tenant, connectionID string, state CircuitBreakerStateValue) {
	// Reset all state label values for this connection before setting the current one
	// to avoid stale multi-value time series. Using three separate gauge vectors would
	// require separate Reset calls; instead we use a single gauge and set the value.
	circuitBreakerState.WithLabelValues(tenant, connectionID, "closed").Set(0)
	circuitBreakerState.WithLabelValues(tenant, connectionID, "half_open").Set(0)
	circuitBreakerState.WithLabelValues(tenant, connectionID, "open").Set(0)

	switch state {
	case CircuitBreakerClosed:
		circuitBreakerState.WithLabelValues(tenant, connectionID, "closed").Set(1)
	case CircuitBreakerHalfOpen:
		circuitBreakerState.WithLabelValues(tenant, connectionID, "half_open").Set(1)
	case CircuitBreakerOpen:
		circuitBreakerState.WithLabelValues(tenant, connectionID, "open").Set(1)
	}
}

// SetActiveInstructions sets the gauge for instructions currently in a given status.
// Call this after each polling cycle with the current count per status.
func SetActiveInstructions(tenant, status string, count float64) {
	activeInstructions.WithLabelValues(tenant, status).Set(count)
}

// IncrActiveInstructions increments the active instructions gauge.
// Call this when a new instruction enters a non-terminal status.
func IncrActiveInstructions(tenant, status string) {
	activeInstructions.WithLabelValues(tenant, status).Inc()
}

// DecrActiveInstructions decrements the active instructions gauge.
// Call this when an instruction leaves a status (transitions to another or reaches terminal).
func DecrActiveInstructions(tenant, status string) {
	activeInstructions.WithLabelValues(tenant, status).Dec()
}
