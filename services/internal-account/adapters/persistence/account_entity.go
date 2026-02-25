// Package persistence provides database persistence for the internal account domain.
package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidAttributesScan is returned when scanning an incompatible type into AttributesJSON.
var ErrInvalidAttributesScan = errors.New("cannot scan into AttributesJSON")

// InternalAccountEntity represents the database persistence model for internal accounts.
// This entity MUST match the schema defined in migrations/20260112000001_initial.sql.
// The mapping between domain model and entity is handled by toEntity/toDomain functions.
type InternalAccountEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Audit fields - must match BaseModel columns from migration
	CreatedAt time.Time  `gorm:"column:created_at;not null;default:now()"`
	CreatedBy string     `gorm:"column:created_by;type:varchar(100);not null"`
	UpdatedAt time.Time  `gorm:"column:updated_at;not null;default:now()"`
	UpdatedBy string     `gorm:"column:updated_by;type:varchar(100);not null"`
	DeletedAt *time.Time `gorm:"column:deleted_at;index"`

	// Account identifiers
	AccountID   string `gorm:"column:account_id;type:varchar(100);uniqueIndex;not null"`
	AccountCode string `gorm:"column:account_code;type:varchar(50);not null;index"`
	Name        string `gorm:"column:name;type:varchar(255);not null"`

	// Account classification
	AccountType    string `gorm:"column:account_type;type:varchar(20);not null;index"`
	InstrumentCode string `gorm:"column:instrument_code;type:varchar(32);not null;index"`
	Dimension      string `gorm:"column:dimension;type:varchar(20);not null"`

	// Account status
	Status string `gorm:"column:status;type:varchar(20);not null;default:'ACTIVE';index"`

	// Clearing purpose (only meaningful for CLEARING account type)
	ClearingPurpose *string `gorm:"column:clearing_purpose;type:varchar(32)"`

	// Organization party ID for org-scoped accounts (NULL = global)
	OrgPartyID *uuid.UUID `gorm:"column:org_party_id;type:uuid"`

	// Product Directory fields (immutable after creation, nullable for pre-migration accounts)
	ProductTypeCode    *string `gorm:"column:product_type_code;type:varchar(100)"`
	ProductTypeVersion *int    `gorm:"column:product_type_version;type:integer"`

	// Counterparty details (nullable for non-nostro/vostro accounts)
	CounterpartyID          *string `gorm:"column:counterparty_id;type:varchar(100)"`
	CounterpartyName        *string `gorm:"column:counterparty_name;type:varchar(255)"`
	CounterpartyExternalRef *string `gorm:"column:counterparty_external_ref;type:varchar(255)"`

	// Extensible attributes
	Attributes AttributesJSON `gorm:"column:attributes;type:jsonb;not null;default:'{}'"`

	// Optimistic locking
	Version int64 `gorm:"column:version;not null;default:1"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (InternalAccountEntity) TableName() string {
	return "internal_account"
}

// AttributesJSON is a custom type for handling JSONB attributes column.
type AttributesJSON map[string]string

// Value implements driver.Valuer for database writes.
func (a AttributesJSON) Value() (driver.Value, error) {
	if a == nil {
		return "{}", nil
	}
	return json.Marshal(a)
}

// Scan implements sql.Scanner for database reads.
func (a *AttributesJSON) Scan(value interface{}) error {
	if value == nil {
		*a = make(AttributesJSON)
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("%w: unsupported type %T", ErrInvalidAttributesScan, value)
	}

	return json.Unmarshal(bytes, a)
}

// StatusHistoryEntity represents the database persistence model for status change audit trail.
// This entity MUST match the schema defined in migrations/20260112000001_initial.sql.
type StatusHistoryEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Foreign key reference (via account_id, not UUID)
	AccountID string `gorm:"column:account_id;type:varchar(100);not null;index"`

	// Status change details
	FromStatus string    `gorm:"column:from_status;type:varchar(20);not null"`
	ToStatus   string    `gorm:"column:to_status;type:varchar(20);not null"`
	Reason     string    `gorm:"column:reason;type:text"`
	ChangedBy  string    `gorm:"column:changed_by;type:varchar(100);not null"`
	ChangedAt  time.Time `gorm:"column:changed_at;not null;default:now();index"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (StatusHistoryEntity) TableName() string {
	return "internal_account_status_history"
}
