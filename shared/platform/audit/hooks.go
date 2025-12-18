// Package audit provides utilities for audit logging including reusable GORM hook helpers.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Errors returned by audit hook helpers.
var (
	// ErrNilTransaction is returned when a nil transaction is passed to RecordAudit.
	ErrNilTransaction = errors.New("tx cannot be nil for audit recording")

	// ErrOldValueType is returned when the old value in context has an incorrect type.
	ErrOldValueType = errors.New("failed to retrieve old values from context: invalid type")

	// ErrOldValueNotFound is returned when old values are not found in context.
	ErrOldValueNotFound = errors.New("old values not found in context")
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

// AuditOutbox represents an audit record waiting to be processed by the background worker.
// Records are written to the outbox within the same transaction as the business operation,
// ensuring atomicity and preventing lost audit records.
//
// This is a generic struct that works with both UUID and string record IDs.
// Services should embed or reference this struct, or define their own compatible version.
//
//nolint:revive // AuditOutbox naming is intentional for clarity across package boundaries
type AuditOutbox struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      string    `gorm:"type:varchar(50);not null;index" json:"record_id"` // String to support both UUID and custom IDs
	OldValues     string    `gorm:"type:jsonb" json:"old_values,omitempty"`
	NewValues     string    `gorm:"type:jsonb" json:"new_values,omitempty"`
	Status        string    `gorm:"type:varchar(20);not null;default:'pending';index" json:"status"`
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	RetryCount    int       `gorm:"not null;default:0" json:"retry_count"`
	LastError     *string   `gorm:"type:text" json:"last_error,omitempty"`
	ChangedBy     *string   `gorm:"type:varchar(100)" json:"changed_by,omitempty"`
	TransactionID *string   `gorm:"type:varchar(100)" json:"transaction_id,omitempty"`
	ClientIP      *string   `gorm:"type:varchar(45)" json:"client_ip,omitempty"`
	UserAgent     *string   `gorm:"type:text" json:"user_agent,omitempty"`
}

// TableName returns the table name for AuditOutbox.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (AuditOutbox) TableName() string {
	return "audit_outbox"
}

// AuditLog represents a permanent audit log entry.
// Records are moved from the audit_outbox to audit_log by the background worker.
// Once written, audit log entries are immutable and provide a permanent audit trail.
//
//nolint:revive // AuditLog naming is intentional for clarity across package boundaries
type AuditLog struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      string    `gorm:"type:varchar(50);not null;index" json:"record_id"` // String to support both UUID and custom IDs
	OldValues     string    `gorm:"type:jsonb" json:"old_values,omitempty"`
	NewValues     string    `gorm:"type:jsonb" json:"new_values,omitempty"`
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	ChangedBy     *string   `gorm:"type:varchar(100)" json:"changed_by,omitempty"`
	TransactionID *string   `gorm:"type:varchar(100)" json:"transaction_id,omitempty"`
	ClientIP      *string   `gorm:"type:varchar(45)" json:"client_ip,omitempty"`
	UserAgent     *string   `gorm:"type:text" json:"user_agent,omitempty"`
}

// TableName returns the table name for AuditLog.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (AuditLog) TableName() string {
	return "audit_log"
}

// Auditable defines the interface that an entity must implement to use the generic audit helpers.
// This interface allows the helpers to extract the record ID from any entity type.
type Auditable interface {
	// AuditID returns the record ID as a string for audit logging.
	// For UUID-based entities, return id.String().
	// For string-based entities, return the ID directly.
	AuditID() string

	// AuditTableName returns the table name for audit logging.
	AuditTableName() string
}

// oldValueKey generates a unique context key for storing old values during update operations.
// Each entity type should use its own key to avoid collisions when multiple entities
// are updated in the same transaction.
func oldValueKey(tableName string) contextKey {
	return contextKey("audit:old_value:" + tableName)
}

// RecordCreate writes an audit outbox entry for an INSERT operation.
// Call this from your entity's AfterCreate hook.
//
// Example:
//
//	func (e *MyEntity) AfterCreate(tx *gorm.DB) error {
//	    return audit.RecordCreate(tx, e)
//	}
func RecordCreate[T Auditable](tx *gorm.DB, entity T) error {
	return recordAudit(tx, entity.AuditTableName(), "INSERT", entity.AuditID(), nil, entity)
}

