// Package worker provides background workers for the payment-order service.
package worker

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// BillingMetrics provides Prometheus metrics for the billing scheduler.
type BillingMetrics struct {
	billingRunsTotal       *prometheus.CounterVec
	billingInvoicesCreated prometheus.Counter
	billingAmountCollected prometheus.Counter
	billingSchedulerErrors *prometheus.CounterVec
	billingRunDuration     prometheus.Histogram
}

// NewBillingMetrics creates a new metrics collector with default registrations.
func NewBillingMetrics() *BillingMetrics {
	return &BillingMetrics{
		billingRunsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "billing",
				Name:      "billing_runs_total",
				Help:      "Total number of billing runs by status.",
			},
			[]string{"status"},
		),
		billingInvoicesCreated: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "billing",
				Name:      "billing_invoices_created_total",
				Help:      "Total number of invoices created by billing runs.",
			},
		),
		billingAmountCollected: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "billing",
				Name:      "billing_amount_collected_cents",
				Help:      "Total amount collected in cents across all billing runs.",
			},
		),
		billingSchedulerErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "billing",
				Name:      "scheduler_errors_total",
				Help:      "Total number of billing scheduler errors by type.",
			},
			[]string{"error_type"},
		),
		billingRunDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "meridian",
				Subsystem: "billing",
				Name:      "run_duration_seconds",
				Help:      "Duration of billing run execution in seconds.",
				Buckets:   []float64{.1, .5, 1, 5, 10, 30, 60, 120, 300},
			},
		),
	}
}

// NewBillingMetricsWithRegistry creates metrics registered with a custom registry.
func NewBillingMetricsWithRegistry(reg prometheus.Registerer) *BillingMetrics {
	factory := promauto.With(reg)
	return &BillingMetrics{
		billingRunsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "billing",
				Name:      "billing_runs_total",
				Help:      "Total number of billing runs by status.",
			},
			[]string{"status"},
		),
		billingInvoicesCreated: factory.NewCounter(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "billing",
				Name:      "billing_invoices_created_total",
				Help:      "Total number of invoices created by billing runs.",
			},
		),
		billingAmountCollected: factory.NewCounter(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "billing",
				Name:      "billing_amount_collected_cents",
				Help:      "Total amount collected in cents across all billing runs.",
			},
		),
		billingSchedulerErrors: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "billing",
				Name:      "scheduler_errors_total",
				Help:      "Total number of billing scheduler errors by type.",
			},
			[]string{"error_type"},
		),
		billingRunDuration: factory.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "meridian",
				Subsystem: "billing",
				Name:      "run_duration_seconds",
				Help:      "Duration of billing run execution in seconds.",
				Buckets:   []float64{.1, .5, 1, 5, 10, 30, 60, 120, 300},
			},
		),
	}
}

// RecordBillingRun records a billing run by status.
func (m *BillingMetrics) RecordBillingRun(status string) {
	m.billingRunsTotal.WithLabelValues(status).Inc()
}

// RecordInvoiceCreated increments the invoice creation counter.
func (m *BillingMetrics) RecordInvoiceCreated() {
	m.billingInvoicesCreated.Inc()
}

// RecordAmountCollected adds the amount to the collected counter.
func (m *BillingMetrics) RecordAmountCollected(cents int64) {
	m.billingAmountCollected.Add(float64(cents))
}

// RecordError increments the error counter for the given type.
func (m *BillingMetrics) RecordError(errorType string) {
	m.billingSchedulerErrors.WithLabelValues(errorType).Inc()
}

// ObserveRunDuration records the duration of a billing run.
func (m *BillingMetrics) ObserveRunDuration(seconds float64) {
	m.billingRunDuration.Observe(seconds)
}
