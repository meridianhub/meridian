package scheduler_test

import (
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/forecasting/scheduler"
)

func newTestMetrics(t *testing.T) (*scheduler.Metrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	return scheduler.NewMetricsWithRegistry(reg), reg
}

func gatherMetrics(t *testing.T, reg *prometheus.Registry) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	index := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		index[f.GetName()] = f
	}
	return index
}

func counterValue(t *testing.T, f *dto.MetricFamily) float64 {
	t.Helper()
	require.NotNil(t, f)
	require.NotEmpty(t, f.GetMetric())
	return f.GetMetric()[0].GetCounter().GetValue()
}

// --- RecordExecution ---

func TestMetrics_RecordExecution_Success(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.RecordExecution("tenant-1", "strategy-abc", "success")

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_scheduled_executions_total"]
	require.True(t, ok)
	assert.Equal(t, float64(1), counterValue(t, f))
}

func TestMetrics_RecordExecution_Error(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.RecordExecution("tenant-1", "strategy-abc", "error")

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_scheduled_executions_total"]
	require.True(t, ok)
	assert.Equal(t, float64(1), counterValue(t, f))
}

func TestMetrics_RecordExecution_MultipleStatuses(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.RecordExecution("tenant-1", "strategy-1", "success")
	m.RecordExecution("tenant-1", "strategy-1", "success")
	m.RecordExecution("tenant-1", "strategy-1", "error")

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_scheduled_executions_total"]
	require.True(t, ok)

	// Should have two distinct label sets: status=success (2) and status=error (1)
	var total float64
	for _, metric := range f.GetMetric() {
		total += metric.GetCounter().GetValue()
	}
	assert.Equal(t, float64(3), total)
}

func TestMetrics_RecordExecution_MultipleStrategies(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.RecordExecution("tenant-1", "strategy-a", "success")
	m.RecordExecution("tenant-1", "strategy-b", "success")
	m.RecordExecution("tenant-2", "strategy-c", "success")

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_scheduled_executions_total"]
	require.True(t, ok)
	assert.Equal(t, 3, len(f.GetMetric()))
}

// --- ObserveExecutionDuration ---

func TestMetrics_ObserveExecutionDuration_RecordsHistogram(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.ObserveExecutionDuration("tenant-1", "strategy-1", 2.5)

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_execution_duration_seconds"]
	require.True(t, ok)
	require.NotEmpty(t, f.GetMetric())
	assert.Equal(t, uint64(1), f.GetMetric()[0].GetHistogram().GetSampleCount())
	assert.Equal(t, 2.5, f.GetMetric()[0].GetHistogram().GetSampleSum())
}

func TestMetrics_ObserveExecutionDuration_MultipleObservations(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.ObserveExecutionDuration("tenant-1", "strategy-1", 0.1)
	m.ObserveExecutionDuration("tenant-1", "strategy-1", 5.0)
	m.ObserveExecutionDuration("tenant-1", "strategy-1", 30.0)

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_execution_duration_seconds"]
	require.True(t, ok)
	hist := f.GetMetric()[0].GetHistogram()
	assert.Equal(t, uint64(3), hist.GetSampleCount())
	assert.InDelta(t, 35.1, hist.GetSampleSum(), 0.001)
}

// --- RecordLeaseFailure ---

func TestMetrics_RecordLeaseFailure_Contention(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.RecordLeaseFailure("contention")
	m.RecordLeaseFailure("contention")

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_lease_acquisition_failures_total"]
	require.True(t, ok)
	assert.Equal(t, float64(2), counterValue(t, f))
}

func TestMetrics_RecordLeaseFailure_MultipleReasons(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.RecordLeaseFailure("contention")
	m.RecordLeaseFailure("timeout")
	m.RecordLeaseFailure("db_error")

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_lease_acquisition_failures_total"]
	require.True(t, ok)
	assert.Equal(t, 3, len(f.GetMetric()))
}

// --- SetActiveStrategies ---

func TestMetrics_SetActiveStrategies(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.SetActiveStrategies(5)

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_active_strategies"]
	require.True(t, ok)
	require.NotEmpty(t, f.GetMetric())
	assert.Equal(t, float64(5), f.GetMetric()[0].GetGauge().GetValue())
}

func TestMetrics_SetActiveStrategies_UpdatesGauge(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.SetActiveStrategies(10)
	m.SetActiveStrategies(3) // overwrite with lower value

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_active_strategies"]
	require.True(t, ok)
	assert.Equal(t, float64(3), f.GetMetric()[0].GetGauge().GetValue())
}

func TestMetrics_SetActiveStrategies_Zero(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.SetActiveStrategies(0)

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_active_strategies"]
	require.True(t, ok)
	assert.Equal(t, float64(0), f.GetMetric()[0].GetGauge().GetValue())
}

// --- RecordReload ---

func TestMetrics_RecordReload_Success(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.RecordReload("success")
	m.RecordReload("success")
	m.RecordReload("success")

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_scheduler_reloads_total"]
	require.True(t, ok)
	assert.Equal(t, float64(3), counterValue(t, f))
}

func TestMetrics_RecordReload_MultipleOutcomes(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.RecordReload("success")
	m.RecordReload("error")

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_scheduler_reloads_total"]
	require.True(t, ok)
	assert.Equal(t, 2, len(f.GetMetric()))
}

// --- RecordError ---

func TestMetrics_RecordError_SingleType(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.RecordError("context_cancelled")

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_scheduler_errors_total"]
	require.True(t, ok)
	assert.Equal(t, float64(1), counterValue(t, f))
}

func TestMetrics_RecordError_MultipleTypes(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.RecordError("context_cancelled")
	m.RecordError("db_error")
	m.RecordError("starlark_panic")

	families := gatherMetrics(t, reg)
	f, ok := families["meridian_forecasting_scheduler_errors_total"]
	require.True(t, ok)
	assert.Equal(t, 3, len(f.GetMetric()))
}
