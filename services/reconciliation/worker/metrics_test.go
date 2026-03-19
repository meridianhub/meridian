package worker

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestNewSchedulerMetricsWithRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSchedulerMetricsWithRegistry(reg)
	assert.NotNil(t, m)
}

func TestSchedulerMetrics_RecordRunTriggered(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSchedulerMetricsWithRegistry(reg)
	// Should not panic
	m.RecordRunTriggered("GBP")
	m.RecordRunTriggered("kWh")
}

func TestSchedulerMetrics_RecordError(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSchedulerMetricsWithRegistry(reg)
	m.RecordError("connection_failed")
}

func TestSchedulerMetrics_ObserveRefreshDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSchedulerMetricsWithRegistry(reg)
	m.ObserveRefreshDuration(0.5)
}

func TestSchedulerMetrics_RecordCatchUp(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSchedulerMetricsWithRegistry(reg)
	m.RecordCatchUp(3)
}
