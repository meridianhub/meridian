package persistence

import (
	"time"

	"github.com/google/uuid"
)

// LedgerPostingEntity represents the database persistence model for ledger postings
// Optimized for database concerns: audit fields, indexes, constraints
type LedgerPostingEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey"`

	// Foreign key to booking log
	FinancialBookingLogID uuid.UUID `gorm:"type:uuid;not null;index:idx_booking_log;constraint:OnDelete:RESTRICT"`

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
	CreatedAt time.Time  `gorm:"not null"`
	UpdatedAt time.Time  `gorm:"not null"`
	CreatedBy string     `gorm:"size:255"`
	UpdatedBy string     `gorm:"size:255"`
	DeletedAt *time.Time `gorm:"index"` // Soft delete
}

// TableName overrides the default table name with schema prefix
func (LedgerPostingEntity) TableName() string {
	return "financial_accounting.ledger_postings"
}
