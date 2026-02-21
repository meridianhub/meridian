// Package persistence provides database persistence for the party domain
package persistence

import (
	"time"

	"github.com/google/uuid"
)

// PartyTypeDefinitionEntity represents the database persistence model for party type definitions.
// Tenant-configurable schema for a party type, including JSON Schema for attribute validation
// and CEL expressions for cross-field validation, account eligibility, and custom error messages.
//
// The unique constraint (tenant_id, party_type) ensures each tenant can have at most one
// definition per party type.
type PartyTypeDefinitionEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// TenantID is the tenant that owns this party type definition.
	// Scopes definitions to a specific tenant.
	TenantID string `gorm:"column:tenant_id;type:varchar(100);not null;uniqueIndex:idx_party_type_def_tenant_type"`

	// PartyType is the party classification this definition applies to (e.g., "PERSON", "ORGANIZATION").
	// Uppercase alphanumeric with underscores, max 100 characters.
	PartyType string `gorm:"column:party_type;type:varchar(100);not null;uniqueIndex:idx_party_type_def_tenant_type"`

	// AttributeSchema is a JSON Schema document defining the structure and constraints
	// for party attributes of this type. Stored as text (up to 16KB).
	AttributeSchema string `gorm:"column:attribute_schema;type:text;not null"`

	// ValidationCEL is a CEL expression for cross-field validation of party attributes.
	ValidationCEL string `gorm:"column:validation_cel;type:text;not null;default:''"`

	// EligibilityCEL is a CEL expression for determining account type eligibility.
	EligibilityCEL string `gorm:"column:eligibility_cel;type:text;not null;default:''"`

	// ErrorMessageCEL is a CEL expression for generating custom error messages.
	ErrorMessageCEL string `gorm:"column:error_message_cel;type:text;not null;default:''"`

	// Optimistic locking
	Version int64 `gorm:"column:version;not null;default:1"`

	// Timestamps
	CreatedAt time.Time `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:now()"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (PartyTypeDefinitionEntity) TableName() string {
	return "party_type_definition"
}
