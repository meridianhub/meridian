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
)

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
