package audit

import (
	"testing"
)

func TestRecordKafkaMetrics(t *testing.T) {
	// These are simple counter/gauge/histogram wrappers.
	// Test that they don't panic when called with valid arguments.
	t.Run("RecordPollInterval", func(t *testing.T) {
		RecordPollInterval("test_schema", 5.0)
	})

	t.Run("RecordKafkaPublished", func(t *testing.T) {
		RecordKafkaPublished("test_schema", "INSERT", "success")
		RecordKafkaPublished("test_schema", "UPDATE", "failure")
	})

	t.Run("RecordKafkaConsumed", func(t *testing.T) {
		RecordKafkaConsumed("test_schema", "INSERT", "success")
		RecordKafkaConsumed("test_schema", "DELETE", "failure")
	})

	t.Run("RecordKafkaPublishDuration", func(t *testing.T) {
		RecordKafkaPublishDuration(0.123)
	})

	t.Run("RecordKafkaConsumeDuration", func(t *testing.T) {
		RecordKafkaConsumeDuration(0.456)
	})

	t.Run("RecordKafkaConsumerLag", func(t *testing.T) {
		RecordKafkaConsumerLag(42.0)
	})

	t.Run("RecordKafkaDLQ", func(t *testing.T) {
		RecordKafkaDLQ("test_schema", "max_retries_exceeded")
	})
}
