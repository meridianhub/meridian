package scheduler

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getCounterValue(t *testing.T, vec *prometheus.CounterVec, label string) float64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	err := vec.WithLabelValues(label).(prometheus.Metric).Write(m)
	require.NoError(t, err)
	return m.GetCounter().GetValue()
}

func getGaugeValue(t *testing.T, vec *prometheus.GaugeVec, label string) float64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	err := vec.WithLabelValues(label).(prometheus.Metric).Write(m)
	require.NoError(t, err)
	return m.GetGauge().GetValue()
}

func getCounterValueMulti(t *testing.T, vec *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	err := vec.WithLabelValues(labels...).(prometheus.Metric).Write(m)
	require.NoError(t, err)
	return m.GetCounter().GetValue()
}

func getGaugeValueMulti(t *testing.T, vec *prometheus.GaugeVec, labels ...string) float64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	err := vec.WithLabelValues(labels...).(prometheus.Metric).Write(m)
	require.NoError(t, err)
	return m.GetGauge().GetValue()
}

func getHistogramCount(t *testing.T, vec *prometheus.HistogramVec, labels ...string) uint64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	err := vec.WithLabelValues(labels...).(prometheus.Metric).Write(m)
	require.NoError(t, err)
	return m.GetHistogram().GetSampleCount()
}

func getHistogramSum(t *testing.T, vec *prometheus.HistogramVec, labels ...string) float64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	err := vec.WithLabelValues(labels...).(prometheus.Metric).Write(m)
	require.NoError(t, err)
	return m.GetHistogram().GetSampleSum()
}

func TestRecordWorkerStart(t *testing.T) {
	before := getCounterValue(t, workerStartsTotal, "test-worker-start")
	RecordWorkerStart("test-worker-start")
	after := getCounterValue(t, workerStartsTotal, "test-worker-start")
	assert.Equal(t, before+1, after)
}

func TestRecordWorkerStop(t *testing.T) {
	before := getCounterValue(t, workerStopsTotal, "test-worker-stop")
	RecordWorkerStop("test-worker-stop")
	after := getCounterValue(t, workerStopsTotal, "test-worker-stop")
	assert.Equal(t, before+1, after)
}

func TestRecordShutdownDuration(t *testing.T) {
	// Histogram observation should not panic and should register
	assert.NotPanics(t, func() {
		RecordShutdownDuration("test-worker-shutdown", 1.5)
	})
}

func TestRecordShutdownTimeout(t *testing.T) {
	before := getCounterValue(t, workerShutdownTimeouts, "test-worker-timeout")
	RecordShutdownTimeout("test-worker-timeout")
	after := getCounterValue(t, workerShutdownTimeouts, "test-worker-timeout")
	assert.Equal(t, before+1, after)
}

func TestRecordInFlightWork(t *testing.T) {
	RecordInFlightWork("test-worker-inflight", 5)
	val := getGaugeValue(t, workerInFlightWork, "test-worker-inflight")
	assert.Equal(t, 5.0, val)

	RecordInFlightWork("test-worker-inflight", 0)
	val = getGaugeValue(t, workerInFlightWork, "test-worker-inflight")
	assert.Equal(t, 0.0, val)
}

func TestRecordPoll(t *testing.T) {
	before := getCounterValue(t, workerPollTotal, "test-worker-poll")
	RecordPoll("test-worker-poll")
	after := getCounterValue(t, workerPollTotal, "test-worker-poll")
	assert.Equal(t, before+1, after)
}

func TestMetrics_different_workers_are_independent(t *testing.T) {
	RecordWorkerStart("worker-a")
	RecordWorkerStart("worker-a")
	RecordWorkerStart("worker-b")

	aVal := getCounterValue(t, workerStartsTotal, "worker-a")
	bVal := getCounterValue(t, workerStartsTotal, "worker-b")

	assert.Greater(t, aVal, bVal)
}

// --- Cron execution metrics ---

func TestRecordCronExecution_completed(t *testing.T) {
	sched := "test-cron-completed"

	// Histogram and counter are labeled by scheduler/status only (tenant_id and schedule_id
	// omitted to keep cardinality bounded regardless of tenant/schedule count).
	beforeHistogram := getHistogramCount(t, cronExecutionDurationSeconds, sched, "COMPLETED")
	beforeCounter := getCounterValueMulti(t, cronExecutionsTotal, sched, "COMPLETED")

	RecordCronExecution(sched, ExecutionStatusCompleted, 500*time.Millisecond)

	assert.Equal(t, beforeHistogram+1, getHistogramCount(t, cronExecutionDurationSeconds, sched, "COMPLETED"))
	assert.Equal(t, beforeCounter+1, getCounterValueMulti(t, cronExecutionsTotal, sched, "COMPLETED"))
}

