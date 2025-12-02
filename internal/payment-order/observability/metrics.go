// Package observability provides Prometheus metrics and monitoring for the PaymentOrder service.
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
			Name:    "payment_order_operation_duration_seconds",
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
	rejectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_rejections_total",
			Help: "Total number of rejected payment orders",
		},
		[]string{"currency", "error_code"},
	)

	// Lien execution metrics
	lienExecutionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_lien_executions_total",
			Help: "Total number of lien execution attempts",
		},
		[]string{"status"},
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

	// Idempotency metrics
	idempotentRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_idempotent_requests_total",
			Help: "Total number of idempotent (duplicate) requests handled",
		},
		[]string{"operation"},
	)

	// External service error metrics
	externalServiceErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_order_external_service_errors_total",
			Help: "Total number of external service errors",
		},
		[]string{"service", "operation"},
	)
)

// RecordOperationDuration records the duration of a payment order operation
func RecordOperationDuration(operation, status string, duration time.Duration) {
	operationDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordGatewayCallback records a gateway callback update
func RecordGatewayCallback(gatewayStatus, result string) {
	gatewayCallbacksTotal.WithLabelValues(gatewayStatus, result).Inc()
}

// RecordCompletion records a completed payment order
func RecordCompletion(currency string) {
	completionsTotal.WithLabelValues(currency).Inc()
}

// RecordRejection records a rejected payment order
func RecordRejection(currency, errorCode string) {
	rejectionsTotal.WithLabelValues(currency, errorCode).Inc()
}

// RecordLienExecution records a lien execution attempt
func RecordLienExecution(status string) {
	lienExecutionsTotal.WithLabelValues(status).Inc()
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

// RecordIdempotentRequest records an idempotent (duplicate) request
func RecordIdempotentRequest(operation string) {
	idempotentRequestsTotal.WithLabelValues(operation).Inc()
}

// RecordExternalServiceError records an external service error
func RecordExternalServiceError(service, operation string) {
	externalServiceErrors.WithLabelValues(service, operation).Inc()
}
