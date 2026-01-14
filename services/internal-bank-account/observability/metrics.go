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
