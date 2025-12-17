// Package persistence provides PostgreSQL persistence for tenants.
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"gorm.io/gorm"
)

const (
	// systemUser is the default user ID for background jobs and migrations
	systemUser = "system"
)

var (
	// ErrNilTransaction is returned when a nil transaction is passed to recordAudit
	ErrNilTransaction = errors.New("tx cannot be nil for audit recording")

	// ErrOldValueType is returned when old value has incorrect type in context
	ErrOldValueType = errors.New("failed to retrieve old tenant values from context: invalid type")

	// ErrOldValueNotFound is returned when old value is not found in context
	ErrOldValueNotFound = errors.New("old tenant values not found in context")
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
// Note: Tenant service uses string IDs (varchar(50)) for record_id, not UUIDs.
type AuditOutbox struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      string    `gorm:"type:varchar(50);not null;index" json:"record_id"` // String ID for tenant compatibility
	OldValues     string    `gorm:"type:text" json:"old_values,omitempty"`            // JSON representation of old values
	NewValues     string    `gorm:"type:text" json:"new_values,omitempty"`            // JSON representation of new values
	Status        string    `gorm:"type:varchar(20);not null;default:'pending';index" json:"status"`
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	RetryCount    int       `gorm:"not null;default:0" json:"retry_count"`
	LastError     *string   `gorm:"type:text" json:"last_error,omitempty"`
	ChangedBy     *string   `gorm:"type:varchar(100)" json:"changed_by,omitempty"`
	TransactionID *string   `gorm:"type:varchar(100)" json:"transaction_id,omitempty"`
	ClientIP      *string   `gorm:"type:varchar(45)" json:"client_ip,omitempty"`
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
//
// Note: Tenant service uses string IDs (varchar(50)) for record_id, not UUIDs.
type AuditLog struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      string    `gorm:"type:varchar(50);not null;index" json:"record_id"` // String ID for tenant compatibility
	OldValues     string    `gorm:"type:text" json:"old_values,omitempty"`            // JSON representation of old values
	NewValues     string    `gorm:"type:text" json:"new_values,omitempty"`            // JSON representation of new values
	ChangedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"changed_at"`
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
//   - tableName: The table being audited (e.g., "tenant")
//   - operation: The operation type ("INSERT", "UPDATE", "DELETE")
//   - recordID: The string ID of the record being audited (tenant ID)
//   - oldValue: The old state (nil for INSERT, populated for UPDATE/DELETE)
//   - newValue: The new state (populated for INSERT/UPDATE, nil for DELETE)
//
// Returns:
//   - error: Any error encountered during audit recording
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
	var changedBy *string
	if tx.Statement != nil && tx.Statement.Context != nil {
		if userID := getUserIDFromContext(tx.Statement.Context); userID != "" {
			changedBy = &userID
		}
	}
	if changedBy == nil {
		// Default to system
		user := systemUser
		changedBy = &user
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

// getUserIDFromContext extracts the user ID from the context.
// Returns empty string if not found or if type assertion fails.
func getUserIDFromContext(ctx any) string {
	if ctx == nil {
		return ""
	}

	// Safely convert to context.Context interface
	stdCtx, ok := ctx.(context.Context)
	if !ok {
		return ""
	}

	// Try to extract user_id from context (set by JWT auth interceptor)
	if userID, ok := stdCtx.Value(auth.UserIDContextKey).(string); ok {
		return userID
	}

	return ""
}

// AfterCreate is a GORM hook that runs after INSERT operations on TenantEntity.
// It writes an audit outbox entry with the new tenant data.
func (t *TenantEntity) AfterCreate(tx *gorm.DB) error {
	// Skip if entity ID is not set (shouldn't happen for Create, but defensive)
	if t.ID == "" {
		return nil
	}
	return recordAudit(tx, "tenant", "INSERT", t.ID, nil, t)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on TenantEntity.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
//
// Note: This hook only runs for model-based updates (Save) where t.ID is populated.
// Map-based updates (Updates(map)) via Repository methods bypass this hook.
func (t *TenantEntity) BeforeUpdate(tx *gorm.DB) error {
	// Skip if entity ID is not set (happens with map-based updates like Updates(map))
	// Map-based updates don't trigger hooks on the actual entity instance
	if t.ID == "" {
		return nil
	}

	// Capture old values before the update
	var old TenantEntity
	if err := tx.First(&old, "id = ?", t.ID).Error; err != nil {
		return fmt.Errorf("failed to fetch old tenant values: %w", err)
	}

	// Store old values in transaction context for AfterUpdate to access
	if tx.Statement != nil && tx.Statement.Context != nil {
		ctx := context.WithValue(tx.Statement.Context, auditOldValueKey, &old)
		tx.Statement.Context = ctx
	}

	return nil
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on TenantEntity.
// It retrieves the old values from context and writes an audit outbox entry.
//
// Note: This hook only runs for model-based updates (Save) where BeforeUpdate captured old values.
// Map-based updates (Updates(map)) via Repository methods bypass audit recording.
func (t *TenantEntity) AfterUpdate(tx *gorm.DB) error {
	// Skip if entity ID is not set (happens with map-based updates like Updates(map))
	if t.ID == "" {
		return nil
	}

	// Retrieve old values from context (captured in BeforeUpdate)
	var old *TenantEntity
	if tx.Statement != nil && tx.Statement.Context != nil {
		if oldValue := tx.Statement.Context.Value(auditOldValueKey); oldValue != nil {
			var ok bool
			old, ok = oldValue.(*TenantEntity)
			if !ok {
				return ErrOldValueType
			}
		}
	}

	// Skip audit if old values weren't captured (map-based update)
	if old == nil {
		return nil
	}

	return recordAudit(tx, "tenant", "UPDATE", t.ID, old, t)
}

// AfterDelete is a GORM hook that runs after DELETE operations on TenantEntity.
// It writes an audit outbox entry with the deleted tenant data.
func (t *TenantEntity) AfterDelete(tx *gorm.DB) error {
	// Skip if entity ID is not set (shouldn't happen for Delete, but defensive)
	if t.ID == "" {
		return nil
	}
	return recordAudit(tx, "tenant", "DELETE", t.ID, t, nil)
}
