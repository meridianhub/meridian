package worker

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestNewBillingMetrics(t *testing.T) {
	t.Run("creates metrics without panic", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		m := NewBillingMetricsWithRegistry(reg)
		assert.NotNil(t, m)
	})
}

func TestBillingMetricsRecording(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewBillingMetricsWithRegistry(reg)

	t.Run("records billing run by status", func(t *testing.T) {
		m.RecordBillingRun("INITIATED")
		m.RecordBillingRun("COMPLETED")
		m.RecordBillingRun("FAILED")

		families, err := reg.Gather()
		assert.NoError(t, err)

		var found bool
		for _, f := range families {
			if f.GetName() == "meridian_billing_billing_runs_total" {
				found = true
				assert.Len(t, f.GetMetric(), 3)
			}
		}
		assert.True(t, found, "billing_runs_total metric should be registered")
	})

	t.Run("records invoice creation", func(t *testing.T) {
		m.RecordInvoiceCreated()
		m.RecordInvoiceCreated()

		families, err := reg.Gather()
		assert.NoError(t, err)

		var found bool
		for _, f := range families {
			if f.GetName() == "meridian_billing_billing_invoices_created_total" {
				found = true
				assert.Equal(t, float64(2), f.GetMetric()[0].GetCounter().GetValue())
			}
		}
		assert.True(t, found, "billing_invoices_created_total metric should be registered")
	})

	t.Run("records amount collected", func(t *testing.T) {
		m.RecordAmountCollected(10000)
		m.RecordAmountCollected(5000)

		families, err := reg.Gather()
		assert.NoError(t, err)

		var found bool
		for _, f := range families {
			if f.GetName() == "meridian_billing_billing_amount_collected_cents" {
				found = true
				assert.Equal(t, float64(15000), f.GetMetric()[0].GetCounter().GetValue())
			}
		}
		assert.True(t, found, "billing_amount_collected_cents metric should be registered")
	})

	t.Run("records scheduler errors", func(t *testing.T) {
		m.RecordError("idempotency_check")
		m.RecordError("persist_billing_run")

		families, err := reg.Gather()
		assert.NoError(t, err)

		var found bool
		for _, f := range families {
			if f.GetName() == "meridian_billing_scheduler_errors_total" {
				found = true
				assert.Len(t, f.GetMetric(), 2)
			}
		}
		assert.True(t, found, "scheduler_errors_total metric should be registered")
	})

	t.Run("observes run duration", func(t *testing.T) {
		m.ObserveRunDuration(1.5)
		m.ObserveRunDuration(3.2)

		families, err := reg.Gather()
		assert.NoError(t, err)

		var found bool
		for _, f := range families {
			if f.GetName() == "meridian_billing_run_duration_seconds" {
				found = true
				assert.Equal(t, uint64(2), f.GetMetric()[0].GetHistogram().GetSampleCount())
			}
		}
		assert.True(t, found, "run_duration_seconds metric should be registered")
	})
}
