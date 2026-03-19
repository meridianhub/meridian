package worker

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSchedulerMetricsWithRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSchedulerMetricsWithRegistry(reg)
	assert.NotNil(t, m)
}

func TestSchedulerMetrics_RecordRunTriggered(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSchedulerMetricsWithRegistry(reg)
	m.RecordRunTriggered("GBP")
	m.RecordRunTriggered("kWh")

	families, err := reg.Gather()
	require.NoError(t, err)
	assert.NotEmpty(t, families)
}

func TestSchedulerMetrics_RecordError(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSchedulerMetricsWithRegistry(reg)
	m.RecordError("connection_failed")

	families, err := reg.Gather()
	require.NoError(t, err)
	assert.NotEmpty(t, families)
}

func TestSchedulerMetrics_ObserveRefreshDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSchedulerMetricsWithRegistry(reg)
	m.ObserveRefreshDuration(0.5)

	families, err := reg.Gather()
	require.NoError(t, err)
	assert.NotEmpty(t, families)
}

func TestSchedulerMetrics_RecordCatchUp(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSchedulerMetricsWithRegistry(reg)
	m.RecordCatchUp(3)
}
