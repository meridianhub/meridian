// Package persistence provides database persistence for the party domain
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

// Audit-related errors
var (
	// ErrNilTransaction is returned when a nil transaction is passed to recordAudit
	ErrNilTransaction = errors.New("tx cannot be nil for audit recording")

	// ErrOldValueType is returned when old value has incorrect type in context
	ErrOldValueType = errors.New("failed to retrieve old party values from context: invalid type")

	// ErrOldValueNotFound is returned when old value is not found in context
	ErrOldValueNotFound = errors.New("old party values not found in context")
)

// contextKey is a private type for context keys to avoid collisions
type contextKey string

// auditOldValueKey is the context key used to store old values before an UPDATE operation.
// This allows BeforeUpdate hook to capture the old state and pass it to AfterUpdate hook.
const auditOldValueKey contextKey = "audit:old_party_value"

// PartyEntity represents the database persistence model for parties.
// This entity MUST match the schema defined in migrations/party/*.sql
// The mapping between domain model and entity is handled by toEntity/toDomain functions.
type PartyEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Business fields
	PartyType             string  `gorm:"column:party_type;type:varchar(20);not null;index:idx_party_party_type"`
	LegalName             string  `gorm:"column:legal_name;type:varchar(255);not null"`
	DisplayName           *string `gorm:"column:display_name;type:varchar(255)"`
	Status                string  `gorm:"column:status;type:varchar(20);not null;default:'ACTIVE';index:idx_party_status"`
	ExternalReference     *string `gorm:"column:external_reference;type:varchar(100);uniqueIndex:idx_party_external_ref,where:external_reference IS NOT NULL AND deleted_at IS NULL"`
	ExternalReferenceType *string `gorm:"column:external_reference_type;type:varchar(30);uniqueIndex:idx_party_external_ref,where:external_reference IS NOT NULL AND deleted_at IS NULL"`

	// Optimistic locking
	Version int64 `gorm:"column:version;not null;default:1"`

	// Audit fields
	CreatedAt time.Time  `gorm:"column:created_at;not null;default:now()"`
	CreatedBy string     `gorm:"column:created_by;type:varchar(100);not null"`
	UpdatedAt time.Time  `gorm:"column:updated_at;not null;default:now()"`
	UpdatedBy string     `gorm:"column:updated_by;type:varchar(100);not null"`
	DeletedAt *time.Time `gorm:"column:deleted_at;index"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (PartyEntity) TableName() string {
	return "party"
}

// PartyAuditOutbox represents an audit record waiting to be processed by the background worker.
// Records are written to the outbox within the same transaction as the business operation,
// ensuring atomicity and preventing lost audit records.
type PartyAuditOutbox struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index"`
	Operation     string    `gorm:"type:varchar(10);not null;index"` // INSERT, UPDATE, DELETE
	RecordID      uuid.UUID `gorm:"type:uuid;not null;index"`
	OldValues     string    `gorm:"type:text"`                                         // JSON representation of old values
	NewValues     string    `gorm:"type:text"`                                         // JSON representation of new values
	Status        string    `gorm:"type:varchar(20);not null;default:'pending';index"` // pending, processing, completed, failed
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP"`
	RetryCount    int       `gorm:"not null;default:0"`
	LastError     *string   `gorm:"type:text"`
	ChangedBy     *string   `gorm:"type:varchar(100)"`
	TransactionID *string   `gorm:"type:varchar(100)"`
	ClientIP      *string   `gorm:"type:varchar(45)"` // Pointer for NULL support
	UserAgent     *string   `gorm:"type:text"`
}

// TableName overrides the table name for PartyAuditOutbox.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (PartyAuditOutbox) TableName() string {
	return "audit_outbox"
}

// recordPartyAudit writes an audit outbox entry within the current transaction.
// This function is called by GORM hooks (AfterCreate, AfterUpdate, AfterDelete).
func recordPartyAudit(tx *gorm.DB, tableName, operation string, recordID uuid.UUID, oldValue, newValue interface{}) error {
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
		userID := audit.GetUserFromContext(tx.Statement.Context)
		changedBy = &userID
	}
	if changedBy == nil {
		defaultUser := audit.DefaultAuditUser
		changedBy = &defaultUser
	}

	// Create audit outbox entry
	outbox := PartyAuditOutbox{
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

// AfterCreate is a GORM hook that runs after INSERT operations on PartyEntity.
// It writes an audit outbox entry with the new party data.
func (p *PartyEntity) AfterCreate(tx *gorm.DB) error {
	return recordPartyAudit(tx, "party", "INSERT", p.ID, nil, p)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on PartyEntity.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
//
// NOTE: This hook is skipped when:
// - The entity ID is not set (map-based updates via Model(&Entity{}).Updates(map...))
// - These patterns bypass hooks in GORM; the repository uses them for optimistic locking
func (p *PartyEntity) BeforeUpdate(tx *gorm.DB) error {
	// Skip if ID is not set - this happens with map-based updates
	// where the model is an empty struct used only for table name resolution
	if p.ID == uuid.Nil {
		return nil
	}

	// Capture old values before the update
	// Use Unscoped() to find records even if soft-deleted (for audit completeness)
	var old PartyEntity
	if err := tx.Unscoped().First(&old, p.ID).Error; err != nil {
		return fmt.Errorf("failed to fetch old party values: %w", err)
	}

	// Store old values in transaction context for AfterUpdate to access
	if tx.Statement != nil && tx.Statement.Context != nil {
		ctx := context.WithValue(tx.Statement.Context, auditOldValueKey, &old)
		tx.Statement.Context = ctx
	}

	return nil
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on PartyEntity.
// It retrieves the old values from context and writes an audit outbox entry.
//
// NOTE: This hook is skipped when:
// - The entity ID is not set (map-based updates bypass hooks)
// - Old values are not in context (BeforeUpdate was skipped)
func (p *PartyEntity) AfterUpdate(tx *gorm.DB) error {
	// Skip if ID is not set - this happens with map-based updates
	if p.ID == uuid.Nil {
		return nil
	}

	// Retrieve old values from context (captured in BeforeUpdate)
	var old *PartyEntity
	if tx.Statement != nil && tx.Statement.Context != nil {
		if oldValue := tx.Statement.Context.Value(auditOldValueKey); oldValue != nil {
			var ok bool
			old, ok = oldValue.(*PartyEntity)
			if !ok {
				return ErrOldValueType
			}
		}
	}

	// Skip audit if old values are not available (BeforeUpdate was skipped)
	if old == nil {
		return nil
	}

	return recordPartyAudit(tx, "party", "UPDATE", p.ID, old, p)
}

// AfterDelete is a GORM hook that runs after DELETE operations on PartyEntity.
// It writes an audit outbox entry with the deleted party data.
func (p *PartyEntity) AfterDelete(tx *gorm.DB) error {
	return recordPartyAudit(tx, "party", "DELETE", p.ID, p, nil)
}
