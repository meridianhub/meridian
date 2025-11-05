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
		[]string{"account_id", "currency"},
	)

	withdrawalsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_withdrawals_total",
			Help: "Total number of withdrawal transactions",
		},
		[]string{"account_id", "currency"},
	)

	balanceGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "current_account_balance_cents",
			Help: "Current account balance in cents",
		},
		[]string{"account_id", "currency"},
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
)

// RecordOperationDuration records the duration of a current account operation
func RecordOperationDuration(operation, status string, duration time.Duration) {
	operationDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordDeposit records a deposit transaction
func RecordDeposit(accountID, currency string) {
	depositsTotal.WithLabelValues(accountID, currency).Inc()
}

// RecordWithdrawal records a withdrawal transaction
func RecordWithdrawal(accountID, currency string) {
	withdrawalsTotal.WithLabelValues(accountID, currency).Inc()
}

// RecordBalance records the current account balance
func RecordBalance(accountID string, balanceCents int64, currency string) {
	balanceGauge.WithLabelValues(accountID, currency).Set(float64(balanceCents))
}

// RecordSagaFailure records a saga failure
func RecordSagaFailure(operation, failedStep string) {
	sagaFailuresTotal.WithLabelValues(operation, failedStep).Inc()
}

// RecordSagaCompensation records a saga compensation
func RecordSagaCompensation(operation, step string) {
	sagaCompensationsTotal.WithLabelValues(operation, step).Inc()
}

// RecordSagaDuration records the duration of a saga execution
func RecordSagaDuration(operation, status string, duration time.Duration) {
	sagaDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordExternalServiceError records an external service error
func RecordExternalServiceError(service, operation string) {
	externalServiceErrors.WithLabelValues(service, operation).Inc()
}
