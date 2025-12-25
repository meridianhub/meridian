// Package audit provides utilities for audit logging and background processing.
package audit

import "errors"

// Sentinel errors for audit operations.
// These errors are designed to be checked with errors.Is() for proper error handling.
//
// Error handling patterns:
//
//	if errors.Is(err, audit.ErrNilTransaction) {
//	    // Handle missing transaction
//	}
//
//	if errors.Is(err, audit.ErrAuditFailed) {
//	    // Handle audit failure
//	}
var (
	// ErrNilTransaction is returned when a nil database transaction is passed to an audit function.
	// This is a programmer error - audit operations must be called within a transaction context.
	ErrNilTransaction = errors.New("tx cannot be nil for audit recording")

	// ErrOldValueType is returned when the old value stored in context has an incorrect type.
	// This typically indicates a bug in the audit hooks or context management.
	ErrOldValueType = errors.New("failed to retrieve old values from context: invalid type")

	// ErrOldValueNotFound is returned when old values are not found in context during update.
	// This may indicate CaptureOldValue was not called before RecordUpdate.
	ErrOldValueNotFound = errors.New("old values not found in context")

	// ErrAuditFailed is returned when an audit operation fails and cannot be retried.
	// Check the wrapped error for details on the failure cause.
	ErrAuditFailed = errors.New("audit operation failed")

	// ErrContextMissing is returned when required context values are not present.
	// This may occur when audit functions are called outside of a proper request context.
	ErrContextMissing = errors.New("required context values missing")

	// ErrMaxRetriesExceeded is returned when an entry has exceeded the maximum retry count.
	// Entries in this state are moved to 'failed' status and require manual intervention.
	ErrMaxRetriesExceeded = errors.New("max retries exceeded")

	// ErrWorkerShutdown is returned when the worker is shutting down.
	// Operations should be retried once the worker restarts.
	ErrWorkerShutdown = errors.New("worker is shutting down")

	// ErrBatchProcessingFailed is returned when batch processing completes with failures.
	// Some entries in the batch may have been processed successfully.
	ErrBatchProcessingFailed = errors.New("batch processing completed with failures")

	// ErrSimulatedProcessingFailure is a test error for simulating processing failures.
	// This should only be used in tests.
	ErrSimulatedProcessingFailure = errors.New("simulated processing error")
)

// Publisher-specific errors.
var (
	// ErrPublisherDisabled indicates Kafka publishing is disabled (no bootstrap servers).
	// The system will fall back to the audit outbox pattern.
	ErrPublisherDisabled = errors.New("audit publisher disabled: no bootstrap servers configured")

	// ErrEventsNotDelivered indicates some events were not delivered during shutdown.
	// These events may be lost unless they were written to the outbox.
	ErrEventsNotDelivered = errors.New("audit events not delivered")

	// ErrEmptyRecordID indicates the event has no record ID for partitioning.
	// All audit events must have a valid record ID.
	ErrEmptyRecordID = errors.New("event RecordId cannot be empty")
)
