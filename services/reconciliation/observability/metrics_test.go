package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestRecordRunDuration(_ *testing.T) {
	// Recording a run duration should not panic
	RecordRunDuration(RunTypeScheduled, "GBP", 30*time.Second)
	RecordRunDuration(RunTypeManual, "kWh", 120*time.Second)
}

func TestRecordSnapshotsCreated(t *testing.T) {
	initial := testutil.ToFloat64(SnapshotsCreatedTotal)
	RecordSnapshotsCreated(42)
	newCount := testutil.ToFloat64(SnapshotsCreatedTotal)
	assert.Equal(t, initial+42, newCount, "snapshot counter should increment by 42")

	// Negative count should be ignored (not panic)
	before := testutil.ToFloat64(SnapshotsCreatedTotal)
	RecordSnapshotsCreated(-1)
	after := testutil.ToFloat64(SnapshotsCreatedTotal)
	assert.Equal(t, before, after, "negative count should not change counter")

	// Zero count should be ignored
	RecordSnapshotsCreated(0)
	afterZero := testutil.ToFloat64(SnapshotsCreatedTotal)
	assert.Equal(t, before, afterZero, "zero count should not change counter")
}

func TestRecordVarianceDetected(t *testing.T) {
	initial := testutil.ToFloat64(VariancesDetectedTotal.WithLabelValues(ReasonAmountMismatch))
	RecordVarianceDetected(ReasonAmountMismatch)
	newCount := testutil.ToFloat64(VariancesDetectedTotal.WithLabelValues(ReasonAmountMismatch))
	assert.Equal(t, initial+1, newCount, "variance counter should increment by 1")
}

func TestSetVarianceValue(t *testing.T) {
	SetVarianceValue(12345.67)
	value := testutil.ToFloat64(VarianceValueGauge)
	assert.Equal(t, 12345.67, value, "variance value gauge should be set")
}

func TestSetDisputesPending(t *testing.T) {
	SetDisputesPending(7)
	value := testutil.ToFloat64(DisputesPendingGauge)
	assert.Equal(t, float64(7), value, "disputes pending gauge should be 7")
}

func TestSetRunStatus(t *testing.T) {
	tests := []struct {
		status   string
		expected float64
	}{
		{RunStatusPending, 0},
		{RunStatusRunning, 1},
		{RunStatusCompleted, 2},
		{RunStatusFailed, 3},
		{RunStatusFinalized, 4},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			SetRunStatus(tt.status)
			value := testutil.ToFloat64(RunStatusGauge)
			assert.Equal(t, tt.expected, value)
		})
	}
}

func TestRecordKafkaPublish(t *testing.T) {
	initial := testutil.ToFloat64(KafkaPublishTotal.WithLabelValues("test.topic", "success"))
	RecordKafkaPublish("test.topic", "success", 5*time.Millisecond)
	newCount := testutil.ToFloat64(KafkaPublishTotal.WithLabelValues("test.topic", "success"))
	assert.Equal(t, initial+1, newCount, "kafka publish counter should increment")
}

func TestSetCircuitBreakerState(t *testing.T) {
	SetCircuitBreakerState("position-keeping", 0)
	value := testutil.ToFloat64(CircuitBreakerStateGauge.WithLabelValues("position-keeping"))
	assert.Equal(t, float64(0), value, "circuit breaker state should be 0 (closed)")

	SetCircuitBreakerState("position-keeping", 2)
	value = testutil.ToFloat64(CircuitBreakerStateGauge.WithLabelValues("position-keeping"))
	assert.Equal(t, float64(2), value, "circuit breaker state should be 2 (open)")
}

func TestRecordCircuitBreakerTrip(t *testing.T) {
	initial := testutil.ToFloat64(CircuitBreakerTripsTotal.WithLabelValues("test-svc", "closed", "open"))
	RecordCircuitBreakerTrip("test-svc", "closed", "open")
	newCount := testutil.ToFloat64(CircuitBreakerTripsTotal.WithLabelValues("test-svc", "closed", "open"))
	assert.Equal(t, initial+1, newCount, "circuit breaker trip counter should increment")
}

func TestSetConsecutiveFailures(t *testing.T) {
	SetConsecutiveFailures(3)
	value := testutil.ToFloat64(ConsecutiveFailuresGauge)
	assert.Equal(t, float64(3), value, "consecutive failures gauge should be 3")

	SetConsecutiveFailures(0)
	value = testutil.ToFloat64(ConsecutiveFailuresGauge)
	assert.Equal(t, float64(0), value, "consecutive failures gauge should be reset to 0")
}

func TestBalanceImbalanceGauge(t *testing.T) {
	BalanceImbalanceGauge.WithLabelValues("GBP").Set(100.50)
	value := testutil.ToFloat64(BalanceImbalanceGauge.WithLabelValues("GBP"))
	assert.Equal(t, 100.50, value, "balance imbalance gauge should be set")

	BalanceImbalanceGauge.WithLabelValues("GBP").Set(0)
	value = testutil.ToFloat64(BalanceImbalanceGauge.WithLabelValues("GBP"))
	assert.Equal(t, float64(0), value, "balance imbalance gauge should be reset to 0")
}

func TestExistingMetrics(t *testing.T) {
	// Verify existing metrics still work after enhancement

	t.Run("BalanceAssertionTotal", func(t *testing.T) {
		initial := testutil.ToFloat64(BalanceAssertionTotal.WithLabelValues("PASSED", "POSITION_LEDGER"))
		BalanceAssertionTotal.WithLabelValues("PASSED", "POSITION_LEDGER").Inc()
		newCount := testutil.ToFloat64(BalanceAssertionTotal.WithLabelValues("PASSED", "POSITION_LEDGER"))
		assert.Equal(t, initial+1, newCount)
	})

	t.Run("SettlementFinalityTotal", func(t *testing.T) {
		initial := testutil.ToFloat64(SettlementFinalityTotal.WithLabelValues("SUCCESS"))
		SettlementFinalityTotal.WithLabelValues("SUCCESS").Inc()
		newCount := testutil.ToFloat64(SettlementFinalityTotal.WithLabelValues("SUCCESS"))
		assert.Equal(t, initial+1, newCount)
	})

	t.Run("PositionLockAttemptTotal", func(t *testing.T) {
		initial := testutil.ToFloat64(PositionLockAttemptTotal.WithLabelValues("ATTEMPTED"))
		PositionLockAttemptTotal.WithLabelValues("ATTEMPTED").Inc()
		newCount := testutil.ToFloat64(PositionLockAttemptTotal.WithLabelValues("ATTEMPTED"))
		assert.Equal(t, initial+1, newCount)
	})
}

func TestRunTypeConstants(t *testing.T) {
	assert.Equal(t, "scheduled", RunTypeScheduled)
	assert.Equal(t, "manual", RunTypeManual)
	assert.Equal(t, "rerun", RunTypeRerun)
}

func TestVarianceReasonConstants(t *testing.T) {
	reasons := []string{
		ReasonAmountMismatch,
		ReasonMissingEntry,
		ReasonQualityUpgrade,
		ReasonExternalMismatch,
		ReasonCorrectionApplied,
	}
	for _, r := range reasons {
		assert.NotEmpty(t, r, "variance reason constant should not be empty")
	}
}
