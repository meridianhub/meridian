package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

// LedgerPostingEntity represents the database persistence model for ledger postings
// Optimized for database concerns: audit fields, indexes, constraints
type LedgerPostingEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey"`

	// Foreign key to booking log
	FinancialBookingLogID uuid.UUID `gorm:"type:uuid;not null;index:idx_ledger_posting_booking_log_id;constraint:OnDelete:RESTRICT"`

	// Business fields
	PostingDirection string    `gorm:"not null;size:10"`
	AmountCents      int64     `gorm:"not null"`
	Currency         string    `gorm:"not null;size:3;index"`
	AccountID        string    `gorm:"not null;size:255;index"`
	ValueDate        time.Time `gorm:"not null;index"`
	PostingResult    string    `gorm:"size:1000"`
	Status           string    `gorm:"not null;size:50;index"`

	// Correlation for event sourcing
	CorrelationID string `gorm:"size:255;index"` // Links back to originating event

	// Audit fields
	CreatedAt time.Time      `gorm:"not null"`
	UpdatedAt time.Time      `gorm:"not null"`
	CreatedBy string         `gorm:"size:255"`
	UpdatedBy string         `gorm:"size:255"`
	DeletedAt gorm.DeletedAt `gorm:"index"` // Soft delete
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (LedgerPostingEntity) TableName() string {
	return "ledger_posting"
}

// AuditID returns the record ID as a string for audit logging.
// Implements the audit.Auditable interface.
func (e LedgerPostingEntity) AuditID() string {
	return e.ID.String()
}

// AuditTableName returns the table name for audit logging.
// Implements the audit.Auditable interface.
func (e LedgerPostingEntity) AuditTableName() string {
	return "ledger_posting"
}

// AfterCreate is a GORM hook that runs after INSERT operations.
// Publishes audit events to Kafka with outbox fallback.
func (e *LedgerPostingEntity) AfterCreate(tx *gorm.DB) error {
	return audit.RecordCreate(tx, *e)
}

// BeforeUpdate is a GORM hook that captures old values before UPDATE.
// Stores old values in context for AfterUpdate to use.
func (e *LedgerPostingEntity) BeforeUpdate(tx *gorm.DB) error {
	return audit.CaptureOldValue(tx, *e)
}

// AfterUpdate is a GORM hook that runs after UPDATE operations.
// Publishes audit events to Kafka with outbox fallback.
func (e *LedgerPostingEntity) AfterUpdate(tx *gorm.DB) error {
	return audit.RecordUpdate(tx, *e)
}

// AfterDelete is a GORM hook that runs after DELETE operations.
// Publishes audit events to Kafka with outbox fallback.
func (e *LedgerPostingEntity) AfterDelete(tx *gorm.DB) error {
	return audit.RecordDelete(tx, *e)
}
