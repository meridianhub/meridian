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
	AmountCents int64  `gorm:"not null;check:amount_cents > 0"`
	Currency    string `gorm:"not null;size:3"`

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
