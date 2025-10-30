// Package persistence provides database entities and repository implementations
// for the financial accounting service domain models.
package persistence

import (
	"time"

	"github.com/google/uuid"
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
	BaseCurrency            string `gorm:"not null;size:3;index"`
	Status                  string `gorm:"not null;size:50;index"`

	// Idempotency
	IdempotencyKey string `gorm:"uniqueIndex;not null;size:255"`

	// Audit fields
	CreatedAt time.Time  `gorm:"not null"`
	UpdatedAt time.Time  `gorm:"not null"`
	CreatedBy string     `gorm:"size:255"`
	UpdatedBy string     `gorm:"size:255"`
	DeletedAt *time.Time `gorm:"index"` // Soft delete

	// Version for optimistic locking
	Version int `gorm:"not null"`
}

// TableName overrides the default table name
func (FinancialBookingLogEntity) TableName() string {
	return "financial_booking_logs"
}
