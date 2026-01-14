// Package observability provides Prometheus metrics and monitoring for the InternalBankAccount service.
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Operation duration metrics
	operationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "internal_bank_account_operation_duration_seconds",
			Help:    "Duration of internal bank account operations in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"operation", "status"},
	)

	// Balance query duration metric (target <50ms p99)
	// Separate histogram with finer-grained buckets for balance queries
	balanceQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "internal_bank_account_balance_query_duration_seconds",
			Help:    "Duration of balance queries to Position Keeping service in seconds (target p99 < 50ms)",
			Buckets: []float64{.005, .01, .025, .05, .075, .1, .15, .2, .25, .5, 1},
		},
		[]string{"status"},
	)

	// Account lifecycle metrics
	accountsCreated = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "internal_bank_account_accounts_created_total",
			Help: "Total number of internal bank accounts created",
		},
		[]string{"account_type"},
	)

	accountStatusChanges = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "internal_bank_account_status_changes_total",
			Help: "Total number of account status changes",
		},
		[]string{"from_status", "to_status"},
	)
)

// RecordOperationDuration records the duration of an internal bank account operation.
func RecordOperationDuration(operation, status string, duration time.Duration) {
	operationDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordAccountCreated records a newly created account.
func RecordAccountCreated(accountType string) {
	accountsCreated.WithLabelValues(accountType).Inc()
}

// RecordAccountStatusChange records an account status transition.
func RecordAccountStatusChange(fromStatus, toStatus string) {
	accountStatusChanges.WithLabelValues(fromStatus, toStatus).Inc()
}

// RecordBalanceQueryDuration records the duration of a balance query to Position Keeping service.
// Target p99 latency is <50ms. This metric uses finer-grained buckets optimized for low-latency operations.
func RecordBalanceQueryDuration(status string, duration time.Duration) {
	balanceQueryDuration.WithLabelValues(status).Observe(duration.Seconds())
}

// Circuit breaker metrics
var (
	circuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "internal_bank_account_circuit_breaker_state",
			Help: "Current state of circuit breakers (0=closed, 1=half-open, 2=open)",
		},
		[]string{"service"},
	)

	circuitBreakerStateChanges = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "internal_bank_account_circuit_breaker_state_changes_total",
			Help: "Total number of circuit breaker state changes",
		},
		[]string{"service", "from_state", "to_state"},
	)
)

// CircuitBreakerState represents the state of a circuit breaker.
type CircuitBreakerState int

const (
	// CircuitBreakerStateClosed indicates the circuit is closed (healthy).
	CircuitBreakerStateClosed CircuitBreakerState = 0
	// CircuitBreakerStateHalfOpen indicates the circuit is testing recovery.
	CircuitBreakerStateHalfOpen CircuitBreakerState = 1
	// CircuitBreakerStateOpen indicates the circuit is open (failing fast).
	CircuitBreakerStateOpen CircuitBreakerState = 2
)

// RecordCircuitBreakerState records the current state of a circuit breaker.
func RecordCircuitBreakerState(service string, state CircuitBreakerState) {
	circuitBreakerState.WithLabelValues(service).Set(float64(state))
}

// RecordCircuitBreakerStateChange records a circuit breaker state transition.
func RecordCircuitBreakerStateChange(service, fromState, toState string) {
	circuitBreakerStateChanges.WithLabelValues(service, fromState, toState).Inc()
}
