package scheduler

import (
	"testing"

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
	// Should not panic
	RecordShutdownDuration("test-worker-shutdown", 1.5)
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
