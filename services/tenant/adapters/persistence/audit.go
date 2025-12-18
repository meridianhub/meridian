// Package persistence provides PostgreSQL persistence for tenants.
package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

// TenantAuditOutbox represents an audit record waiting to be processed by the background worker.
// Records are written to the outbox within the same transaction as the business operation,
// ensuring atomicity and preventing lost audit records.
//
// Note: This is a service-specific type kept for backward compatibility with existing migrations.
// Uses string IDs (varchar(50)) for record_id since tenants use string IDs.
type TenantAuditOutbox struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      string    `gorm:"type:varchar(50);not null;index" json:"record_id"` // String ID for tenant compatibility
	OldValues     string    `gorm:"type:jsonb" json:"old_values,omitempty"`           // JSON representation of old values
	NewValues     string    `gorm:"type:jsonb" json:"new_values,omitempty"`           // JSON representation of new values
	Status        string    `gorm:"type:varchar(20);not null;default:'pending';index" json:"status"`
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	RetryCount    int       `gorm:"not null;default:0" json:"retry_count"`
	LastError     *string   `gorm:"type:text" json:"last_error,omitempty"`
	ChangedBy     *string   `gorm:"type:varchar(100)" json:"changed_by,omitempty"`
	TransactionID *string   `gorm:"type:varchar(100)" json:"transaction_id,omitempty"`
	ClientIP      *string   `gorm:"type:varchar(45)" json:"client_ip,omitempty"`
	UserAgent     *string   `gorm:"type:text" json:"user_agent,omitempty"`
}

// TableName overrides the table name for TenantAuditOutbox.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (TenantAuditOutbox) TableName() string {
	return "audit_outbox"
}

// TenantAuditLog represents a permanent audit log entry.
// Records are moved from the audit_outbox to audit_log by the background worker.
// Once written, audit log entries are immutable and provide a permanent audit trail.
//
// Note: Uses string IDs (varchar(50)) for record_id since tenants use string IDs.
type TenantAuditLog struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      string    `gorm:"type:varchar(50);not null;index" json:"record_id"` // String ID for tenant compatibility
	OldValues     string    `gorm:"type:jsonb" json:"old_values,omitempty"`           // JSON representation of old values
	NewValues     string    `gorm:"type:jsonb" json:"new_values,omitempty"`           // JSON representation of new values
	ChangedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"changed_at"`
	ChangedBy     *string   `gorm:"type:varchar(100)" json:"changed_by,omitempty"`
	TransactionID *string   `gorm:"type:varchar(100)" json:"transaction_id,omitempty"`
	ClientIP      *string   `gorm:"type:varchar(45)" json:"client_ip,omitempty"`
	UserAgent     *string   `gorm:"type:text" json:"user_agent,omitempty"`
}

// TableName overrides the table name for TenantAuditLog.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (TenantAuditLog) TableName() string {
	return "audit_log"
}

// AfterCreate is a GORM hook that runs after INSERT operations on TenantEntity.
// It writes an audit outbox entry with the new tenant data.
func (t *TenantEntity) AfterCreate(tx *gorm.DB) error {
	return audit.RecordCreate(tx, *t)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on TenantEntity.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
//
// Note: This hook only runs for model-based updates (Save) where t.ID is populated.
// Map-based updates (Updates(map)) via Repository methods bypass this hook.
func (t *TenantEntity) BeforeUpdate(tx *gorm.DB) error {
	return audit.CaptureOldValue(tx, *t)
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on TenantEntity.
// It retrieves the old values from context and writes an audit outbox entry.
//
// Note: This hook only runs for model-based updates (Save) where BeforeUpdate captured old values.
// Map-based updates (Updates(map)) via Repository methods bypass audit recording.
func (t *TenantEntity) AfterUpdate(tx *gorm.DB) error {
	return audit.RecordUpdate(tx, *t)
}

// AfterDelete is a GORM hook that runs after DELETE operations on TenantEntity.
// It writes an audit outbox entry with the deleted tenant data.
func (t *TenantEntity) AfterDelete(tx *gorm.DB) error {
	return audit.RecordDelete(tx, *t)
}

// systemUser is an alias for audit.DefaultAuditUser for backward compatibility with tests.
const systemUser = audit.DefaultAuditUser

// AuditOutbox is an alias for the shared audit.AuditOutbox type.
// Kept for backward compatibility with existing tests.
type AuditOutbox = audit.AuditOutbox
