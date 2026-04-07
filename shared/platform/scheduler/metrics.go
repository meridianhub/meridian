package scheduler

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus metrics for scheduler workers. Metrics are labeled by worker name
// to distinguish between different worker types sharing this lifecycle.
var (
	workerStartsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "worker_starts_total",
		Help:      "Total number of worker starts",
	}, []string{"worker"})

	workerStopsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "worker_stops_total",
		Help:      "Total number of worker stops",
	}, []string{"worker"})

	workerShutdownDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "shutdown_duration_seconds",
		Help:      "Duration of worker shutdown in seconds",
		Buckets:   []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"worker"})

	workerShutdownTimeouts = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "shutdown_timeouts_total",
		Help:      "Total number of worker shutdown timeouts",
	}, []string{"worker"})

	workerInFlightWork = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "in_flight_work",
		Help:      "Current number of in-flight guarded work items",
	}, []string{"worker"})

	workerPollTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "poll_total",
		Help:      "Total number of poll cycles executed",
	}, []string{"worker"})
)

// RecordWorkerStart increments the worker starts counter.
func RecordWorkerStart(worker string) {
	workerStartsTotal.WithLabelValues(worker).Inc()
}

// RecordWorkerStop increments the worker stops counter.
func RecordWorkerStop(worker string) {
	workerStopsTotal.WithLabelValues(worker).Inc()
}

// RecordShutdownDuration observes the shutdown duration for a worker.
func RecordShutdownDuration(worker string, seconds float64) {
	workerShutdownDuration.WithLabelValues(worker).Observe(seconds)
}

// RecordShutdownTimeout increments the shutdown timeout counter.
func RecordShutdownTimeout(worker string) {
	workerShutdownTimeouts.WithLabelValues(worker).Inc()
}

// RecordInFlightWork sets the current in-flight work gauge.
func RecordInFlightWork(worker string, count float64) {
	workerInFlightWork.WithLabelValues(worker).Set(count)
}

// RecordPoll increments the poll cycle counter.
func RecordPoll(worker string) {
	workerPollTotal.WithLabelValues(worker).Inc()
}

// Prometheus metrics for cron schedule execution health.
var (
	cronExecutionDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "cron_execution_duration_seconds",
		Help:      "Duration of cron schedule executions in seconds",
		Buckets:   prometheus.ExponentialBuckets(0.1, 2, 13), // 0.1s to ~409s, covers 5-minute default timeout
	}, []string{"scheduler", "tenant_id", "status"}) // schedule_id omitted: histograms create N series per label combo

	cronExecutionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "cron_executions_total",
		Help:      "Total number of cron schedule executions by status",
	}, []string{"scheduler", "tenant_id", "status"})

	cronLockContentionTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "cron_lock_contention_total",
		Help:      "Number of times a schedule was skipped because the distributed lock was already held",
	}, []string{"scheduler"})

	cronConcurrencyRejectionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "cron_concurrency_rejections_total",
		Help:      "Number of executions skipped due to concurrency limits",
	}, []string{"scheduler", "limit_type"})

	cronLastExecutionTimestamp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "cron_last_execution_timestamp",
		Help:      "Unix timestamp of the most recent completed or failed execution for each schedule",
	}, []string{"scheduler", "schedule_id"})

	cronActiveSchedules = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "cron_active_schedules",
		Help:      "Number of active schedules currently registered in the cron runner",
	}, []string{"scheduler"})
)

// RecordCronExecution records metrics for a completed or failed schedule execution.
func RecordCronExecution(schedulerName, tenantID, scheduleID string, status ExecutionStatus, duration time.Duration) {
	statusStr := string(status)
	cronExecutionDurationSeconds.WithLabelValues(schedulerName, tenantID, statusStr).Observe(duration.Seconds())
	cronExecutionsTotal.WithLabelValues(schedulerName, tenantID, statusStr).Inc()
	cronLastExecutionTimestamp.WithLabelValues(schedulerName, scheduleID).SetToCurrentTime()
}

// RecordCronLockContention increments the counter for schedules skipped due to distributed lock contention.
func RecordCronLockContention(schedulerName string) {
	cronLockContentionTotal.WithLabelValues(schedulerName).Inc()
}

// RecordCronConcurrencyRejection increments the counter for schedules skipped due to concurrency limits.
// limitType should be "global" or "per_tenant".
func RecordCronConcurrencyRejection(schedulerName, limitType string) {
	cronConcurrencyRejectionsTotal.WithLabelValues(schedulerName, limitType).Inc()
}

// UpdateCronActiveSchedules sets the gauge for the number of active schedules registered.
func UpdateCronActiveSchedules(schedulerName string, count float64) {
	cronActiveSchedules.WithLabelValues(schedulerName).Set(count)
}

// DeleteCronScheduleMetrics removes per-schedule Prometheus series for a schedule that
// has been deregistered. This prevents stale series from persisting after schedules are
// removed from the provider.
func DeleteCronScheduleMetrics(schedulerName, scheduleID string) {
	// The histogram is labeled by tenant_id only (not schedule_id), so no per-schedule
	// cleanup is needed there. Only the last-execution timestamp gauge is per-schedule.
	cronLastExecutionTimestamp.DeleteLabelValues(schedulerName, scheduleID)
}
