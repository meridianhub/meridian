package audit

import (
	"testing"
)

func TestRecordKafkaMetrics(t *testing.T) {
	// These are simple counter/gauge/histogram wrappers.
	// Test that they don't panic when called with valid arguments.
	t.Run("RecordPollInterval", func(_ *testing.T) {
		RecordPollInterval("test_schema", 5.0)
	})

	t.Run("RecordEmptyPolls", func(_ *testing.T) {
		RecordEmptyPolls("test_schema", 0)
		RecordEmptyPolls("test_schema", 5)
	})

	t.Run("RecordKafkaPublished", func(_ *testing.T) {
		RecordKafkaPublished("test_schema", "INSERT", "success")
		RecordKafkaPublished("test_schema", "UPDATE", "failure")
	})

	t.Run("RecordKafkaConsumed", func(_ *testing.T) {
		RecordKafkaConsumed("test_schema", "INSERT", "success")
		RecordKafkaConsumed("test_schema", "DELETE", "failure")
	})

	t.Run("RecordKafkaPublishDuration", func(_ *testing.T) {
		RecordKafkaPublishDuration(0.123)
	})

	t.Run("RecordKafkaConsumeDuration", func(_ *testing.T) {
		RecordKafkaConsumeDuration(0.456)
	})

	t.Run("RecordKafkaConsumerLag", func(_ *testing.T) {
		RecordKafkaConsumerLag(42.0)
	})

	t.Run("RecordKafkaDLQ", func(_ *testing.T) {
		RecordKafkaDLQ("test_schema", "max_retries_exceeded")
	})
}
