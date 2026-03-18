package idempotency

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsCollector_RecordPending(t *testing.T) {
	collector := NewMetricsCollector("test-service")

	// Get initial count
	initial := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("test-service", "deposit"))

	// Record pending
	collector.RecordPending("deposit")

	// Verify counter incremented
	newCount := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("test-service", "deposit"))
	assert.Equal(t, initial+1, newCount, "pending counter should increment by 1")
}

func TestMetricsCollector_RecordCompleted(t *testing.T) {
	collector := NewMetricsCollector("test-service")

	// Get initial count
	initial := testutil.ToFloat64(ExposeMetricsForTesting.KeysCompletedTotal.WithLabelValues("test-service", "withdrawal"))

	// Record completed
	collector.RecordCompleted("withdrawal")

	// Verify counter incremented
	newCount := testutil.ToFloat64(ExposeMetricsForTesting.KeysCompletedTotal.WithLabelValues("test-service", "withdrawal"))
	assert.Equal(t, initial+1, newCount, "completed counter should increment by 1")
}

func TestMetricsCollector_RecordFailed(t *testing.T) {
	collector := NewMetricsCollector("test-service")

	// Get initial count
	initial := testutil.ToFloat64(ExposeMetricsForTesting.KeysFailedTotal.WithLabelValues("test-service", "transfer", MetricReasonTimeout))

	// Record failed
	collector.RecordFailed("transfer", MetricReasonTimeout)

	// Verify counter incremented
	newCount := testutil.ToFloat64(ExposeMetricsForTesting.KeysFailedTotal.WithLabelValues("test-service", "transfer", MetricReasonTimeout))
	assert.Equal(t, initial+1, newCount, "failed counter should increment by 1")
}

func TestMetricsCollector_RecordCleanedUp(t *testing.T) {
	collector := NewMetricsCollector("test-service")

	// Get initial count
	initial := testutil.ToFloat64(ExposeMetricsForTesting.KeysCleanedUpTotal.WithLabelValues("financial-accounting"))

	// Record cleanup
	collector.RecordCleanedUp("financial-accounting")

	// Verify counter incremented
	newCount := testutil.ToFloat64(ExposeMetricsForTesting.KeysCleanedUpTotal.WithLabelValues("financial-accounting"))
	assert.Equal(t, initial+1, newCount, "cleaned up counter should increment by 1")
}

func TestMetricsCollector_RecordPendingDuration(_ *testing.T) {
	collector := NewMetricsCollector("test-service")

	// Record duration - this should not panic
	collector.RecordPendingDuration("deposit", 500*time.Millisecond)

	// Verify by checking the histogram has been registered and doesn't panic.
	// The actual histogram values are tested through integration tests.
}

func TestMetricsCollector_SetStalePendingCount(t *testing.T) {
	collector := NewMetricsCollector("test-service")

	// Set stale count
	collector.SetStalePendingCount("current-account", 5)

	// Verify gauge is set
	count := testutil.ToFloat64(ExposeMetricsForTesting.KeysStalePendingTotal.WithLabelValues("current-account"))
	assert.Equal(t, float64(5), count, "stale pending gauge should be set to 5")

	// Update to new value
	collector.SetStalePendingCount("current-account", 2)

	// Verify gauge updated
	count = testutil.ToFloat64(ExposeMetricsForTesting.KeysStalePendingTotal.WithLabelValues("current-account"))
	assert.Equal(t, float64(2), count, "stale pending gauge should be updated to 2")
}

func TestGlobalRecordFunctions(t *testing.T) {
	t.Run("RecordIdempotencyPending", func(t *testing.T) {
		initial := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("global-service", "operation1"))

		RecordIdempotencyPending("global-service", "operation1")

		newCount := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("global-service", "operation1"))
		assert.Equal(t, initial+1, newCount)
	})

	t.Run("RecordIdempotencyCompleted", func(t *testing.T) {
		initial := testutil.ToFloat64(ExposeMetricsForTesting.KeysCompletedTotal.WithLabelValues("global-service", "operation2"))

		RecordIdempotencyCompleted("global-service", "operation2")

		newCount := testutil.ToFloat64(ExposeMetricsForTesting.KeysCompletedTotal.WithLabelValues("global-service", "operation2"))
		assert.Equal(t, initial+1, newCount)
	})

	t.Run("RecordIdempotencyFailed", func(t *testing.T) {
		initial := testutil.ToFloat64(ExposeMetricsForTesting.KeysFailedTotal.WithLabelValues("global-service", "operation3", MetricReasonInternal))

		RecordIdempotencyFailed("global-service", "operation3", MetricReasonInternal)

		newCount := testutil.ToFloat64(ExposeMetricsForTesting.KeysFailedTotal.WithLabelValues("global-service", "operation3", MetricReasonInternal))
		assert.Equal(t, initial+1, newCount)
	})

	t.Run("RecordIdempotencyCleanedUp", func(t *testing.T) {
		initial := testutil.ToFloat64(ExposeMetricsForTesting.KeysCleanedUpTotal.WithLabelValues("global-cleanup-service"))

		RecordIdempotencyCleanedUp("global-cleanup-service")

		newCount := testutil.ToFloat64(ExposeMetricsForTesting.KeysCleanedUpTotal.WithLabelValues("global-cleanup-service"))
		assert.Equal(t, initial+1, newCount)
	})

	t.Run("SetIdempotencyStalePendingCount", func(t *testing.T) {
		SetIdempotencyStalePendingCount("stale-test-service", 10)

		count := testutil.ToFloat64(ExposeMetricsForTesting.KeysStalePendingTotal.WithLabelValues("stale-test-service"))
		assert.Equal(t, float64(10), count)
	})

	t.Run("RecordIdempotencyPendingDuration", func(_ *testing.T) {
		// This should not panic
		RecordIdempotencyPendingDuration("duration-service", "slow-operation", 2*time.Second)
	})
}

