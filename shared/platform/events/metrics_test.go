package events

import (
	"testing"
)

func TestRecordStuckEntriesReset(t *testing.T) {
	// Verify it doesn't panic with valid arguments.
	RecordStuckEntriesReset("test-service", 5)
	RecordStuckEntriesReset("test-service", 0)
}

func TestRecordOutboxDepth(t *testing.T) {
	RecordOutboxDepth("test-service", 10)
	RecordOutboxDepth("test-service", 0)
}

func TestRecordPublished(t *testing.T) {
	RecordPublished("test-service", "event.type", "success")
	RecordPublished("test-service", "event.type", "failure")
	RecordPublished("test-service", "event.type", "timeout")
}

func TestRecordDLQEntry(t *testing.T) {
	RecordDLQEntry("test-service", "event.type")
}

func TestRecordProcessingDuration(t *testing.T) {
	RecordProcessingDuration("test-service", 1.234)
}

func TestRecordEntryAge(t *testing.T) {
	RecordEntryAge("test-service", 0.5)
}

func TestRecordRetry(t *testing.T) {
	RecordRetry("test-service", "event.type")
}
