// Package audit provides utilities for audit logging and background processing.
package audit

// Status constants for audit outbox entries.
// These are used in both the worker and database migrations.
//
// Status lifecycle:
//
//	pending -> processing -> completed (success)
//	pending -> processing -> pending (retry, under max retries)
//	pending -> processing -> failed (retry exhausted)
//
// Use these constants in SQL migrations with CHECK constraints:
//
//	CHECK (status IN ('pending', 'processing', 'failed', 'completed'))
const (
	// StatusPending indicates the entry is waiting to be processed.
	StatusPending = "pending"

	// StatusProcessing indicates the entry is currently being processed.
	// Entries stuck in this state for too long are reset to pending.
	StatusProcessing = "processing"

	// StatusFailed indicates the entry has exceeded max retries and will not be processed.
	// Manual intervention may be required to resolve failed entries.
	StatusFailed = "failed"

	// StatusCompleted indicates the entry was successfully processed.
	// Completed entries can be archived or deleted based on retention policy.
	StatusCompleted = "completed"
)

// Operation constants for audit events.
// These match the values used in GORM hooks and database constraints.
//
// Use these constants in SQL migrations with CHECK constraints:
//
//	CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE'))
const (
	// OperationInsert indicates a new record was created.
	OperationInsert = "INSERT"

	// OperationUpdate indicates an existing record was modified.
	OperationUpdate = "UPDATE"

	// OperationDelete indicates a record was removed (soft or hard delete).
	OperationDelete = "DELETE"
)

// Table name constants for audit tables.
// These are used for schema-qualified table names in multi-tenant setups.
const (
	// TableAuditOutbox is the name of the audit outbox table.
	TableAuditOutbox = "audit_outbox"

	// TableAuditLog is the name of the permanent audit log table.
	TableAuditLog = "audit_log"
)
