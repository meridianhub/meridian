// Package observability provides Prometheus metrics and monitoring for the InternalAccount service.
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Error category constants for bounded label cardinality.
// Using constants prevents metric cardinality explosion from arbitrary error messages.
const (
	ErrorCategoryValidation      = "validation"
	ErrorCategoryNotFound        = "not_found"
	ErrorCategoryDuplicate       = "duplicate"
	ErrorCategoryInternal        = "internal"
	ErrorCategoryDatabase        = "database"
	ErrorCategoryPositionKeeping = "position_keeping"
)

// Operation name constants for consistent metric labeling.
const (
	OperationInitiate   = "initiate"
	OperationUpdate     = "update"
	OperationControl    = "control"
	OperationRetrieve   = "retrieve"
	OperationList       = "list"
	OperationGetBalance = "get_balance"
)

// Status constants for operation outcomes.
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

// Repository operation result constants.
const (
	ResultSuccess = "success"
	ResultError   = "error"
)

var (
	// Operation duration metrics
	operationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "internal_account_operation_duration_seconds",
			Help:    "Duration of internal account operations in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"operation", "status"},
	)

	// RPC duration histogram with method and status labels
	rpcDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "internal_account_rpc_duration_seconds",
			Help:    "Duration of gRPC method calls in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"method", "status"},
	)

	// Balance query duration metric (target <50ms p99)
	// Separate histogram with finer-grained buckets for balance queries
	// Uses status label to distinguish success/error for SLO tracking
	balanceQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "internal_account_balance_query_duration_seconds",
			Help:    "Duration of balance queries to Position Keeping service in seconds (target p99 < 50ms)",
			Buckets: []float64{.005, .01, .025, .05, .075, .1, .15, .2, .25, .5, 1},
		},
		[]string{"status"},
	)

	// Account lifecycle metrics
	accountsCreated = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "internal_account_accounts_created_total",
			Help: "Total number of internal accounts created",
		},
		[]string{"account_type"},
	)

	accountStatusChanges = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "internal_account_status_changes_total",
			Help: "Total number of account status changes",
		},
		[]string{"from_status", "to_status"},
	)

	// Instrument validation metrics
	instrumentValidation = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "internal_account_instrument_validation_total",
			Help: "Total number of instrument validation attempts",
		},
		[]string{"result"},
	)

	instrumentValidationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "internal_account_instrument_validation_duration_seconds",
			Help:    "Duration of instrument validation calls to Reference Data service",
			Buckets: []float64{.001, .005, .01, .05, .1, .5, 1.0, 2.5, 5.0},
		},
		[]string{"result"},
	)

	// Repository operations counter
	repositoryOperations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "internal_account_repository_operations_total",
			Help: "Total number of repository operations by operation and result",
		},
		[]string{"operation", "result"},
	)

	// Health check metrics
	healthCheckTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "internal_account_health_check_total",
			Help: "Total number of health checks by component and status",
		},
		[]string{"component", "status"},
	)

	// Error metrics counter
	errorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "internal_account_errors_total",
			Help: "Total number of errors by category and operation",
		},
		[]string{"category", "operation"},
	)

	// In-flight operations gauge
	operationsInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "internal_account_operations_in_flight",
			Help: "Number of operations currently being processed",
		},
		[]string{"operation"},
	)

	// Circuit breaker metrics
	circuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "internal_account_circuit_breaker_state",
			Help: "Current state of circuit breakers (0=closed, 1=half-open, 2=open)",
		},
		[]string{"service"},
	)

	circuitBreakerStateChanges = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "internal_account_circuit_breaker_state_changes_total",
			Help: "Total number of circuit breaker state changes",
		},
		[]string{"service", "from_state", "to_state"},
	)
)

// RecordOperationDuration records the duration of an internal account operation.
func RecordOperationDuration(operation, status string, duration time.Duration) {
	operationDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordRPCDuration records the duration of a gRPC method call.
func RecordRPCDuration(method, status string, duration time.Duration) {
	rpcDuration.WithLabelValues(method, status).Observe(duration.Seconds())
}

// RecordBalanceQueryDuration records the duration of a balance query to Position Keeping service.
// Target p99 latency is <50ms. This metric uses finer-grained buckets optimized for low-latency operations.
func RecordBalanceQueryDuration(status string, duration time.Duration) {
	balanceQueryDuration.WithLabelValues(status).Observe(duration.Seconds())
}

// RecordAccountCreated records a newly created account.
func RecordAccountCreated(accountType string) {
	accountsCreated.WithLabelValues(accountType).Inc()
}

// RecordAccountStatusChange records an account status transition.
func RecordAccountStatusChange(fromStatus, toStatus string) {
	accountStatusChanges.WithLabelValues(fromStatus, toStatus).Inc()
}

// RecordInstrumentValidation records an instrument validation attempt with its result.
// Result should be one of: "success", "not_found", "not_active", "timeout", "error".
func RecordInstrumentValidation(result string, duration time.Duration) {
	instrumentValidation.WithLabelValues(result).Inc()
	instrumentValidationDuration.WithLabelValues(result).Observe(duration.Seconds())
}

// RecordRepositoryOperation records a repository operation with its result.
func RecordRepositoryOperation(operation, result string) {
	repositoryOperations.WithLabelValues(operation, result).Inc()
}

// RecordHealthCheck records a health check result.
func RecordHealthCheck(component, status string) {
	healthCheckTotal.WithLabelValues(component, status).Inc()
}

// RecordError records an error with category and operation context.
func RecordError(category, operation string) {
	errorsTotal.WithLabelValues(category, operation).Inc()
}

// IncOperationsInFlight increments the in-flight gauge for an operation.
func IncOperationsInFlight(operation string) {
	operationsInFlight.WithLabelValues(operation).Inc()
}

// DecOperationsInFlight decrements the in-flight gauge for an operation.
func DecOperationsInFlight(operation string) {
	operationsInFlight.WithLabelValues(operation).Dec()
}

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

// OperationTimer provides a convenient way to time operations and record metrics.
// It protects against double-observation which would cause incorrect gauge values.
type OperationTimer struct {
	operation string
	start     time.Time
	observed  bool
}

// NewOperationTimer creates a new timer and increments the in-flight gauge.
func NewOperationTimer(operation string) *OperationTimer {
	IncOperationsInFlight(operation)
	return &OperationTimer{
		operation: operation,
		start:     time.Now(),
		observed:  false,
	}
}

// ObserveSuccess records a successful operation and decrements in-flight gauge.
// Safe to call multiple times; only the first call has effect.
func (t *OperationTimer) ObserveSuccess() {
	if t.observed {
		return
	}
	t.observed = true
	DecOperationsInFlight(t.operation)
	RecordOperationDuration(t.operation, StatusSuccess, time.Since(t.start))
}

// ObserveError records a failed operation with error category and decrements in-flight gauge.
// Safe to call multiple times; only the first call has effect.
func (t *OperationTimer) ObserveError(errorCategory string) {
	if t.observed {
		return
	}
	t.observed = true
	DecOperationsInFlight(t.operation)
	RecordOperationDuration(t.operation, StatusError, time.Since(t.start))
	RecordError(errorCategory, t.operation)
}
