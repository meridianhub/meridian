package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordOperationDuration(t *testing.T) {
	// Reset metrics before test
	operationDuration.Reset()

	// Record a metric
	RecordOperationDuration("test_operation", "success", 100*time.Millisecond)

	// Verify metric was recorded
	count := testutil.CollectAndCount(operationDuration)
	if count == 0 {
		t.Error("Expected operation duration metric to be recorded")
	}
}

func TestRecordDeposit(t *testing.T) {
	// Reset metrics before test
	depositsTotal.Reset()

	// Record a metric
	RecordDeposit("ACC-12345", "GBP")

	// Verify metric was recorded
	count := testutil.CollectAndCount(depositsTotal)
	if count == 0 {
		t.Error("Expected deposit metric to be recorded")
	}
}

func TestRecordBalance(t *testing.T) {
	// Reset metrics before test
	balanceGauge.Reset()

	// Record a metric
	RecordBalance("ACC-12345", 10000, "GBP")

	// Verify metric was recorded
	count := testutil.CollectAndCount(balanceGauge)
	if count == 0 {
		t.Error("Expected balance metric to be recorded")
	}
}

func TestRecordSagaFailure(t *testing.T) {
	// Reset metrics before test
	sagaFailuresTotal.Reset()

	// Record a metric
	RecordSagaFailure("deposit", "log_position")

	// Verify metric was recorded
	count := testutil.CollectAndCount(sagaFailuresTotal)
	if count == 0 {
		t.Error("Expected saga failure metric to be recorded")
	}
}

func TestRecordSagaCompensation(t *testing.T) {
	// Reset metrics before test
	sagaCompensationsTotal.Reset()

	// Record a metric
	RecordSagaCompensation("deposit", "log_position")

	// Verify metric was recorded
	count := testutil.CollectAndCount(sagaCompensationsTotal)
	if count == 0 {
		t.Error("Expected saga compensation metric to be recorded")
	}
}

func TestRecordSagaDuration(t *testing.T) {
	// Reset metrics before test
	sagaDuration.Reset()

	// Record a metric
	RecordSagaDuration("deposit", "success", 500*time.Millisecond)

	// Verify metric was recorded
	count := testutil.CollectAndCount(sagaDuration)
	if count == 0 {
		t.Error("Expected saga duration metric to be recorded")
	}
}

func TestRecordExternalServiceError(t *testing.T) {
	// Reset metrics before test
	externalServiceErrors.Reset()

	// Record a metric
	RecordExternalServiceError("position_keeping", "update_log")

	// Verify metric was recorded
	count := testutil.CollectAndCount(externalServiceErrors)
	if count == 0 {
		t.Error("Expected external service error metric to be recorded")
	}
}

func TestMetricsLabels(t *testing.T) {
	tests := []struct {
		name       string
		metricFunc func()
		metric     prometheus.Collector
	}{
		{
			name: "operation_duration_labels",
			metricFunc: func() {
				RecordOperationDuration("test_op", "success", 100*time.Millisecond)
			},
			metric: operationDuration,
		},
		{
			name: "deposit_labels",
			metricFunc: func() {
				RecordDeposit("ACC-123", "USD")
			},
			metric: depositsTotal,
		},
		{
			name: "saga_failure_labels",
			metricFunc: func() {
				RecordSagaFailure("withdraw", "save_account")
			},
			metric: sagaFailuresTotal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset and record
			if resettable, ok := tt.metric.(interface{ Reset() }); ok {
				resettable.Reset()
			}
			tt.metricFunc()

			// Verify
			count := testutil.CollectAndCount(tt.metric)
			if count == 0 {
				t.Errorf("%s: expected metric to be recorded", tt.name)
			}
		})
	}
}