func TestRecordCronLastExecutionTimestamp(t *testing.T) {
	sched := "test-cron-ts"
	sid := "sched-ts"

	RecordCronLastExecutionTimestamp(sched, sid)
	assert.Greater(t, getGaugeValueMulti(t, cronLastExecutionTimestamp, sched, sid), 0.0)
}

func TestRecordCronExecution_failed(t *testing.T) {
	sched := "test-cron-failed"

	beforeHistogram := getHistogramCount(t, cronExecutionDurationSeconds, sched, "FAILED")
	beforeCounter := getCounterValueMulti(t, cronExecutionsTotal, sched, "FAILED")

	RecordCronExecution(sched, ExecutionStatusFailed, 100*time.Millisecond)

	assert.Equal(t, beforeHistogram+1, getHistogramCount(t, cronExecutionDurationSeconds, sched, "FAILED"))
	assert.Equal(t, beforeCounter+1, getCounterValueMulti(t, cronExecutionsTotal, sched, "FAILED"))
}

func TestRecordCronExecution_duration_is_observed(t *testing.T) {
	sched := "test-cron-duration"

	RecordCronExecution(sched, ExecutionStatusCompleted, 2*time.Second)

	assert.GreaterOrEqual(t, getHistogramSum(t, cronExecutionDurationSeconds, sched, "COMPLETED"), 2.0)
}

func TestRecordCronLockContention(t *testing.T) {
	sched := "test-cron-lock"

	before := getCounterValueMulti(t, cronLockContentionTotal, sched)
	RecordCronLockContention(sched)
	assert.Equal(t, before+1, getCounterValueMulti(t, cronLockContentionTotal, sched))
}

func TestRecordCronConcurrencyRejection_global(t *testing.T) {
	sched := "test-cron-conc-global"

	before := getCounterValueMulti(t, cronConcurrencyRejectionsTotal, sched, "global")
	RecordCronConcurrencyRejection(sched, "global")
	assert.Equal(t, before+1, getCounterValueMulti(t, cronConcurrencyRejectionsTotal, sched, "global"))
}

func TestRecordCronConcurrencyRejection_per_tenant(t *testing.T) {
	sched := "test-cron-conc-tenant"

	before := getCounterValueMulti(t, cronConcurrencyRejectionsTotal, sched, "per_tenant")
	RecordCronConcurrencyRejection(sched, "per_tenant")
	assert.Equal(t, before+1, getCounterValueMulti(t, cronConcurrencyRejectionsTotal, sched, "per_tenant"))
}

func TestUpdateCronActiveSchedules(t *testing.T) {
	sched := "test-cron-active"

	UpdateCronActiveSchedules(sched, 5)
	assert.Equal(t, 5.0, getGaugeValueMulti(t, cronActiveSchedules, sched))

	UpdateCronActiveSchedules(sched, 0)
	assert.Equal(t, 0.0, getGaugeValueMulti(t, cronActiveSchedules, sched))
}

func TestDeleteCronScheduleMetrics_removes_series(t *testing.T) {
	sched := "test-cron-delete"
	sid := "sched-del"

	// Record a timestamp so the per-schedule series exists
	RecordCronLastExecutionTimestamp(sched, sid)
	assert.Greater(t, getGaugeValueMulti(t, cronLastExecutionTimestamp, sched, sid), 0.0)

	// Delete should not panic and removes the per-schedule timestamp series
	assert.NotPanics(t, func() {
		DeleteCronScheduleMetrics(sched, sid)
	})

	// After deletion, the timestamp series resets to zero (re-created fresh)
	assert.Equal(t, 0.0, getGaugeValueMulti(t, cronLastExecutionTimestamp, sched, sid))
}

func TestCronMetrics_independent_schedulers(t *testing.T) {
	RecordCronLockContention("sched-x")
	RecordCronLockContention("sched-x")
	RecordCronLockContention("sched-y")

	xVal := getCounterValueMulti(t, cronLockContentionTotal, "sched-x")
	yVal := getCounterValueMulti(t, cronLockContentionTotal, "sched-y")
	assert.Greater(t, xVal, yVal)
}
