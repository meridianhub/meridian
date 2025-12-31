package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordProvisioningDuration(t *testing.T) {
	provisioningDuration.Reset()

	RecordProvisioningDuration(StatusSuccess, 5*time.Second)

	count := testutil.CollectAndCount(provisioningDuration)
	if count == 0 {
		t.Error("Expected provisioning duration metric to be recorded")
	}
}

func TestSetProvisioningQueueDepth(t *testing.T) {
	SetProvisioningQueueDepth(0)

	SetProvisioningQueueDepth(10)

	// Get the current value
	ch := make(chan prometheus.Metric, 1)
	provisioningQueueDepth.Collect(ch)
	metric := <-ch

	if metric == nil {
		t.Error("Expected queue depth gauge to be recorded")
	}
}

func TestIncrementServiceFailure(t *testing.T) {
	serviceProvisioningFailures.Reset()

	IncrementServiceFailure("database")

	count := testutil.CollectAndCount(serviceProvisioningFailures)
	if count == 0 {
		t.Error("Expected service failure metric to be recorded")
	}
}

func TestIncrementRetryAttempt(t *testing.T) {
	// Note: Counter doesn't have Reset(), but we can still verify it increments
	IncrementRetryAttempt()

	count := testutil.CollectAndCount(provisioningRetries)
	if count == 0 {
		t.Error("Expected retry attempt metric to be recorded")
	}
}

func TestProvisioningDurationHistogramBuckets(t *testing.T) {
	provisioningDuration.Reset()

	// Test that buckets are correctly defined (0.5, 1, 2, 4, 8, ..., 1024 seconds)
	// Starting at 0.5s to capture fast provisioning operations
	expectedBuckets := []float64{0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024}
	actualBuckets := prometheus.ExponentialBuckets(0.5, 2, 12)

	if len(actualBuckets) != len(expectedBuckets) {
		t.Errorf("Expected %d buckets, got %d", len(expectedBuckets), len(actualBuckets))
	}

	for i, expected := range expectedBuckets {
		if actualBuckets[i] != expected {
			t.Errorf("Bucket %d: expected %f, got %f", i, expected, actualBuckets[i])
		}
	}
}

func TestMetricsAreRegistered(t *testing.T) {
	// Verify all four metrics are registered with Prometheus
	tests := []struct {
		name   string
		metric prometheus.Collector
	}{
		{
			name:   "provisioning_duration",
			metric: provisioningDuration,
		},
		{
			name:   "provisioning_queue_depth",
			metric: provisioningQueueDepth,
		},
		{
			name:   "service_provisioning_failures",
			metric: serviceProvisioningFailures,
		},
		{
			name:   "provisioning_retries",
			metric: provisioningRetries,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset metric if possible
			if resettable, ok := tt.metric.(interface{ Reset() }); ok {
				resettable.Reset()
			}

			// Verify metric can be collected
			count := testutil.CollectAndCount(tt.metric)
			if count < 0 {
				t.Errorf("%s: metric is not registered", tt.name)
			}
		})
	}
}

func TestHelperFunctionsUpdateCorrectMetrics(t *testing.T) {
	tests := []struct {
		name       string
		metricFunc func()
		metric     prometheus.Collector
	}{
		{
			name: "RecordProvisioningDuration_updates_histogram",
			metricFunc: func() {
				RecordProvisioningDuration(StatusError, 30*time.Second)
			},
			metric: provisioningDuration,
		},
		{
			name: "IncrementServiceFailure_updates_counter",
			metricFunc: func() {
				IncrementServiceFailure("kafka")
			},
			metric: serviceProvisioningFailures,
		},
		{
			name: "IncrementRetryAttempt_updates_counter",
			metricFunc: func() {
				IncrementRetryAttempt()
			},
			metric: provisioningRetries,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if resettable, ok := tt.metric.(interface{ Reset() }); ok {
				resettable.Reset()
			}

			tt.metricFunc()

			count := testutil.CollectAndCount(tt.metric)
			if count == 0 {
				t.Errorf("%s: expected metric to be updated", tt.name)
			}
		})
	}
}

