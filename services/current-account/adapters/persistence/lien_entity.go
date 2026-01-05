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

	// Foreign key to account table
	AccountID uuid.UUID `gorm:"not null;index:idx_lien_account_status"`

	// Monetary amount
	AmountCents int64  `gorm:"not null;check:amount_cents > 0"`
	Currency    string `gorm:"not null;size:3"`

	// Bucket identifier for bucket-aware reservations (fungibility key)
	// Empty string represents the default bucket for backward compatibility
	BucketID string `gorm:"column:bucket_id;size:255;not null;default:'';index:idx_lien_account_bucket"`

	// Lifecycle state
	Status string `gorm:"not null;size:20;index:idx_lien_account_status;check:status IN ('ACTIVE','EXECUTED','TERMINATED')"`

	// Reference to the payment order that created this lien (unique - each payment order has at most one lien)
	PaymentOrderReference string `gorm:"not null;size:255;uniqueIndex:idx_lien_payment_order"`

	// Reason for termination (only set when status is TERMINATED)
	TerminationReason string `gorm:"size:1000"`

	// Optional expiration time for automatic termination of stale liens
	ExpiresAt *time.Time `gorm:"index:idx_lien_expires_at"`

	// Audit fields
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	Version   int       `gorm:"not null;default:1"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (LienEntity) TableName() string {
	return "lien"
}
