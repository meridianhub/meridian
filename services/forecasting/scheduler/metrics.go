// Package scheduler provides cron-based scheduling for forecasting strategy execution.
package scheduler

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics provides Prometheus metrics for the forecasting scheduler.
type Metrics struct {
	executionsTotal       *prometheus.CounterVec
	executionDuration     *prometheus.HistogramVec
	leaseFailuresTotal    *prometheus.CounterVec
	activeStrategiesGauge prometheus.Gauge
	reloadTotal           *prometheus.CounterVec
	schedulerErrorsTotal  *prometheus.CounterVec
}

// NewMetrics creates a new metrics collector with default registrations.
func NewMetrics() *Metrics {
	return newMetricsWithFactory(promauto.With(prometheus.DefaultRegisterer))
}

// NewMetricsWithRegistry creates metrics registered with a custom registry.
func NewMetricsWithRegistry(reg prometheus.Registerer) *Metrics {
	return newMetricsWithFactory(promauto.With(reg))
}

func newMetricsWithFactory(factory promauto.Factory) *Metrics {
	return &Metrics{
		executionsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "forecasting",
				Name:      "scheduled_executions_total",
				Help:      "Total number of scheduled forecast executions by tenant, strategy, and status.",
			},
			[]string{"tenant_id", "strategy_id", "status"},
		),
		executionDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "meridian",
				Subsystem: "forecasting",
				Name:      "execution_duration_seconds",
				Help:      "Duration of scheduled forecast execution in seconds.",
				Buckets:   []float64{.1, .5, 1, 5, 10, 30, 60, 120, 300},
			},
			[]string{"tenant_id", "strategy_id"},
		),
		leaseFailuresTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "forecasting",
				Name:      "lease_acquisition_failures_total",
				Help:      "Total number of lease acquisition failures by reason.",
			},
			[]string{"reason"},
		),
		activeStrategiesGauge: factory.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "meridian",
				Subsystem: "forecasting",
				Name:      "active_strategies",
				Help:      "Number of active strategies currently registered in the scheduler.",
			},
		),
		reloadTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "forecasting",
				Name:      "scheduler_reloads_total",
				Help:      "Total number of strategy reload cycles by outcome.",
			},
			[]string{"outcome"},
		),
		schedulerErrorsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "forecasting",
				Name:      "scheduler_errors_total",
				Help:      "Total number of scheduler errors by type.",
			},
			[]string{"error_type"},
		),
	}
}

// RecordExecution records a forecast execution result.
func (m *Metrics) RecordExecution(tenantID, strategyID, status string) {
	m.executionsTotal.WithLabelValues(tenantID, strategyID, status).Inc()
}

// ObserveExecutionDuration records the duration of a forecast execution.
func (m *Metrics) ObserveExecutionDuration(tenantID, strategyID string, seconds float64) {
	m.executionDuration.WithLabelValues(tenantID, strategyID).Observe(seconds)
}

// RecordLeaseFailure records a lease acquisition failure.
func (m *Metrics) RecordLeaseFailure(reason string) {
	m.leaseFailuresTotal.WithLabelValues(reason).Inc()
}

// SetActiveStrategies sets the gauge for active strategies.
func (m *Metrics) SetActiveStrategies(count float64) {
	m.activeStrategiesGauge.Set(count)
}

// RecordReload records a strategy reload outcome.
func (m *Metrics) RecordReload(outcome string) {
	m.reloadTotal.WithLabelValues(outcome).Inc()
}

// RecordError records a scheduler error.
func (m *Metrics) RecordError(errorType string) {
	m.schedulerErrorsTotal.WithLabelValues(errorType).Inc()
}
