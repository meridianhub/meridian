// Package persistence provides database persistence for the party domain
package persistence

import (
	"time"

	"github.com/google/uuid"
)

// PartyEntity represents the database persistence model for parties.
// This entity MUST match the schema defined in migrations/party/*.sql
// The mapping between domain model and entity is handled by toEntity/toDomain functions.
type PartyEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Business fields
	PartyType             string  `gorm:"column:party_type;type:varchar(20);not null;index:idx_parties_party_type"`
	LegalName             string  `gorm:"column:legal_name;type:varchar(255);not null"`
	DisplayName           *string `gorm:"column:display_name;type:varchar(255)"`
	Status                string  `gorm:"column:status;type:varchar(20);not null;default:'ACTIVE';index:idx_parties_status"`
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

// TableName overrides the default table name with schema prefix
func (PartyEntity) TableName() string {
	return "party.parties"
}
