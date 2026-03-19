package events

import (
	"testing"
)

func TestRecordStuckEntriesReset(_ *testing.T) {
	// Verify it doesn't panic with valid arguments.
	RecordStuckEntriesReset("test-service", 5)
	RecordStuckEntriesReset("test-service", 0)
}

func TestRecordOutboxDepth(_ *testing.T) {
	RecordOutboxDepth("test-service", 10)
	RecordOutboxDepth("test-service", 0)
}

func TestRecordPublished(_ *testing.T) {
	RecordPublished("test-service", "event.type", "success")
	RecordPublished("test-service", "event.type", "failure")
	RecordPublished("test-service", "event.type", "timeout")
}

func TestRecordDLQEntry(_ *testing.T) {
	RecordDLQEntry("test-service", "event.type")
}

func TestRecordProcessingDuration(_ *testing.T) {
	RecordProcessingDuration("test-service", 1.234)
}

func TestRecordEntryAge(_ *testing.T) {
	RecordEntryAge("test-service", 0.5)
}

func TestRecordRetry(_ *testing.T) {
	RecordRetry("test-service", "event.type")
}
