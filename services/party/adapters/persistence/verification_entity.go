// Package persistence provides database persistence for the party domain
package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

// PartyVerificationEntity represents a KYC/AML verification record
type PartyVerificationEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Foreign key to party
	PartyID uuid.UUID `gorm:"column:party_id;type:uuid;not null;index:idx_party_verification_party_id"`

	// Provider's verification ID (external reference)
	VerificationID string `gorm:"column:verification_id;type:varchar(255);not null;uniqueIndex:idx_party_verification_verification_id"`

	// Provider name (e.g., "onfido", "stripe")
	Provider string `gorm:"column:provider;type:varchar(100);not null"`

	// Verification status (PENDING, APPROVED, REJECTED, MANUAL_REVIEW)
	// Note: Production uses verification_status enum, but GORM uses varchar for portability
	Status string `gorm:"column:status;type:varchar(20);not null;default:'PENDING';index:idx_party_verification_status"`

	// Risk score from provider (0.0 to 1.0)
	RiskScore *float64 `gorm:"column:risk_score;type:decimal(5,4)"`

	// Reason for the verification result
	Reason *string `gorm:"column:reason;type:text"`

	// When the verification was completed by the provider
	CompletedAt *time.Time `gorm:"column:completed_at;type:timestamptz"`

	// Provider-specific metadata as JSON
	Metadata *string `gorm:"column:metadata;type:jsonb;default:'{}'"`

	// Optimistic locking
	Version int64 `gorm:"column:version;not null;default:1"`

	// Timestamps
	CreatedAt time.Time `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:now()"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (PartyVerificationEntity) TableName() string {
	return "party_verification"
}

// AuditID returns the record ID as a string for audit logging.
// Implements the audit.Auditable interface.
func (p PartyVerificationEntity) AuditID() string {
	return p.ID.String()
}

// AuditTableName returns the table name for audit logging.
// Implements the audit.Auditable interface.
func (p PartyVerificationEntity) AuditTableName() string {
	return p.TableName()
}

// AfterCreate is a GORM hook that runs after INSERT operations on PartyVerificationEntity.
// It writes an audit outbox entry with the new verification data.
func (p *PartyVerificationEntity) AfterCreate(tx *gorm.DB) error {
	return audit.RecordCreate(tx, *p)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on PartyVerificationEntity.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
func (p *PartyVerificationEntity) BeforeUpdate(tx *gorm.DB) error {
	return audit.CaptureOldValue(tx, *p)
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on PartyVerificationEntity.
// It retrieves the old values from context and writes an audit outbox entry.
func (p *PartyVerificationEntity) AfterUpdate(tx *gorm.DB) error {
	return audit.RecordUpdate(tx, *p)
}

// AfterDelete is a GORM hook that runs after DELETE operations on PartyVerificationEntity.
// It writes an audit outbox entry with the deleted verification data.
func (p *PartyVerificationEntity) AfterDelete(tx *gorm.DB) error {
	return audit.RecordDelete(tx, *p)
}
