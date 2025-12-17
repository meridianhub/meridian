package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
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

// ledgerPostingOldValueKey is the context key for storing old values before UPDATE
const ledgerPostingOldValueKey contextKey = "audit:ledger_posting_old_value"

// AfterCreate is a GORM hook that runs after INSERT operations.
func (e *LedgerPostingEntity) AfterCreate(tx *gorm.DB) error {
	// Skip audit if ID is not set (defensive check for edge cases)
	if e.ID == uuid.Nil {
		return nil
	}
	return recordAudit(tx, "ledger_posting", "INSERT", e.ID, nil, e)
}

// BeforeUpdate is a GORM hook that captures old values before UPDATE.
func (e *LedgerPostingEntity) BeforeUpdate(tx *gorm.DB) error {
	// Skip audit capture if ID is not set (happens when using db.Model().Update())
	if e.ID == uuid.Nil {
		return nil
	}

	var old LedgerPostingEntity
	if err := tx.First(&old, e.ID).Error; err != nil {
		return fmt.Errorf("failed to fetch old ledger posting values: %w", err)
	}

	if tx.Statement != nil && tx.Statement.Context != nil {
		ctx := context.WithValue(tx.Statement.Context, ledgerPostingOldValueKey, &old)
		tx.Statement.Context = ctx
	}

	return nil
}

// AfterUpdate is a GORM hook that runs after UPDATE operations.
func (e *LedgerPostingEntity) AfterUpdate(tx *gorm.DB) error {
	// Skip audit if ID is not set (defensive check)
	if e.ID == uuid.Nil {
		return nil
	}

	var old *LedgerPostingEntity
	if tx.Statement != nil && tx.Statement.Context != nil {
		if oldValue := tx.Statement.Context.Value(ledgerPostingOldValueKey); oldValue != nil {
			var ok bool
			old, ok = oldValue.(*LedgerPostingEntity)
			if !ok {
				return ErrOldValueType
			}
		}
	}

	// Skip if old values weren't captured (happens with partial updates via db.Model().Update())
	if old == nil {
		return nil
	}

	return recordAudit(tx, "ledger_posting", "UPDATE", e.ID, old, e)
}

// AfterDelete is a GORM hook that runs after DELETE operations.
func (e *LedgerPostingEntity) AfterDelete(tx *gorm.DB) error {
	// Skip audit if ID is not set (defensive check)
	if e.ID == uuid.Nil {
		return nil
	}
	return recordAudit(tx, "ledger_posting", "DELETE", e.ID, e, nil)
}
