// Package service provides Prometheus metrics for CurrentAccount service monitoring
package service

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Business Metrics - Track customer-facing operations and account states

var (
	// depositsTotal counts the total number of deposits processed
	depositsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_deposits_total",
			Help: "Total number of deposits processed",
		},
		[]string{"account_id", "currency"},
	)

	// withdrawalsTotal counts the total number of withdrawals processed
	withdrawalsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_withdrawals_total",
			Help: "Total number of withdrawals processed",
		},
		[]string{"account_id", "currency"},
	)

	// balanceCents tracks the current balance of accounts in cents
	balanceCents = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "current_account_balance_cents",
			Help: "Current balance of account in cents",
		},
		[]string{"account_id", "currency"},
	)

	// overdraftUsageCents tracks the current overdraft usage in cents
	overdraftUsageCents = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "current_account_overdraft_usage_cents",
			Help: "Current overdraft usage in cents",
		},
		[]string{"account_id"},
	)

	// transactionAmountCents tracks the distribution of transaction amounts
	transactionAmountCents = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "current_account_transaction_amount_cents",
			Help: "Distribution of transaction amounts in cents",
			Buckets: []float64{
				100,      // £1 or $1
				1000,     // £10 or $10
				5000,     // £50 or $50
				10000,    // £100 or $100
				50000,    // £500 or $500
				100000,   // £1,000 or $1,000
				500000,   // £5,000 or $5,000
				1000000,  // £10,000 or $10,000
				5000000,  // £50,000 or $50,000
				10000000, // £100,000 or $100,000
			},
		},
		[]string{"operation_type", "currency"},
	)
)

// Technical Metrics - Track service health and performance

var (
	// operationDurationSeconds tracks the duration of service operations
	operationDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "current_account_operation_duration_seconds",
			Help: "Duration of service operations in seconds",
			Buckets: []float64{
				0.001, // 1ms
				0.005, // 5ms
				0.01,  // 10ms
				0.025, // 25ms
				0.05,  // 50ms
				0.1,   // 100ms
				0.25,  // 250ms
				0.5,   // 500ms
				1.0,   // 1s
				2.5,   // 2.5s
				5.0,   // 5s
			},
		},
		[]string{"operation", "status"},
	)

	// sagaFailuresTotal counts saga failures by failed step
	sagaFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_saga_failures_total",
			Help: "Total number of saga failures by step",
		},
		[]string{"failed_step"},
	)

	// sagaCompensationsTotal counts saga compensations by step
	sagaCompensationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_saga_compensations_total",
			Help: "Total number of saga compensations by step",
		},
		[]string{"step"},
	)

	// dbErrorsTotal counts database operation errors
	dbErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_db_errors_total",
			Help: "Total number of database errors",
		},
		[]string{"operation"},
	)

	// externalServiceErrorsTotal counts external service call errors
	externalServiceErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_external_service_errors_total",
			Help: "Total number of external service call errors",
		},
		[]string{"service", "operation"},
	)
)

// Business Metrics Helper Functions

// RecordDeposit records a successful deposit operation
func RecordDeposit(accountID, currency string, amountCents int64) {
	depositsTotal.WithLabelValues(accountID, currency).Inc()
	transactionAmountCents.WithLabelValues("deposit", currency).Observe(float64(amountCents))
}

// RecordWithdrawal records a successful withdrawal operation
func RecordWithdrawal(accountID, currency string, amountCents int64) {
	withdrawalsTotal.WithLabelValues(accountID, currency).Inc()
	transactionAmountCents.WithLabelValues("withdrawal", currency).Observe(float64(amountCents))
}

// UpdateBalance updates the current balance gauge for an account
func UpdateBalance(accountID, currency string, balance int64) {
	balanceCents.WithLabelValues(accountID, currency).Set(float64(balance))
}

// UpdateOverdraftUsage updates the overdraft usage gauge for an account
func UpdateOverdraftUsage(accountID string, usageCents int64) {
	overdraftUsageCents.WithLabelValues(accountID).Set(float64(usageCents))
}

// Technical Metrics Helper Functions

// RecordOperationDuration records the duration of a service operation
func RecordOperationDuration(operation, status string, durationSeconds float64) {
	operationDurationSeconds.WithLabelValues(operation, status).Observe(durationSeconds)
}

// RecordSagaFailure records a saga failure
func RecordSagaFailure(failedStep string) {
	sagaFailuresTotal.WithLabelValues(failedStep).Inc()
}

// RecordSagaCompensation records a saga compensation
func RecordSagaCompensation(step string) {
	sagaCompensationsTotal.WithLabelValues(step).Inc()
}

// RecordDatabaseError records a database operation error
func RecordDatabaseError(operation string) {
	dbErrorsTotal.WithLabelValues(operation).Inc()
}

// RecordExternalServiceError records an external service call error
func RecordExternalServiceError(service, operation string) {
	externalServiceErrorsTotal.WithLabelValues(service, operation).Inc()
}
