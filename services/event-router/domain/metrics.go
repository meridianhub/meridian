// Package domain provides Prometheus metrics for the event-router service.
// This service routes events from Kafka channels to registered handlers.
package domain

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// serviceName tracks which service the audit event originated from.
	// This helps distinguish event sources in a centralized consumer.
	serviceName   = "event-router"
	serviceNameMu sync.RWMutex

	// eventsConsumedTotal counts audit events successfully consumed from Kafka.
	// Labels: service (source service), topic (Kafka topic name)
	eventsConsumedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "events_consumed_total",
			Help:      "Total number of audit events consumed from Kafka by source service and topic",
		},
		[]string{"service", "topic"},
	)

	// measurementsRecordedTotal counts utilization measurements successfully recorded.
	// Labels: service (source service), asset_code (e.g., "USD", "KWH", "GPU_HOURS")
	measurementsRecordedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "measurements_recorded_total",
			Help:      "Total number of utilization measurements recorded to position-keeping",
		},
		[]string{"service", "asset_code"},
	)

	// transformationErrorsTotal counts errors during event transformation.
	// Labels: service (source service), error_type (e.g., "missing_tenant_context", "invalid_amount", "unsupported_operation")
	transformationErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "transformation_errors_total",
			Help:      "Total number of errors during audit event transformation",
		},
		[]string{"service", "error_type"},
	)

	// positionKeepingAPIErrorsTotal counts errors when calling Position Keeping service.
	// Labels: error_type (e.g., "grpc_unavailable", "invalid_request", "timeout")
	positionKeepingAPIErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "position_keeping_api_errors_total",
			Help:      "Total number of errors calling Position Keeping API",
		},
		[]string{"error_type"},
	)

	// kafkaConsumerLag tracks consumer lag per topic and partition.
	// Labels: topic (Kafka topic), partition (partition number)
	// This gauge shows how far behind the consumer is from the latest offset.
	// NOTE: Consumer lag is typically tracked by external Kafka exporters (e.g., kafka_exporter, Burrow)
	// rather than by the application itself. This metric is defined for completeness but may be
	// populated by an external monitoring system rather than RecordKafkaConsumerLag calls.
	kafkaConsumerLag = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "kafka_consumer_lag_messages",
			Help:      "Number of messages the consumer is behind the latest offset per topic and partition",
		},
		[]string{"topic", "partition"},
	)

	// eventProcessingDuration tracks time to transform and record an event.
	// Buckets range from 1ms to 10 seconds to capture both fast and slow processing.
	eventProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "event_processing_duration_seconds",
			Help:      "Duration of event processing (transform + record) in seconds",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"service"},
	)

	// mdsPublishTotal counts MDS publish attempts by status.
	// Labels: status ("success", "error")
	mdsPublishTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "mds_publish_total",
			Help:      "Total number of MDS publish attempts by status",
		},
		[]string{"status"},
	)

	// dualOutputLatency tracks per-output latency in the fan-out path.
	// Labels: output ("pk", "mds")
	dualOutputLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "dual_output_latency_seconds",
			Help:      "Per-output latency in the dual-output fan-out path",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"output"},
	)
)

// SetServiceName sets the service name for metrics (used for logging context).
// Not actively used in labels but maintained for consistency with other services.
func SetServiceName(name string) {
	if name == "" {
		name = "event-router"
	}
	serviceNameMu.Lock()
	defer serviceNameMu.Unlock()
	serviceName = name
}

// GetServiceName returns the currently configured service name.
func GetServiceName() string {
	serviceNameMu.RLock()
	defer serviceNameMu.RUnlock()
	return serviceName
}

// RecordEventConsumed increments the counter for successfully consumed events.
// service: source service name (e.g., "current-account", "financial-accounting")
// topic: Kafka topic name (e.g., "audit.events.current-account.v1")
func RecordEventConsumed(service, topic string) {
	eventsConsumedTotal.WithLabelValues(service, topic).Inc()
}

// RecordMeasurementRecorded increments the counter for successfully recorded measurements.
// service: source service name (e.g., "current-account")
// assetCode: asset code for the measurement (e.g., "USD", "KWH", "GPU_HOURS")
func RecordMeasurementRecorded(service, assetCode string) {
	measurementsRecordedTotal.WithLabelValues(service, assetCode).Inc()
}

// RecordTransformationError increments the counter for transformation errors.
// service: source service name
// errorType: type of error (e.g., "missing_tenant_context", "invalid_amount", "unsupported_operation")
func RecordTransformationError(service, errorType string) {
	transformationErrorsTotal.WithLabelValues(service, errorType).Inc()
}

// RecordPositionKeepingAPIError increments the counter for Position Keeping API errors.
// errorType: type of error (e.g., "grpc_unavailable", "invalid_request", "timeout")
func RecordPositionKeepingAPIError(errorType string) {
	positionKeepingAPIErrorsTotal.WithLabelValues(errorType).Inc()
}

// RecordKafkaConsumerLag sets the current consumer lag for a topic/partition.
// topic: Kafka topic name
// partition: partition number (as string)
// lag: number of messages behind the latest offset
func RecordKafkaConsumerLag(topic, partition string, lag float64) {
	kafkaConsumerLag.WithLabelValues(topic, partition).Set(lag)
}

// RecordEventProcessingDuration observes the duration of event processing.
// service: source service name
// durationSeconds: processing duration in seconds
func RecordEventProcessingDuration(service string, durationSeconds float64) {
	eventProcessingDuration.WithLabelValues(service).Observe(durationSeconds)
}

// RecordMDSPublish increments the MDS publish counter.
// status: "success" or "error"
func RecordMDSPublish(status string) {
	mdsPublishTotal.WithLabelValues(status).Inc()
}

// RecordDualOutputLatency observes per-output latency in the fan-out path.
// output: "pk" or "mds"
// durationSeconds: latency in seconds
func RecordDualOutputLatency(output string, durationSeconds float64) {
	dualOutputLatency.WithLabelValues(output).Observe(durationSeconds)
}
