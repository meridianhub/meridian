package persistence

import (
	"time"

	"github.com/google/uuid"
)

// WithdrawalEntity represents the database persistence model for withdrawals
// Optimized for database concerns: audit fields, indexes, constraints
type WithdrawalEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"primaryKey"`

	// Foreign key to account table
	AccountID uuid.UUID `gorm:"not null;index:idx_withdrawal_account_status"`

	// Monetary amount
	AmountCents    int64  `gorm:"column:amount_cents;not null;check:amount_cents > 0"`
	InstrumentCode string `gorm:"column:instrument_code;not null;size:32"` // Instrument code (e.g. GBP, kWh)
	Dimension      string `gorm:"column:dimension;not null;size:20"`       // Asset dimension (e.g. CURRENCY, ENERGY)
	Precision      int    `gorm:"column:precision;not null;default:2"`     // Decimal places for the instrument

	// Lifecycle state
	Status string `gorm:"not null;size:20;index:idx_withdrawal_account_status;check:status IN ('PENDING','COMPLETED','FAILED','CANCELLED')"`

	// Unique reference for idempotency
	Reference string `gorm:"not null;size:255;uniqueIndex:idx_withdrawal_reference"`

	// Audit fields
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	Version   int64     `gorm:"not null;default:1"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (WithdrawalEntity) TableName() string {
	return "withdrawal"
}
