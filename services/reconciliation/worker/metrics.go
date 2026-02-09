package worker

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// SchedulerMetrics provides Prometheus metrics for the settlement scheduler.
type SchedulerMetrics struct {
	runsTriggered   *prometheus.CounterVec
	errorsTotal     *prometheus.CounterVec
	refreshDuration prometheus.Histogram
}

// NewSchedulerMetrics creates a new metrics collector with default registrations.
func NewSchedulerMetrics() *SchedulerMetrics {
	return &SchedulerMetrics{
		runsTriggered: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "reconciliation",
				Name:      "scheduled_runs_triggered_total",
				Help:      "Total number of reconciliation runs triggered by the scheduler.",
			},
			[]string{"asset_type"},
		),
		errorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "reconciliation",
				Name:      "scheduler_errors_total",
				Help:      "Total number of scheduler errors by error type.",
			},
			[]string{"error_type"},
		),
		refreshDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "meridian",
				Subsystem: "reconciliation",
				Name:      "schedule_refresh_duration_seconds",
				Help:      "Duration of schedule refresh operations from Reference Data.",
				Buckets:   prometheus.DefBuckets,
			},
		),
	}
}

// NewSchedulerMetricsWithRegistry creates metrics registered with a custom registry.
// Useful for testing to avoid duplicate metric registration panics.
func NewSchedulerMetricsWithRegistry(reg prometheus.Registerer) *SchedulerMetrics {
	factory := promauto.With(reg)
	return &SchedulerMetrics{
		runsTriggered: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "reconciliation",
				Name:      "scheduled_runs_triggered_total",
				Help:      "Total number of reconciliation runs triggered by the scheduler.",
			},
			[]string{"asset_type"},
		),
		errorsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "reconciliation",
				Name:      "scheduler_errors_total",
				Help:      "Total number of scheduler errors by error type.",
			},
			[]string{"error_type"},
		),
		refreshDuration: factory.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "meridian",
				Subsystem: "reconciliation",
				Name:      "schedule_refresh_duration_seconds",
				Help:      "Duration of schedule refresh operations from Reference Data.",
				Buckets:   prometheus.DefBuckets,
			},
		),
	}
}

// RecordRunTriggered increments the runs triggered counter for the given asset type.
func (m *SchedulerMetrics) RecordRunTriggered(assetType string) {
	m.runsTriggered.WithLabelValues(assetType).Inc()
}

// RecordError increments the error counter for the given error type.
func (m *SchedulerMetrics) RecordError(errorType string) {
	m.errorsTotal.WithLabelValues(errorType).Inc()
}

// ObserveRefreshDuration records the duration of a schedule refresh operation.
func (m *SchedulerMetrics) ObserveRefreshDuration(seconds float64) {
	m.refreshDuration.Observe(seconds)
}
