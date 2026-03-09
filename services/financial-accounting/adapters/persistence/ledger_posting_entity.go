package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// LedgerPostingEntity represents the database persistence model for ledger postings
// Optimized for database concerns: audit fields, indexes, constraints
//
// # Multi-Asset Support
//
// This entity supports both monetary (USD, EUR) and commodity (KWH, GPU_HOUR) quantities:
//   - AmountMinorUnits: Stores the amount in minor units (cents for currencies, base units for commodities)
//   - Currency: The instrument code (repurposed from currency-only to support any instrument)
//   - DimensionType: Distinguishes monetary ("CURRENCY") from commodity ("ENERGY", "COMPUTE", etc.)
//   - InstrumentVersion: Schema version for the instrument definition
//   - InstrumentPrecision: Number of decimal places for proper rounding
//   - Attributes: JSONB storage for contextual metadata (source system, transaction type, etc.)
//
// Backward compatibility: Existing rows have Currency set and will use defaults for new fields
// (DimensionType="CURRENCY", InstrumentVersion=1, InstrumentPrecision=2).
type LedgerPostingEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey"`

	// Foreign key to booking log
	FinancialBookingLogID uuid.UUID `gorm:"type:uuid;not null;index:idx_ledger_posting_booking_log_id;constraint:OnDelete:RESTRICT"`

	// Business fields
	PostingDirection string `gorm:"not null;size:10"`

	// AmountMinorUnits stores the amount in minor units based on instrument precision.
	// For precision 2 (USD, GBP): 100.50 becomes 10050
	// For precision 0 (JPY): 100 stays as 100
	// For precision 6 (KWH): 1.234567 becomes 1234567
	// Note: DB column is still 'amount_cents' for backward compatibility.
	AmountMinorUnits int64 `gorm:"column:amount_cents;not null"`

	// Currency stores the instrument code (e.g., "USD", "GBP", "KWH", "GPU_HOUR").
	// Originally for ISO 4217 currency codes, now repurposed for any instrument code.
	// Index retained for query performance on currency/instrument filtering.
	Currency string `gorm:"not null;size:32;index"`

	// DimensionType distinguishes monetary from commodity instruments.
	// Values: "CURRENCY", "ENERGY", "COMPUTE", "CARBON", "DATA", "COUNT", etc.
	// Default "CURRENCY" ensures backward compatibility with existing fiat-only postings.
	DimensionType string `gorm:"size:20;default:'CURRENCY'"`

	// InstrumentVersion is the schema version of the instrument definition.
	// Used for schema evolution and ensuring compatibility during arithmetic operations.
	// Default 1 for backward compatibility.
	InstrumentVersion uint32 `gorm:"default:1"`

	// InstrumentPrecision is the number of decimal places for this instrument.
	// Used for converting between minor units and decimal amounts.
	// Default 2 for backward compatibility (standard currency precision).
	InstrumentPrecision int `gorm:"default:2"`

	// Attributes stores contextual metadata as JSONB.
	// Can include: source system, transaction type, batch ID, external references, etc.
	// Default empty object for backward compatibility.
	Attributes datatypes.JSONType[map[string]string] `gorm:"type:jsonb;default:'{}'"`

	AccountID            string    `gorm:"not null;size:255;index"`
	AccountServiceDomain string    `gorm:"size:20;default:''"` // BIAN Service Domain: CURRENT_ACCOUNT, INTERNAL_ACCOUNT, or empty
	ValueDate            time.Time `gorm:"not null;index"`
	PostingResult        string    `gorm:"size:1000"`
	Status               string    `gorm:"not null;size:50;index"`

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
