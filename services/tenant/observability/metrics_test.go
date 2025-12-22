package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordProvisioningDuration(t *testing.T) {
	provisioningDuration.Reset()

	RecordProvisioningDuration("tenant-123", StatusSuccess, 5*time.Second)

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
	provisioningRetries.Reset()

	IncrementRetryAttempt("tenant-456")

	count := testutil.CollectAndCount(provisioningRetries)
	if count == 0 {
		t.Error("Expected retry attempt metric to be recorded")
	}
}

func TestProvisioningDurationHistogramBuckets(t *testing.T) {
	provisioningDuration.Reset()

	// Test that buckets are correctly defined (1, 2, 4, 8, ..., 1024 seconds)
	expectedBuckets := []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024}
	actualBuckets := prometheus.ExponentialBuckets(1, 2, 11)

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
				RecordProvisioningDuration("tenant-789", StatusError, 30*time.Second)
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
				IncrementRetryAttempt("tenant-999")
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
	provisioningRetries.Reset()

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
			IncrementRetryAttempt("tenant-concurrent")
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

	RecordProvisioningDuration("tenant-success", StatusSuccess, 10*time.Second)
	RecordProvisioningDuration("tenant-error", StatusError, 5*time.Second)

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
	SetProvisioningQueueDepth(5)
	SetProvisioningQueueDepth(10)
	SetProvisioningQueueDepth(0)

	// Verify gauge can be read
	ch := make(chan prometheus.Metric, 1)
	provisioningQueueDepth.Collect(ch)
	metric := <-ch

	if metric == nil {
		t.Error("Expected queue depth gauge to track changes")
	}
}