// CaptureOldValue fetches and stores the old entity values before an update.
// Call this from your entity's BeforeUpdate hook after any base model updates.
//
// Example:
//
//	func (e *MyEntity) BeforeUpdate(tx *gorm.DB) error {
//	    if err := e.BaseModel.BeforeUpdate(tx); err != nil {
//	        return err
//	    }
//	    return audit.CaptureOldValue(tx, e)
//	}
//
// Parameters:
//   - tx: The GORM transaction
//   - entity: The entity being updated (used to determine table name and ID)
//
// The function fetches the current database state and stores it in the transaction context
// for later retrieval by RecordUpdate.
func CaptureOldValue[T Auditable](tx *gorm.DB, entity T) error {
	// Get the record ID
	idStr := entity.AuditID()

	// Skip if ID is empty (happens with map-based updates)
	if idStr == "" || idStr == uuid.Nil.String() {
		return nil
	}

	// Fetch old values using reflection-free approach
	// We need to query the database directly and unmarshal into a new instance
	var old T
	if err := tx.Unscoped().First(&old, "id = ?", idStr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Record doesn't exist yet (edge case), skip audit
			return nil
		}
		return fmt.Errorf("failed to fetch old values for audit: %w", err)
	}

	// Store old values in transaction context
	if tx.Statement != nil && tx.Statement.Context != nil {
		key := oldValueKey(entity.AuditTableName())
		ctx := context.WithValue(tx.Statement.Context, key, old)
		tx.Statement.Context = ctx
	}

	return nil
}

// RecordUpdate writes an audit outbox entry for an UPDATE operation.
// Call this from your entity's AfterUpdate hook.
// It retrieves the old values stored by CaptureOldValue and records the change.
//
// Example:
//
//	func (e *MyEntity) AfterUpdate(tx *gorm.DB) error {
//	    return audit.RecordUpdate(tx, e)
//	}
//
// Returns nil without error if:
//   - The entity ID is empty (map-based updates)
//   - Old values were not captured (CaptureOldValue was skipped)
func RecordUpdate[T Auditable](tx *gorm.DB, entity T) error {
	if tx == nil {
		return ErrNilTransaction
	}

	// Skip if ID is empty (happens with map-based updates)
	idStr := entity.AuditID()
	if idStr == "" || idStr == uuid.Nil.String() {
		return nil
	}

	// Retrieve old values from context
	var old *T
	if tx.Statement != nil && tx.Statement.Context != nil {
		key := oldValueKey(entity.AuditTableName())
		if oldValue := tx.Statement.Context.Value(key); oldValue != nil {
			typedOld, ok := oldValue.(T)
			if !ok {
				return ErrOldValueType
			}
			old = &typedOld
		}
	}

	// Skip audit if old values are not available (CaptureOldValue was skipped)
	if old == nil {
		return nil
	}

	return recordAudit(tx, entity.AuditTableName(), "UPDATE", idStr, old, entity)
}

// RecordDelete writes an audit outbox entry for a DELETE operation.
// Call this from your entity's AfterDelete hook.
//
// Example:
//
//	func (e *MyEntity) AfterDelete(tx *gorm.DB) error {
//	    return audit.RecordDelete(tx, e)
//	}
func RecordDelete[T Auditable](tx *gorm.DB, entity T) error {
	return recordAudit(tx, entity.AuditTableName(), "DELETE", entity.AuditID(), entity, nil)
}

// Global schema name for the current service.
// Set by the service during initialization.
var (
	globalSchemaName string
)

// SetSchemaName sets the schema name for audit events.
// This should be called during service initialization.
func SetSchemaName(schema string) {
	globalSchemaName = schema
}

// GetSchemaName returns the configured schema name.
func GetSchemaName() string {
	return globalSchemaName
}

// recordAudit writes an audit record using Kafka with outbox fallback.
// This is the internal implementation used by all public helper functions.
//
// The function attempts to publish to Kafka first (if configured and enabled).
// If Kafka publishing fails or is not available, it falls back to writing
// to the audit_outbox table for reliable eventual processing.
func recordAudit(tx *gorm.DB, tableName, operation, recordID string, oldValue, newValue interface{}) error {
	if tx == nil {
		return ErrNilTransaction
	}

	// Serialize old and new values to JSON
	var oldJSON, newJSON string

	if oldValue != nil {
		oldBytes, err := json.Marshal(oldValue)
		if err != nil {
			return fmt.Errorf("failed to marshal old value: %w", err)
		}
		oldJSON = string(oldBytes)
	}

	if newValue != nil {
		newBytes, err := json.Marshal(newValue)
		if err != nil {
			return fmt.Errorf("failed to marshal new value: %w", err)
		}
		newJSON = string(newBytes)
	}

	// Extract user ID from context
	changedBy := DefaultAuditUser
	if tx.Statement != nil && tx.Statement.Context != nil {
		if userID := GetUserFromContext(tx.Statement.Context); userID != "" {
			changedBy = userID
		}
	}

	// Use Kafka with outbox fallback
	return publishToKafkaWithFallback(
		tx,
		tableName,
		operation,
		recordID,
		oldJSON,
		newJSON,
		changedBy,
		globalSchemaName,
	)
}
