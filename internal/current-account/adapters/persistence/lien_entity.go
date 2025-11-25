package persistence

import (
	"time"

	"github.com/google/uuid"
)

// LienEntity represents the database persistence model for liens
// Optimized for database concerns: audit fields, indexes, constraints
type LienEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"primaryKey"`

	// Foreign key to current_accounts
	AccountID uuid.UUID `gorm:"not null;index:idx_liens_account_status"`

	// Monetary amount
	AmountCents int64  `gorm:"not null;check:amount_cents > 0"`
	Currency    string `gorm:"not null;size:3"`

	// Lifecycle state
	Status string `gorm:"not null;size:20;index:idx_liens_account_status;check:status IN ('ACTIVE','EXECUTED','TERMINATED')"`

	// Reference to the payment order that created this lien
	PaymentOrderReference string `gorm:"not null;size:255;index:idx_liens_payment_order"`

	// Reason for termination (only set when status is TERMINATED)
	TerminationReason string `gorm:"size:1000"`

	// Optional expiration time for automatic termination of stale liens
	ExpiresAt *time.Time `gorm:"index:idx_liens_expires_at"`

	// Audit fields
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	Version   int       `gorm:"not null;default:1"`
}

// TableName overrides the default table name
func (LienEntity) TableName() string {
	return "liens"
}
