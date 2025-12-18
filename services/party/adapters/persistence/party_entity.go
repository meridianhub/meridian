// Package persistence provides database persistence for the party domain
package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

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

// AuditID returns the record ID as a string for audit logging.
// Implements the audit.Auditable interface.
func (p PartyEntity) AuditID() string {
	return p.ID.String()
}

// AuditTableName returns the table name for audit logging.
// Implements the audit.Auditable interface.
func (p PartyEntity) AuditTableName() string {
	return p.TableName()
}

// AfterCreate is a GORM hook that runs after INSERT operations on PartyEntity.
// It writes an audit outbox entry with the new party data.
func (p *PartyEntity) AfterCreate(tx *gorm.DB) error {
	return audit.RecordCreate(tx, *p)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on PartyEntity.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
//
// NOTE: This hook is skipped when:
// - The entity ID is not set (map-based updates via Model(&Entity{}).Updates(map...))
// - These patterns bypass hooks in GORM; the repository uses them for optimistic locking
func (p *PartyEntity) BeforeUpdate(tx *gorm.DB) error {
	return audit.CaptureOldValue(tx, *p)
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on PartyEntity.
// It retrieves the old values from context and writes an audit outbox entry.
//
// NOTE: This hook is skipped when:
// - The entity ID is not set (map-based updates bypass hooks)
// - Old values are not in context (BeforeUpdate was skipped)
func (p *PartyEntity) AfterUpdate(tx *gorm.DB) error {
	return audit.RecordUpdate(tx, *p)
}

// AfterDelete is a GORM hook that runs after DELETE operations on PartyEntity.
// It writes an audit outbox entry with the deleted party data.
func (p *PartyEntity) AfterDelete(tx *gorm.DB) error {
	return audit.RecordDelete(tx, *p)
}

// PartyAuditOutbox is an alias for the shared audit.AuditOutbox type.
// Kept for backward compatibility with existing tests and migrations.
type PartyAuditOutbox = audit.AuditOutbox
