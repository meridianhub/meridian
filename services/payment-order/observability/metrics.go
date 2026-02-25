// Package observability provides Prometheus metrics and monitoring for the PaymentOrder service.
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Payment order metrics
	paymentOrdersTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_total",
			Help: "Total number of payment orders by status",
		},
		[]string{"status"},
	)

	// Operation duration metrics
	operationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_order_operation_duration_seconds",
			Help:    "Duration of payment order operations in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"operation", "status"},
	)

	paymentOrderDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_order_duration_seconds",
			Help:    "Duration of payment order operations in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"operation", "status"},
	)

	// Gateway callback metrics
	gatewayCallbacksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_gateway_callbacks_total",
			Help: "Total number of gateway callback updates received",
		},
		[]string{"gateway_status", "result"},
	)

	// Payment completion metrics
	completionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_completions_total",
			Help: "Total number of completed payment orders",
		},
		[]string{"currency"},
	)

	// Payment rejection metrics
	// Note: error_category uses bounded values (gateway_rejected, insufficient_funds, validation_error, internal_error)
	// to prevent metric cardinality explosion from arbitrary gateway error codes
	rejectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_rejections_total",
			Help: "Total number of rejected payment orders",
		},
		[]string{"currency", "error_category"},
	)

	// Lien execution metrics
	lienExecutionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_lien_executions_total",
			Help: "Total number of lien execution attempts",
		},
		[]string{"status"},
	)

	// Lien execution status update exhaustion - indicates reconciliation needed
	lienExecutionStatusUpdateExhausted = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "payment_order_lien_execution_status_update_exhausted_total",
			Help: "Total number of lien execution status updates that exhausted all retries due to version conflicts",
		},
	)

	// Payment amount metrics
	paymentAmountTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_amount_cents_total",
			Help: "Total amount processed in cents",
		},
		[]string{"currency", "outcome"},
	)

	// Saga metrics
	sagaFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_saga_failures_total",
			Help: "Total number of saga failures",
		},
		[]string{"failed_step"},
	)

	sagaDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_order_saga_duration_seconds",
			Help:    "Duration of saga execution in seconds",
			Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		},
		[]string{"status"},
	)

	// Saga stage metrics
	sagaStageDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_order_saga_stage_duration_seconds",
			Help:    "Duration of saga stages in seconds",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		},
		[]string{"stage", "status"},
	)

	sagaCompensationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_saga_compensation_total",
			Help: "Total number of saga compensations by reason",
		},
		[]string{"reason"},
	)

	// Idempotency metrics
	idempotentRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_idempotent_requests_total",
			Help: "Total number of idempotent (duplicate) requests handled",
		},
		[]string{"operation"},
	)

	// Lien operation metrics
	lienOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_lien_operations_total",
			Help: "Total number of lien operations",
		},
		[]string{"operation", "status"},
	)

	lienOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_order_lien_operation_duration_seconds",
			Help:    "Duration of lien operations in seconds",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"operation"},
	)

	// Payment gateway metrics
	gatewayRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_order_gateway_request_duration_seconds",
			Help:    "Duration of payment gateway requests in seconds",
			Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"status"},
	)

	gatewayRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_gateway_requests_total",
			Help: "Total number of gateway requests by status",
		},
		[]string{"status"},
	)

	// External service error metrics
	externalServiceErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_external_service_errors_total",
			Help: "Total number of external service errors",
		},
		[]string{"service", "operation"},
	)

	// In-flight payment orders gauge
	paymentOrdersInFlight = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "payment_order_in_flight",
			Help: "Number of payment orders currently being processed",
		},
	)

	// Clearing account resolver metrics
	clearingAccountCacheHits = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "payment_order_clearing_account_cache_hits_total",
			Help: "Total number of clearing account cache hits",
		},
	)

	clearingAccountCacheMisses = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "payment_order_clearing_account_cache_misses_total",
			Help: "Total number of clearing account cache misses",
		},
	)

	clearingAccountLookupDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "payment_order_clearing_account_lookup_duration_seconds",
			Help:    "Duration of clearing account lookups from Internal Account service",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		},
	)

	clearingAccountLookupErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_clearing_account_lookup_errors_total",
			Help: "Total number of clearing account lookup errors",
		},
		[]string{"clearing_type"},
	)

	// Distributed lock metrics
	lienExecutionLockContentions = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "payment_order_lien_execution_lock_contentions_total",
			Help: "Total number of lien execution lock contentions (lock already held)",
		},
	)

	lienExecutionLockWaitDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "payment_order_lien_execution_lock_wait_seconds",
			Help:    "Time spent waiting to acquire lien execution lock",
			Buckets: []float64{.001, .005, .01, .05, .1, .5, 1.0, 5.0},
		},
	)

	// Bucket evaluation metrics - tracks CEL expression evaluation for non-fungible instruments
	// Note: instrument_code is intentionally excluded to prevent cardinality explosion
	// in multi-tenant environments where tenants can define custom instruments (ADR-0014).
	// Instrument details are preserved in structured logs for debugging.
	bucketEvaluationFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_bucket_evaluation_failures_total",
			Help: "Total number of bucket ID evaluation failures by error type",
		},
		[]string{"error_type"},
	)

	bucketEvaluationDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "payment_order_bucket_evaluation_duration_seconds",
			Help:    "Time spent evaluating bucket ID via CEL expression",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5},
		},
	)

	bucketEvaluationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_bucket_evaluations_total",
			Help: "Total number of bucket ID evaluations by result status",
		},
		[]string{"status"},
	)

	// Lien execution retry metrics - tracks retry behavior and exhaustion
	lienExecutionRetries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_lien_execution_retries_total",
			Help: "Total number of lien execution retry attempts by outcome",
		},
		[]string{"outcome"},
	)

	lienExecutionRetriesExhausted = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "payment_order_lien_execution_retries_exhausted_total",
			Help: "Total number of payment orders where lien execution retries were exhausted",
		},
	)

	// NoOp fallback metrics - indicates degraded service functionality
	noopIdempotencyActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "payment_order_noop_idempotency_active",
			Help: "1 if NoOp idempotency service is active (production risk), 0 otherwise",
		},
	)

	serviceDegradationEvents = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_service_degradation_events_total",
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
//	expr: payment_order_noop_idempotency_active == 1 AND environment == "production"
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