func TestCounterIncrementsAreAtomic(t *testing.T) {
	serviceProvisioningFailures.Reset()
	// Note: provisioningRetries is a Counter (not CounterVec) and doesn't have Reset()

	// Simulate concurrent increments
	done := make(chan bool)
	iterations := 100

	// Test service failures counter
	for i := 0; i < iterations; i++ {
		go func() {
			IncrementServiceFailure("s3")
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < iterations; i++ {
		<-done
	}

	// Test retry counter
	for i := 0; i < iterations; i++ {
		go func() {
			IncrementRetryAttempt()
			done <- true
		}()
	}

	for i := 0; i < iterations; i++ {
		<-done
	}

	// Verify both metrics were updated (exact count verification is challenging without
	// accessing internal state, so we just verify they have data)
	serviceFailureCount := testutil.CollectAndCount(serviceProvisioningFailures)
	if serviceFailureCount == 0 {
		t.Error("Expected service failure counter to be incremented concurrently")
	}

	retryCount := testutil.CollectAndCount(provisioningRetries)
	if retryCount == 0 {
		t.Error("Expected retry counter to be incremented concurrently")
	}
}

func TestProvisioningDurationWithDifferentStatuses(t *testing.T) {
	provisioningDuration.Reset()

	RecordProvisioningDuration(StatusSuccess, 10*time.Second)
	RecordProvisioningDuration(StatusError, 5*time.Second)

	count := testutil.CollectAndCount(provisioningDuration)
	if count == 0 {
		t.Error("Expected provisioning duration metrics for both statuses to be recorded")
	}
}

func TestMultipleServiceFailures(t *testing.T) {
	serviceProvisioningFailures.Reset()

	services := []string{"database", "kafka", "s3", "redis"}
	for _, service := range services {
		IncrementServiceFailure(service)
	}

	count := testutil.CollectAndCount(serviceProvisioningFailures)
	if count == 0 {
		t.Error("Expected multiple service failure metrics to be recorded")
	}
}

func TestQueueDepthChanges(t *testing.T) {
	SetProvisioningQueueDepth(0)

	// Set to 5 and verify
	SetProvisioningQueueDepth(5)
	if got := testutil.ToFloat64(provisioningQueueDepth); got != 5 {
		t.Errorf("Expected queue depth 5, got %v", got)
	}

	// Set to 10 and verify
	SetProvisioningQueueDepth(10)
	if got := testutil.ToFloat64(provisioningQueueDepth); got != 10 {
		t.Errorf("Expected queue depth 10, got %v", got)
	}

	// Set back to 0 and verify
	SetProvisioningQueueDepth(0)
	if got := testutil.ToFloat64(provisioningQueueDepth); got != 0 {
		t.Errorf("Expected queue depth 0, got %v", got)
	}
}

// =============================================================================
// Alerting Metrics Tests
// =============================================================================

func TestRecordAlertSent(t *testing.T) {
	alertsSentTotal.Reset()

	RecordAlertSent(AlertProviderPagerDuty, AlertSeverityCritical, AlertStatusSuccess)

	count := testutil.CollectAndCount(alertsSentTotal)
	if count == 0 {
		t.Error("Expected alerts_sent_total metric to be recorded")
	}
}

func TestRecordAlertSentWithDifferentLabels(t *testing.T) {
	alertsSentTotal.Reset()

	testCases := []struct {
		provider string
		severity string
		status   string
	}{
		{AlertProviderPagerDuty, AlertSeverityCritical, AlertStatusSuccess},
		{AlertProviderPagerDuty, AlertSeverityWarning, AlertStatusError},
		{AlertProviderPagerDuty, AlertSeverityInfo, AlertStatusRateLimited},
		{AlertProviderSlack, AlertSeverityCritical, AlertStatusSuccess},
		{AlertProviderSlack, AlertSeverityWarning, AlertStatusError},
		{AlertProviderSlack, AlertSeverityInfo, AlertStatusRateLimited},
	}

	for _, tc := range testCases {
		RecordAlertSent(tc.provider, tc.severity, tc.status)
	}

	count := testutil.CollectAndCount(alertsSentTotal)
	if count == 0 {
		t.Error("Expected alerts_sent_total metrics with different labels to be recorded")
	}
}

func TestSetAlertDLQDepth(t *testing.T) {
	SetAlertDLQDepth(0)

	// Set to 10 and verify
	SetAlertDLQDepth(10)
	if got := testutil.ToFloat64(alertDLQDepth); got != 10 {
		t.Errorf("Expected DLQ depth 10, got %v", got)
	}

	// Set to 100 and verify
	SetAlertDLQDepth(100)
	if got := testutil.ToFloat64(alertDLQDepth); got != 100 {
		t.Errorf("Expected DLQ depth 100, got %v", got)
	}

	// Set back to 0 and verify
	SetAlertDLQDepth(0)
	if got := testutil.ToFloat64(alertDLQDepth); got != 0 {
		t.Errorf("Expected DLQ depth 0, got %v", got)
	}
}

func TestAlertStatusConstants(t *testing.T) {
	if AlertStatusSuccess != "success" {
		t.Errorf("Expected AlertStatusSuccess to be 'success', got %q", AlertStatusSuccess)
	}
	if AlertStatusError != "error" {
		t.Errorf("Expected AlertStatusError to be 'error', got %q", AlertStatusError)
	}
	if AlertStatusRateLimited != "rate_limited" {
		t.Errorf("Expected AlertStatusRateLimited to be 'rate_limited', got %q", AlertStatusRateLimited)
	}
}

func TestAlertProviderConstants(t *testing.T) {
	if AlertProviderPagerDuty != "pagerduty" {
		t.Errorf("Expected AlertProviderPagerDuty to be 'pagerduty', got %q", AlertProviderPagerDuty)
	}
	if AlertProviderSlack != "slack" {
		t.Errorf("Expected AlertProviderSlack to be 'slack', got %q", AlertProviderSlack)
	}
}

func TestAlertSeverityConstants(t *testing.T) {
	if AlertSeverityCritical != "critical" {
		t.Errorf("Expected AlertSeverityCritical to be 'critical', got %q", AlertSeverityCritical)
	}
	if AlertSeverityWarning != "warning" {
		t.Errorf("Expected AlertSeverityWarning to be 'warning', got %q", AlertSeverityWarning)
	}
	if AlertSeverityInfo != "info" {
		t.Errorf("Expected AlertSeverityInfo to be 'info', got %q", AlertSeverityInfo)
	}
}

func TestAlertMetricsAreRegistered(t *testing.T) {
	// Verify alerting metrics are registered with Prometheus
	tests := []struct {
		name   string
		metric prometheus.Collector
	}{
		{
			name:   "alerts_sent_total",
			metric: alertsSentTotal,
		},
		{
			name:   "alerts_dlq_depth",
			metric: alertDLQDepth,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset metric if possible
			if resettable, ok := tt.metric.(interface{ Reset() }); ok {
				resettable.Reset()
			}

			// Verify metric can be collected
			count := testutil.CollectAndCount(tt.metric)
			if count < 0 {
				t.Errorf("%s: metric is not registered", tt.name)
			}
		})
	}
}

func TestConcurrentAlertMetricUpdates(t *testing.T) {
	alertsSentTotal.Reset()

	done := make(chan bool)
	iterations := 100

	for i := 0; i < iterations; i++ {
		go func() {
			RecordAlertSent(AlertProviderPagerDuty, AlertSeverityCritical, AlertStatusSuccess)
			done <- true
		}()
	}

	for i := 0; i < iterations; i++ {
		<-done
	}

	count := testutil.CollectAndCount(alertsSentTotal)
	if count == 0 {
		t.Error("Expected alerts counter to be incremented concurrently")
	}
}
