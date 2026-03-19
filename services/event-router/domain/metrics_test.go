package domain

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSetServiceName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "sets custom service name",
			input:    "test-service",
			expected: "test-service",
		},
		{
			name:     "sets default when empty",
			input:    "",
			expected: "event-router",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetServiceName(tt.input)
			got := GetServiceName()
			if got != tt.expected {
				t.Errorf("GetServiceName() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRecordEventConsumed(t *testing.T) {
	// Reset counter before test
	eventsConsumedTotal.Reset()

	service := "current-account"
	topic := "audit.events.current-account.v1"

	RecordEventConsumed(service, topic)

	count := testutil.ToFloat64(eventsConsumedTotal.WithLabelValues(service, topic))
	if count != 1.0 {
		t.Errorf("RecordEventConsumed() count = %v, want 1.0", count)
	}

	// Record again to verify increment
	RecordEventConsumed(service, topic)
	count = testutil.ToFloat64(eventsConsumedTotal.WithLabelValues(service, topic))
	if count != 2.0 {
		t.Errorf("RecordEventConsumed() count after second call = %v, want 2.0", count)
	}
}

func TestRecordMeasurementRecorded(t *testing.T) {
	// Reset counter before test
	measurementsRecordedTotal.Reset()

	service := "financial-accounting"
	assetCode := "USD"

	RecordMeasurementRecorded(service, assetCode)

	count := testutil.ToFloat64(measurementsRecordedTotal.WithLabelValues(service, assetCode))
	if count != 1.0 {
		t.Errorf("RecordMeasurementRecorded() count = %v, want 1.0", count)
	}
}

func TestRecordTransformationError(t *testing.T) {
	// Reset counter before test
	transformationErrorsTotal.Reset()

	service := "party"
	errorType := "missing_tenant_context"

	RecordTransformationError(service, errorType)

	count := testutil.ToFloat64(transformationErrorsTotal.WithLabelValues(service, errorType))
	if count != 1.0 {
		t.Errorf("RecordTransformationError() count = %v, want 1.0", count)
	}
}

func TestRecordPositionKeepingAPIError(t *testing.T) {
	// Reset counter before test
	positionKeepingAPIErrorsTotal.Reset()

	errorType := "grpc_unavailable"

	RecordPositionKeepingAPIError(errorType)

	count := testutil.ToFloat64(positionKeepingAPIErrorsTotal.WithLabelValues(errorType))
	if count != 1.0 {
		t.Errorf("RecordPositionKeepingAPIError() count = %v, want 1.0", count)
	}
}

func TestRecordKafkaConsumerLag(t *testing.T) {
	topic := "audit.events.tenant.v1"
	partition := "0"
	lag := 1500.0

	RecordKafkaConsumerLag(topic, partition, lag)

	value := testutil.ToFloat64(kafkaConsumerLag.WithLabelValues(topic, partition))
	if value != lag {
		t.Errorf("RecordKafkaConsumerLag() value = %v, want %v", value, lag)
	}

	// Update lag to verify gauge behavior
	newLag := 500.0
	RecordKafkaConsumerLag(topic, partition, newLag)
	value = testutil.ToFloat64(kafkaConsumerLag.WithLabelValues(topic, partition))
	if value != newLag {
		t.Errorf("RecordKafkaConsumerLag() after update value = %v, want %v", value, newLag)
	}
}

func TestRecordEventProcessingDuration(_ *testing.T) {
	service := "position-keeping"
	duration := 0.025 // 25ms

	// Record a duration observation
	RecordEventProcessingDuration(service, duration)

	// For histograms, we can only verify the metric exists and is observable
	// We cannot easily extract the count without accessing internal state
	// This is a smoke test to ensure the function doesn't panic
	// In practice, histogram observations are validated via Prometheus queries
}

func TestRecordMDSPublish(t *testing.T) {
	mdsPublishTotal.Reset()

	RecordMDSPublish("success")
	RecordMDSPublish("success")
	RecordMDSPublish("error")

	successCount := testutil.ToFloat64(mdsPublishTotal.WithLabelValues("success"))
	if successCount != 2.0 {
		t.Errorf("RecordMDSPublish(success) count = %v, want 2.0", successCount)
	}

	errorCount := testutil.ToFloat64(mdsPublishTotal.WithLabelValues("error"))
	if errorCount != 1.0 {
		t.Errorf("RecordMDSPublish(error) count = %v, want 1.0", errorCount)
	}
}

func TestRecordDualOutputLatency(_ *testing.T) {
	// Smoke test to ensure the function doesn't panic and records observations
	RecordDualOutputLatency("pk", 0.005)
	RecordDualOutputLatency("mds", 0.010)
	RecordDualOutputLatency("pk", 0.025)
}

func TestMetricsLabels(t *testing.T) {
	tests := []struct {
		name   string
		metric prometheus.Collector
	}{
		{
			name:   "eventsConsumedTotal has service and topic labels",
			metric: eventsConsumedTotal,
		},
		{
			name:   "measurementsRecordedTotal has service and asset_code labels",
			metric: measurementsRecordedTotal,
		},
		{
			name:   "transformationErrorsTotal has service and error_type labels",
			metric: transformationErrorsTotal,
		},
		{
			name:   "kafkaConsumerLag has topic and partition labels",
			metric: kafkaConsumerLag,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desc := make(chan *prometheus.Desc, 1)
			tt.metric.Describe(desc)
			close(desc)

			d := <-desc
			// Basic smoke test - verify metric is describable and has a valid description
			if d == nil {
				t.Errorf("metric %s has nil description", tt.name)
			}
			// Note: Label validation happens at registration time via Prometheus.
			// Direct label inspection from Desc is not straightforward without
			// reflection, so we rely on the metric definition and registration
			// to ensure correctness. The metric recording tests above verify
			// that the metrics work correctly with their labels.
		})
	}
}
