// Package models provides legacy audit types for backward compatibility.
// New code should use the audit helpers from shared/platform/audit package.
package models

import (
	"time"

	"github.com/google/uuid"
)

// AuditOutbox represents an audit record waiting to be processed by the background worker.
// Records are written to the outbox within the same transaction as the business operation,
// ensuring atomicity and preventing lost audit records.
//
// Note: This is a legacy type kept for backward compatibility with existing migrations.
// The canonical AuditOutbox is now in shared/platform/audit.AuditOutbox (uses string RecordID).
// This version uses uuid.UUID for RecordID for compatibility with UUID-based entities.
type AuditOutbox struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      uuid.UUID `gorm:"type:uuid;not null;index" json:"record_id"`
	OldValues     string    `gorm:"type:text" json:"old_values,omitempty"`                           // JSON representation of old values
	NewValues     string    `gorm:"type:text" json:"new_values,omitempty"`                           // JSON representation of new values
	Status        string    `gorm:"type:varchar(20);not null;default:'pending';index" json:"status"` // pending, processing, failed
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	RetryCount    int       `gorm:"not null;default:0" json:"retry_count"`
	LastError     *string   `gorm:"type:text" json:"last_error,omitempty"`
	ChangedBy     *string   `gorm:"type:varchar(100)" json:"changed_by,omitempty"`
	TransactionID *string   `gorm:"type:varchar(100)" json:"transaction_id,omitempty"`
	ClientIP      *string   `gorm:"type:varchar(45)" json:"client_ip,omitempty"` // Pointer for NULL support
	UserAgent     *string   `gorm:"type:text" json:"user_agent,omitempty"`
}

// TableName overrides the table name for AuditOutbox.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (AuditOutbox) TableName() string {
	return "audit_outbox"
}

// AuditLog represents a permanent audit log entry.
// Records are moved from the audit_outbox to audit_log by the background worker.
// Once written, audit log entries are immutable and provide a permanent audit trail.
type AuditLog struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      uuid.UUID `gorm:"type:uuid;not null;index" json:"record_id"`
	OldValues     string    `gorm:"type:text" json:"old_values,omitempty"` // JSON representation of old values
	NewValues     string    `gorm:"type:text" json:"new_values,omitempty"` // JSON representation of new values
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	ChangedBy     *string   `gorm:"type:varchar(100)" json:"changed_by,omitempty"`
	TransactionID *string   `gorm:"type:varchar(100)" json:"transaction_id,omitempty"`
	ClientIP      *string   `gorm:"type:varchar(45)" json:"client_ip,omitempty"`
	UserAgent     *string   `gorm:"type:text" json:"user_agent,omitempty"`
}

// TableName overrides the table name for AuditLog.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (AuditLog) TableName() string {
	return "audit_log"
}