// RecordPaymentOrder records a payment order by status.
func RecordPaymentOrder(status string) {
	paymentOrdersTotal.WithLabelValues(status).Inc()
}

// RecordOperationDuration records the duration of a payment order operation
func RecordOperationDuration(operation, status string, duration time.Duration) {
	operationDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordPaymentOrderDuration records the duration of a payment order operation.
func RecordPaymentOrderDuration(operation, status string, duration time.Duration) {
	paymentOrderDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordGatewayCallback records a gateway callback update
func RecordGatewayCallback(gatewayStatus, result string) {
	gatewayCallbacksTotal.WithLabelValues(gatewayStatus, result).Inc()
}

// RecordCompletion records a completed payment order
func RecordCompletion(currency string) {
	completionsTotal.WithLabelValues(currency).Inc()
}

// Error category constants for bounded cardinality
const (
	ErrorCategoryGatewayRejected   = "gateway_rejected"
	ErrorCategoryInsufficientFunds = "insufficient_funds"
	ErrorCategoryValidationError   = "validation_error"
	ErrorCategoryInternalError     = "internal_error"
)

// RecordRejection records a rejected payment order.
// errorCategory should be one of the ErrorCategory* constants to ensure bounded cardinality.
func RecordRejection(currency, errorCategory string) {
	rejectionsTotal.WithLabelValues(currency, errorCategory).Inc()
}

// RecordLienExecution records a lien execution attempt
func RecordLienExecution(status string) {
	lienExecutionsTotal.WithLabelValues(status).Inc()
}

// RecordLienExecutionStatusUpdateExhausted records when a lien execution status update
// exhausts all retries due to version conflicts. This indicates the payment order
// may be stuck in PENDING state and requires reconciliation.
func RecordLienExecutionStatusUpdateExhausted() {
	lienExecutionStatusUpdateExhausted.Inc()
}

// RecordPaymentAmount records the payment amount processed
func RecordPaymentAmount(currency, outcome string, amountCents int64) {
	paymentAmountTotal.WithLabelValues(currency, outcome).Add(float64(amountCents))
}

// RecordSagaFailure records a saga failure
func RecordSagaFailure(failedStep string) {
	sagaFailuresTotal.WithLabelValues(failedStep).Inc()
}

// RecordSagaDuration records the duration of a saga execution
func RecordSagaDuration(status string, duration time.Duration) {
	sagaDuration.WithLabelValues(status).Observe(duration.Seconds())
}

// RecordSagaStageDuration records the duration of a saga stage.
func RecordSagaStageDuration(stage, status string, duration time.Duration) {
	sagaStageDuration.WithLabelValues(stage, status).Observe(duration.Seconds())
}

// RecordSagaCompensation records a saga compensation.
func RecordSagaCompensation(reason string) {
	sagaCompensationsTotal.WithLabelValues(reason).Inc()
}

// RecordIdempotentRequest records an idempotent (duplicate) request
func RecordIdempotentRequest(operation string) {
	idempotentRequestsTotal.WithLabelValues(operation).Inc()
}

// RecordLienOperation records a lien operation.
func RecordLienOperation(operation, status string) {
	lienOperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordLienOperationDuration records the duration of a lien operation.
func RecordLienOperationDuration(operation string, duration time.Duration) {
	lienOperationDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

// RecordGatewayLatency records payment gateway request duration.
func RecordGatewayLatency(status string, duration time.Duration) {
	gatewayRequestDuration.WithLabelValues(status).Observe(duration.Seconds())
	gatewayRequestsTotal.WithLabelValues(status).Inc()
}

// RecordExternalServiceError records an external service error.
func RecordExternalServiceError(service, operation string) {
	externalServiceErrors.WithLabelValues(service, operation).Inc()
}

// IncPaymentOrdersInFlight increments the in-flight gauge.
func IncPaymentOrdersInFlight() {
	paymentOrdersInFlight.Inc()
}

// DecPaymentOrdersInFlight decrements the in-flight gauge.
func DecPaymentOrdersInFlight() {
	paymentOrdersInFlight.Dec()
}

// RecordClearingAccountCacheHit records a cache hit for clearing account resolution.
func RecordClearingAccountCacheHit() {
	clearingAccountCacheHits.Inc()
}

// RecordClearingAccountCacheMiss records a cache miss for clearing account resolution.
func RecordClearingAccountCacheMiss() {
	clearingAccountCacheMisses.Inc()
}

// RecordClearingAccountLookupDuration records the duration of a clearing account lookup.
func RecordClearingAccountLookupDuration(duration time.Duration) {
	clearingAccountLookupDuration.Observe(duration.Seconds())
}

// RecordClearingAccountLookupError records a clearing account lookup error.
func RecordClearingAccountLookupError(clearingType string) {
	clearingAccountLookupErrors.WithLabelValues(clearingType).Inc()
}

// RecordLienExecutionLockContention records a lock contention event when the distributed
// lock for lien execution status update is already held by another process.
func RecordLienExecutionLockContention() {
	lienExecutionLockContentions.Inc()
}

// RecordLienExecutionLockWaitDuration records the time spent waiting to acquire
// the distributed lock for lien execution status updates.
func RecordLienExecutionLockWaitDuration(seconds float64) {
	lienExecutionLockWaitDuration.Observe(seconds)
}

// Bucket evaluation error type constants for bounded cardinality.
const (
	BucketEvalErrNoClient        = "no_client"
	BucketEvalErrNoEvaluator     = "no_evaluator"
	BucketEvalErrInstrumentFetch = "instrument_fetch"
	BucketEvalErrCELEvaluation   = "cel_evaluation"
)

// Bucket evaluation status constants.
const (
	BucketEvalStatusSuccess  = "success"
	BucketEvalStatusSkipped  = "skipped"
	BucketEvalStatusFallback = "fallback"
)

// RecordBucketEvaluationFailure records a bucket evaluation failure.
// errorType should be one of the BucketEvalErr* constants to ensure bounded cardinality.
// Note: instrumentCode is intentionally not included as a metric label to prevent
// cardinality explosion; use structured logging for instrument-specific debugging.
func RecordBucketEvaluationFailure(errorType string) {
	bucketEvaluationFailures.WithLabelValues(errorType).Inc()
}

// RecordBucketEvaluationDuration records the duration of a bucket ID evaluation.
func RecordBucketEvaluationDuration(duration time.Duration) {
	bucketEvaluationDuration.Observe(duration.Seconds())
}

// RecordBucketEvaluation records the outcome of a bucket evaluation attempt.
// status should be one of the BucketEvalStatus* constants.
func RecordBucketEvaluation(status string) {
	bucketEvaluationsTotal.WithLabelValues(status).Inc()
}

// Lien execution retry outcome constants for bounded cardinality.
const (
	LienRetryOutcomeAttempt   = "attempt"
	LienRetryOutcomeSuccess   = "success"
	LienRetryOutcomeFailed    = "failed"
	LienRetryOutcomeExhausted = "exhausted"
)

// RecordLienExecutionRetry records a lien execution retry attempt.
// outcome should be one of the LienRetryOutcome* constants.
func RecordLienExecutionRetry(outcome string) {
	lienExecutionRetries.WithLabelValues(outcome).Inc()
}

// RecordLienExecutionRetriesExhausted records when lien execution retries are exhausted.
// This indicates the payment order may require manual reconciliation.
func RecordLienExecutionRetriesExhausted() {
	lienExecutionRetriesExhausted.Inc()
}
