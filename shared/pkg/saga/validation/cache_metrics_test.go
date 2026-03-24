package validation

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	err := c.(prometheus.Metric).Write(m)
	require.NoError(t, err)
	return m.GetCounter().GetValue()
}

func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	err := g.(prometheus.Metric).Write(m)
	require.NoError(t, err)
	return m.GetGauge().GetValue()
}

func counterVecValue(t *testing.T, cv *prometheus.CounterVec, label string) float64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	err := cv.WithLabelValues(label).(prometheus.Metric).Write(m)
	require.NoError(t, err)
	return m.GetCounter().GetValue()
}

func TestRecordCacheHit(t *testing.T) {
	before := counterValue(t, ExposeValidationCacheMetricsForTesting.HitsTotal)
	RecordCacheHit()
	after := counterValue(t, ExposeValidationCacheMetricsForTesting.HitsTotal)
	assert.Equal(t, before+1, after)
}

func TestRecordCacheMiss(t *testing.T) {
	before := counterValue(t, ExposeValidationCacheMetricsForTesting.MissesTotal)
	RecordCacheMiss()
	after := counterValue(t, ExposeValidationCacheMetricsForTesting.MissesTotal)
	assert.Equal(t, before+1, after)
}

func TestRecordCacheSize(t *testing.T) {
	RecordCacheSize(42)
	val := gaugeValue(t, ExposeValidationCacheMetricsForTesting.Size)
	assert.Equal(t, 42.0, val)

	RecordCacheSize(0)
	val = gaugeValue(t, ExposeValidationCacheMetricsForTesting.Size)
	assert.Equal(t, 0.0, val)
}

func TestRecordCacheEviction_ttl(t *testing.T) {
	before := counterVecValue(t, ExposeValidationCacheMetricsForTesting.EvictionsTotal, "ttl")
	RecordCacheEviction("ttl")
	after := counterVecValue(t, ExposeValidationCacheMetricsForTesting.EvictionsTotal, "ttl")
	assert.Equal(t, before+1, after)
}

func TestRecordCacheEviction_lru(t *testing.T) {
	before := counterVecValue(t, ExposeValidationCacheMetricsForTesting.EvictionsTotal, "lru")
	RecordCacheEviction("lru")
	after := counterVecValue(t, ExposeValidationCacheMetricsForTesting.EvictionsTotal, "lru")
	assert.Equal(t, before+1, after)
}

func TestRecordCacheHit_multiple_increments(t *testing.T) {
	before := counterValue(t, ExposeValidationCacheMetricsForTesting.HitsTotal)
	RecordCacheHit()
	RecordCacheHit()
	RecordCacheHit()
	after := counterValue(t, ExposeValidationCacheMetricsForTesting.HitsTotal)
	assert.Equal(t, before+3, after)
}

func TestCacheMetrics_exposed_for_testing(t *testing.T) {
	// Verify the test exposure struct has all fields populated
	assert.NotNil(t, ExposeValidationCacheMetricsForTesting.HitsTotal)
	assert.NotNil(t, ExposeValidationCacheMetricsForTesting.MissesTotal)
	assert.NotNil(t, ExposeValidationCacheMetricsForTesting.Size)
	assert.NotNil(t, ExposeValidationCacheMetricsForTesting.EvictionsTotal)
}
