package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	// ErrNilTransaction is returned when a nil transaction is passed to recordAudit
	ErrNilTransaction = errors.New("tx cannot be nil for audit recording")

	// ErrOldValueType is returned when old value has incorrect type in context
	ErrOldValueType = errors.New("failed to retrieve old customer values from context: invalid type")

	// ErrOldValueNotFound is returned when old value is not found in context
	ErrOldValueNotFound = errors.New("old customer values not found in context")
)

// contextKey is a private type for context keys to avoid collisions
type contextKey string

// auditOldValueKey is the context key used to store old values before an UPDATE operation.
// This allows BeforeUpdate hook to capture the old state and pass it to AfterUpdate hook.
const auditOldValueKey contextKey = "audit:old_value"

// AuditOutbox represents an audit record waiting to be processed by the background worker.
// Records are written to the outbox within the same transaction as the business operation,
// ensuring atomicity and preventing lost audit records.
//
// The background worker (Phase 3) will asynchronously move records from outbox to audit_log.
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

// recordAudit writes an audit outbox entry within the current transaction.
// This function is called by GORM hooks (AfterCreate, AfterUpdate, AfterDelete).
//
// Parameters:
//   - tx: The GORM transaction (must be non-nil)
//   - tableName: The table being audited (e.g., "customer")
//   - operation: The operation type ("INSERT", "UPDATE", "DELETE")
//   - recordID: The UUID of the record being audited
//   - oldValue: The old state (nil for INSERT, populated for UPDATE/DELETE)
//   - newValue: The new state (populated for INSERT/UPDATE, nil for DELETE)
//
// Returns:
//   - error: Any error encountered during audit recording
func recordAudit(tx *gorm.DB, tableName, operation string, recordID uuid.UUID, oldValue, newValue interface{}) error {
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
	var changedBy *string
	if tx.Statement != nil && tx.Statement.Context != nil {
		if userID := getUserIDFromContext(tx.Statement.Context); userID != "" {
			changedBy = &userID
		}
	}
	if changedBy == nil {
		// Default to system
		systemUser := SystemUser
		changedBy = &systemUser
	}

	// Create audit outbox entry
	outbox := AuditOutbox{
		ID:        uuid.New(),
		Table:     tableName,
		Operation: operation,
		RecordID:  recordID,
		OldValues: oldJSON,
		NewValues: newJSON,
		Status:    "pending",
		ChangedBy: changedBy,
		CreatedAt: time.Now(),
	}

	// Write to outbox within the same transaction
	return tx.Create(&outbox).Error
}

// AfterCreate is a GORM hook that runs after INSERT operations on Customer.
// It writes an audit outbox entry with the new customer data.
func (c *Customer) AfterCreate(tx *gorm.DB) error {
	return recordAudit(tx, "customer", "INSERT", c.ID, nil, c)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on Customer.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
func (c *Customer) BeforeUpdate(tx *gorm.DB) error {
	// First, call the base model's BeforeUpdate to handle UpdatedBy
	if err := c.BaseModel.BeforeUpdate(tx); err != nil {
		return err
	}

	// Capture old values before the update
	var old Customer
	if err := tx.First(&old, c.ID).Error; err != nil {
		return fmt.Errorf("failed to fetch old customer values: %w", err)
	}

	// Store old values in transaction context for AfterUpdate to access
	if tx.Statement != nil && tx.Statement.Context != nil {
		ctx := context.WithValue(tx.Statement.Context, auditOldValueKey, &old)
		tx.Statement.Context = ctx
	}

	return nil
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on Customer.
// It retrieves the old values from context and writes an audit outbox entry.
func (c *Customer) AfterUpdate(tx *gorm.DB) error {
	// Retrieve old values from context (captured in BeforeUpdate)
	var old *Customer
	if tx.Statement != nil && tx.Statement.Context != nil {
		if oldValue := tx.Statement.Context.Value(auditOldValueKey); oldValue != nil {
			var ok bool
			old, ok = oldValue.(*Customer)
			if !ok {
				return ErrOldValueType
			}
		}
	}

	if old == nil {
		return ErrOldValueNotFound
	}

	return recordAudit(tx, "customer", "UPDATE", c.ID, old, c)
}

// AfterDelete is a GORM hook that runs after DELETE operations on Customer.
// It writes an audit outbox entry with the deleted customer data.
func (c *Customer) AfterDelete(tx *gorm.DB) error {
	return recordAudit(tx, "customer", "DELETE", c.ID, c, nil)
}
