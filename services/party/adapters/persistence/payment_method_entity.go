// Package persistence provides database persistence for the party domain
package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

// PaymentMethodEntity represents the database persistence model for party payment methods.
// This entity MUST match the schema defined in migrations/20260210000001_party_payment_methods.sql
type PaymentMethodEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Foreign key to party
	PartyID uuid.UUID `gorm:"column:party_id;type:uuid;not null"`

	// Provider fields
	Provider           string `gorm:"column:provider;type:varchar(50);not null"`
	ProviderCustomerID string `gorm:"column:provider_customer_id;type:varchar(255);not null"`
	ProviderMethodID   string `gorm:"column:provider_method_id;type:varchar(255);not null"`

	// Method type (CARD, BANK_ACCOUNT, SEPA)
	MethodType string `gorm:"column:method_type;type:varchar(50);not null"`

	// Default flag
	IsDefault bool `gorm:"column:is_default;not null;default:false"`

	// Non-sensitive display metadata (last4, brand, exp_month, exp_year)
	Metadata *string `gorm:"column:metadata;type:jsonb;default:'{}'"`

	// Lifecycle status (ACTIVE, EXPIRED, REMOVED)
	Status string `gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`

	// Optimistic locking
	Version int64 `gorm:"column:version;not null;default:1"`

	// Timestamps
	CreatedAt time.Time `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:now()"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (PaymentMethodEntity) TableName() string {
	return "party_payment_method"
}

// AuditID returns the record ID as a string for audit logging.
// Implements the audit.Auditable interface.
func (p PaymentMethodEntity) AuditID() string {
	return p.ID.String()
}

// AuditTableName returns the table name for audit logging.
// Implements the audit.Auditable interface.
func (p PaymentMethodEntity) AuditTableName() string {
	return p.TableName()
}

// AfterCreate is a GORM hook that runs after INSERT operations.
func (p *PaymentMethodEntity) AfterCreate(tx *gorm.DB) error {
	return audit.RecordCreate(tx, *p)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations.
func (p *PaymentMethodEntity) BeforeUpdate(tx *gorm.DB) error {
	return audit.CaptureOldValue(tx, *p)
}

// AfterUpdate is a GORM hook that runs after UPDATE operations.
func (p *PaymentMethodEntity) AfterUpdate(tx *gorm.DB) error {
	return audit.RecordUpdate(tx, *p)
}

// AfterDelete is a GORM hook that runs after DELETE operations.
func (p *PaymentMethodEntity) AfterDelete(tx *gorm.DB) error {
	return audit.RecordDelete(tx, *p)
}
