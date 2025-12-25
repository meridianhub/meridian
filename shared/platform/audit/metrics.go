// Package audit provides background processing for audit outbox entries.
// It includes metrics collection, worker processing, and graceful shutdown capabilities.
package audit

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// outboxDepthBySchema tracks the number of pending entries per schema
	outboxDepthBySchema = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "outbox_depth",
		Help:      "Number of pending entries in the audit outbox by schema",
	}, []string{"schema"})

	// outboxProcessedBySchema counts successfully processed audit entries per schema
	outboxProcessedBySchema = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "outbox_processed_total",
		Help:      "Total number of successfully processed audit entries by schema",
	}, []string{"schema"})

	// outboxFailedBySchema counts failed audit entries per schema
	outboxFailedBySchema = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "outbox_failed_total",
		Help:      "Total number of failed audit entries by schema",
	}, []string{"schema"})

	// processingDuration tracks the duration of batch processing operations
	// Note: Using simple histogram without batch_size label to avoid high cardinality.
	// Batch size correlation can be done via log analysis if needed.
	processingDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "processing_duration_seconds",
		Help:      "Duration of batch processing operations in seconds",
		Buckets:   prometheus.DefBuckets,
	})

	// batchSize tracks the actual batch sizes processed (useful for adaptive polling analysis)
	batchSizeHistogram = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "batch_size",
		Help:      "Number of entries processed per batch",
		Buckets:   []float64{0, 1, 5, 10, 25, 50, 100, 250, 500, 1000},
	})

	// currentPollInterval tracks the current adaptive poll interval per schema
	currentPollInterval = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "poll_interval_seconds",
		Help:      "Current poll interval in seconds (adaptive)",
	}, []string{"schema"})

	// emptyPolls tracks consecutive empty poll cycles (useful for tuning adaptive intervals)
	emptyPolls = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "empty_polls_consecutive",
		Help:      "Number of consecutive polls with no pending entries",
	}, []string{"schema"})

	// entryAge tracks how old entries are when they are processed (latency)
	entryAge = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "audit_worker",
		Name:      "entry_age_seconds",
		Help:      "Age of audit entries when processed (time from creation to processing)",
		Buckets:   prometheus.DefBuckets,
	})

	// Kafka-based audit metrics

	// kafkaEventsPublished counts audit events published to Kafka
	kafkaEventsPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "audit_kafka",
		Name:      "events_published_total",
		Help:      "Total number of audit events published to Kafka",
	}, []string{"schema", "operation", "status"})

	// kafkaEventsConsumed counts audit events consumed from Kafka
	kafkaEventsConsumed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "audit_kafka",
		Name:      "events_consumed_total",
		Help:      "Total number of audit events consumed from Kafka",
	}, []string{"schema", "operation", "status"})

	// kafkaPublishDuration tracks the duration of Kafka publish operations
	kafkaPublishDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "audit_kafka",
		Name:      "publish_duration_seconds",
		Help:      "Duration of Kafka publish operations in seconds",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	})

	// kafkaConsumeDuration tracks the duration of Kafka consume/process operations
	kafkaConsumeDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "audit_kafka",
		Name:      "consume_duration_seconds",
		Help:      "Duration of Kafka consume and process operations in seconds",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	})

	// kafkaConsumerLag tracks the consumer lag in messages
	kafkaConsumerLag = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "audit_kafka",
		Name:      "consumer_lag_messages",
		Help:      "Number of messages the consumer is behind the latest offset",
	})

	// kafkaFallbackUsed counts when Kafka publishing failed and outbox fallback was used
	kafkaFallbackUsed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "audit_kafka",
		Name:      "fallback_used_total",
		Help:      "Number of times Kafka publish failed and outbox fallback was used",
	}, []string{"schema", "reason"})

	// kafkaDLQMessages counts messages sent to DLQ
	kafkaDLQMessages = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "audit_kafka",
		Name:      "dlq_messages_total",
		Help:      "Total number of messages sent to dead letter queue",
	}, []string{"schema", "reason"})
)

// RecordOutboxDepthBySchema updates the gauge for the number of pending entries for a specific schema.
func RecordOutboxDepthBySchema(schema string, depth int) {
	outboxDepthBySchema.WithLabelValues(schema).Set(float64(depth))
}

// RecordProcessedBySchema increments the counter for successfully processed entries for a specific schema.
func RecordProcessedBySchema(schema string) {
	outboxProcessedBySchema.WithLabelValues(schema).Inc()
}

// RecordFailedBySchema increments the counter for failed entries for a specific schema.
func RecordFailedBySchema(schema string) {
	outboxFailedBySchema.WithLabelValues(schema).Inc()
}

// RecordProcessingDuration observes the duration of a batch processing operation
func RecordProcessingDuration(seconds float64) {
	processingDuration.Observe(seconds)
}

// RecordEntryAge observes the age of an entry when it is processed
func RecordEntryAge(ageSeconds float64) {
	entryAge.Observe(ageSeconds)
}

// RecordBatchSize observes the number of entries processed in a batch
func RecordBatchSize(size int) {
	batchSizeHistogram.Observe(float64(size))
}

// RecordPollInterval sets the current poll interval for a schema
func RecordPollInterval(schema string, intervalSeconds float64) {
	currentPollInterval.WithLabelValues(schema).Set(intervalSeconds)
}

// RecordEmptyPolls sets the consecutive empty poll count for a schema
func RecordEmptyPolls(schema string, count int) {
	emptyPolls.WithLabelValues(schema).Set(float64(count))
}

// RecordKafkaPublished increments the counter for events published to Kafka.
// status should be "success" or "failure".
func RecordKafkaPublished(schema, operation, status string) {
	kafkaEventsPublished.WithLabelValues(schema, operation, status).Inc()
}

// RecordKafkaConsumed increments the counter for events consumed from Kafka.
// status should be "success" or "failure".
func RecordKafkaConsumed(schema, operation, status string) {
	kafkaEventsConsumed.WithLabelValues(schema, operation, status).Inc()
}

// RecordKafkaPublishDuration observes the duration of a Kafka publish operation.
func RecordKafkaPublishDuration(seconds float64) {
	kafkaPublishDuration.Observe(seconds)
}

// RecordKafkaConsumeDuration observes the duration of a Kafka consume operation.
func RecordKafkaConsumeDuration(seconds float64) {
	kafkaConsumeDuration.Observe(seconds)
}

// RecordKafkaConsumerLag sets the current consumer lag.
func RecordKafkaConsumerLag(lag float64) {
	kafkaConsumerLag.Set(lag)
}

// RecordKafkaFallback increments the counter when outbox fallback is used.
func RecordKafkaFallback(schema, reason string) {
	kafkaFallbackUsed.WithLabelValues(schema, reason).Inc()
}

// RecordKafkaDLQ increments the counter when a message is sent to DLQ.
func RecordKafkaDLQ(schema, reason string) {
	kafkaDLQMessages.WithLabelValues(schema, reason).Inc()
}
