// Package observability provides Prometheus metrics and monitoring for the CurrentAccount service.
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
			Name:    "current_account_operation_duration_seconds",
			Help:    "Duration of current account operations in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"operation", "status"},
	)

	// Business metrics
	depositsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_deposits_total",
			Help: "Total number of deposit transactions",
		},
		[]string{"currency"},
	)

	withdrawalsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_withdrawals_total",
			Help: "Total number of withdrawal transactions",
		},
		[]string{"currency"},
	)

	balanceGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "current_account_balance_cents",
			Help: "Current account balance in cents",
		},
		[]string{"currency"},
	)

	// Saga metrics
	sagaFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_saga_failures_total",
			Help: "Total number of saga failures",
		},
		[]string{"operation", "failed_step"},
	)

	sagaCompensationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_saga_compensations_total",
			Help: "Total number of saga compensations executed",
		},
		[]string{"operation", "step"},
	)

	// Inline compensation metrics - for compensations that happen within a step
	// due to saga pattern limitations (step fails after side effects)
	inlineCompensationFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_inline_compensation_failures_total",
			Help: "Total number of inline compensation failures (requires manual intervention)",
		},
		[]string{"operation", "leg"},
	)

	sagaDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "current_account_saga_duration_seconds",
			Help:    "Duration of saga execution in seconds",
			Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"operation", "status"},
	)

	// External service error metrics
	externalServiceErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_external_service_errors_total",
			Help: "Total number of external service errors",
		},
		[]string{"service", "operation"},
	)

	// Party validation metrics
	partyValidationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "current_account_party_validation_duration_seconds",
			Help:    "Duration of party validation calls in seconds",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		},
		[]string{"success"},
	)

	// Circuit breaker metrics
	circuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "current_account_circuit_breaker_state",
			Help: "Current state of circuit breakers (0=closed, 1=half-open, 2=open)",
		},
		[]string{"service"},
	)

	circuitBreakerStateChanges = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_circuit_breaker_state_changes_total",
			Help: "Total number of circuit breaker state changes",
		},
		[]string{"service", "from_state", "to_state"},
	)
)

// RecordOperationDuration records the duration of a current account operation
func RecordOperationDuration(operation, status string, duration time.Duration) {
	operationDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordDeposit records a deposit transaction
func RecordDeposit(currency string) {
	depositsTotal.WithLabelValues(currency).Inc()
}

// RecordWithdrawal records a withdrawal transaction
func RecordWithdrawal(currency string) {
	withdrawalsTotal.WithLabelValues(currency).Inc()
}

// RecordBalance records the current account balance
func RecordBalance(balanceCents int64, currency string) {
	balanceGauge.WithLabelValues(currency).Set(float64(balanceCents))
}

// RecordSagaFailure records a saga failure
func RecordSagaFailure(operation, failedStep string) {
	sagaFailuresTotal.WithLabelValues(operation, failedStep).Inc()
}

// RecordSagaCompensation records a saga compensation
func RecordSagaCompensation(operation, step string) {
	sagaCompensationsTotal.WithLabelValues(operation, step).Inc()
}

// RecordInlineCompensationFailure records an inline compensation failure.
// These failures indicate that a compensating entry could not be created,
// requiring manual intervention to restore ledger integrity.
//
// ALERTING: This metric MUST have a Prometheus alert configured:
//
//	alert: InlineCompensationFailure
//	expr: increase(current_account_inline_compensation_failures_total[5m]) > 0
//	severity: critical
//	runbook: docs/runbooks/saga-failure-recovery.md
//
// See docs/runbooks/saga-failure-recovery.md for remediation steps.
func RecordInlineCompensationFailure(operation, leg string) {
	inlineCompensationFailuresTotal.WithLabelValues(operation, leg).Inc()
}

// RecordSagaDuration records the duration of a saga execution
func RecordSagaDuration(operation, status string, duration time.Duration) {
	sagaDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordExternalServiceError records an external service error
func RecordExternalServiceError(service, operation string) {
	externalServiceErrors.WithLabelValues(service, operation).Inc()
}

// RecordPartyValidationDuration records the duration of a party validation call
func RecordPartyValidationDuration(duration time.Duration, success bool) {
	successLabel := "false"
	if success {
		successLabel = "true"
	}
	partyValidationDuration.WithLabelValues(successLabel).Observe(duration.Seconds())
}

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState int

const (
	// CircuitBreakerStateClosed indicates the circuit is closed (healthy)
	CircuitBreakerStateClosed CircuitBreakerState = 0
	// CircuitBreakerStateHalfOpen indicates the circuit is testing recovery
	CircuitBreakerStateHalfOpen CircuitBreakerState = 1
	// CircuitBreakerStateOpen indicates the circuit is open (failing fast)
	CircuitBreakerStateOpen CircuitBreakerState = 2
)

// RecordCircuitBreakerState records the current state of a circuit breaker
func RecordCircuitBreakerState(service string, state CircuitBreakerState) {
	circuitBreakerState.WithLabelValues(service).Set(float64(state))
}

// RecordCircuitBreakerStateChange records a circuit breaker state transition
func RecordCircuitBreakerStateChange(service, fromState, toState string) {
	circuitBreakerStateChanges.WithLabelValues(service, fromState, toState).Inc()
}
