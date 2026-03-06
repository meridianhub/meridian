// Package persistence provides database entities and repository implementations
// for the financial accounting service domain models.
package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

// FinancialBookingLogEntity represents the database persistence model
// Optimized for database concerns: audit fields, indexes, constraints
type FinancialBookingLogEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey"`

	// Business fields
	FinancialAccountType    string `gorm:"not null;size:50;index"`
	ProductServiceReference string `gorm:"not null;size:255;index"`
	BusinessUnitReference   string `gorm:"not null;size:255;index"`
	ChartOfAccountsRules    string `gorm:"not null;type:text"`
	BaseCurrency            string `gorm:"not null;size:32;index"`
	Status                  string `gorm:"not null;size:50;index"`

	// Idempotency
	IdempotencyKey string `gorm:"uniqueIndex;not null;size:255"`

	// Audit fields
	CreatedAt time.Time      `gorm:"not null"`
	UpdatedAt time.Time      `gorm:"not null"`
	CreatedBy string         `gorm:"size:255"`
	UpdatedBy string         `gorm:"size:255"`
	DeletedAt gorm.DeletedAt `gorm:"index"` // Soft delete

	// Version for optimistic locking
	Version int `gorm:"not null"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (FinancialBookingLogEntity) TableName() string {
	return "financial_booking_log"
}

// AuditID returns the record ID as a string for audit logging.
// Implements the audit.Auditable interface.
func (e FinancialBookingLogEntity) AuditID() string {
	return e.ID.String()
}

// AuditTableName returns the table name for audit logging.
// Implements the audit.Auditable interface.
func (e FinancialBookingLogEntity) AuditTableName() string {
	return "financial_booking_log"
}

// AfterCreate is a GORM hook that runs after INSERT operations.
// Publishes audit events to Kafka with outbox fallback.
func (e *FinancialBookingLogEntity) AfterCreate(tx *gorm.DB) error {
	return audit.RecordCreate(tx, *e)
}

// BeforeUpdate is a GORM hook that captures old values before UPDATE.
// Stores old values in context for AfterUpdate to use.
func (e *FinancialBookingLogEntity) BeforeUpdate(tx *gorm.DB) error {
	return audit.CaptureOldValue(tx, *e)
}

// AfterUpdate is a GORM hook that runs after UPDATE operations.
// Publishes audit events to Kafka with outbox fallback.
func (e *FinancialBookingLogEntity) AfterUpdate(tx *gorm.DB) error {
	return audit.RecordUpdate(tx, *e)
}

// AfterDelete is a GORM hook that runs after DELETE operations.
// Publishes audit events to Kafka with outbox fallback.
func (e *FinancialBookingLogEntity) AfterDelete(tx *gorm.DB) error {
	return audit.RecordDelete(tx, *e)
}