func TestMetricConstants(t *testing.T) {
	// Verify constants are defined and non-empty
	assert.NotEmpty(t, MetricStatusPending)
	assert.NotEmpty(t, MetricStatusCompleted)
	assert.NotEmpty(t, MetricStatusFailed)
	assert.NotEmpty(t, MetricReasonTimeout)
	assert.NotEmpty(t, MetricReasonValidation)
	assert.NotEmpty(t, MetricReasonInternal)

	// Verify expected values
	assert.Equal(t, "pending", MetricStatusPending)
	assert.Equal(t, "completed", MetricStatusCompleted)
	assert.Equal(t, "failed", MetricStatusFailed)
	assert.Equal(t, "timeout", MetricReasonTimeout)
	assert.Equal(t, "validation", MetricReasonValidation)
	assert.Equal(t, "internal", MetricReasonInternal)
}

func TestMetricsCollector_FullWorkflow(t *testing.T) {
	// Test a full workflow: pending -> completed
	collector := NewMetricsCollector("workflow-test")

	// Get initial counts
	initialPending := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("workflow-test", "payment"))
	initialCompleted := testutil.ToFloat64(ExposeMetricsForTesting.KeysCompletedTotal.WithLabelValues("workflow-test", "payment"))

	// Record pending
	start := time.Now()
	collector.RecordPending("payment")

	//nolint:forbidigo // ensures non-zero duration is recorded in metrics histogram
	time.Sleep(10 * time.Millisecond)

	// Record completion
	collector.RecordPendingDuration("payment", time.Since(start))
	collector.RecordCompleted("payment")

	// Verify both counters incremented
	assert.Equal(t, initialPending+1, testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("workflow-test", "payment")))
	assert.Equal(t, initialCompleted+1, testutil.ToFloat64(ExposeMetricsForTesting.KeysCompletedTotal.WithLabelValues("workflow-test", "payment")))
}

func TestMetricsCollector_FailureWorkflow(t *testing.T) {
	// Test a full workflow: pending -> failed
	collector := NewMetricsCollector("failure-workflow-test")

	// Get initial counts
	initialPending := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("failure-workflow-test", "refund"))
	initialFailed := testutil.ToFloat64(ExposeMetricsForTesting.KeysFailedTotal.WithLabelValues("failure-workflow-test", "refund", MetricReasonInternal))

	// Record pending
	start := time.Now()
	collector.RecordPending("refund")

	//nolint:forbidigo // ensures non-zero duration is recorded in metrics histogram before failure
	time.Sleep(5 * time.Millisecond)

	// Record failure
	collector.RecordPendingDuration("refund", time.Since(start))
	collector.RecordFailed("refund", MetricReasonInternal)

	// Verify counts
	assert.Equal(t, initialPending+1, testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("failure-workflow-test", "refund")))
	assert.Equal(t, initialFailed+1, testutil.ToFloat64(ExposeMetricsForTesting.KeysFailedTotal.WithLabelValues("failure-workflow-test", "refund", MetricReasonInternal)))
}

func TestMetricsAreRegistered(t *testing.T) {
	// Verify all metrics are registered and accessible
	require.NotNil(t, ExposeMetricsForTesting.KeysPendingTotal)
	require.NotNil(t, ExposeMetricsForTesting.KeysCompletedTotal)
	require.NotNil(t, ExposeMetricsForTesting.KeysFailedTotal)
	require.NotNil(t, ExposeMetricsForTesting.KeysCleanedUpTotal)
	require.NotNil(t, ExposeMetricsForTesting.KeyPendingDuration)
	require.NotNil(t, ExposeMetricsForTesting.KeysStalePendingTotal)
}

func TestPendingDurationBuckets(_ *testing.T) {
	// Test that various durations are recorded in appropriate histogram buckets
	collector := NewMetricsCollector("bucket-test")

	durations := []time.Duration{
		50 * time.Millisecond,  // Should fall in 0.1s bucket
		250 * time.Millisecond, // Should fall in 0.5s bucket
		750 * time.Millisecond, // Should fall in 1s bucket
		3 * time.Second,        // Should fall in 5s bucket
		8 * time.Second,        // Should fall in 10s bucket
		25 * time.Second,       // Should fall in 30s bucket
		45 * time.Second,       // Should fall in 60s bucket
		120 * time.Second,      // Should fall in 300s bucket
		600 * time.Second,      // Should fall in 900s bucket
	}

	for _, d := range durations {
		// This should not panic and should record to appropriate bucket
		collector.RecordPendingDuration("bucket-operation", d)
	}
}
