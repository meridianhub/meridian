// Package models defines domain models for the Meridian banking platform.
package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

// Account represents a bank account in the system
// This follows BIAN Current Account domain model
type Account struct {
	BaseModel

	// Account identification
	AccountNumber string `gorm:"type:varchar(34);uniqueIndex;not null" json:"account_number"` // IBAN format
	AccountType   string `gorm:"type:varchar(50);not null" json:"account_type"`               // current, savings, etc.
	Currency      string `gorm:"type:char(3);not null;default:'GBP'" json:"currency"`         // ISO 4217
	Status        string `gorm:"type:varchar(20);not null;default:'active'" json:"status"`    // active, suspended, closed

	// Customer relationship
	CustomerID uuid.UUID `gorm:"type:uuid;not null;index" json:"customer_id"`
	Customer   *Customer `gorm:"foreignKey:CustomerID;constraint:OnDelete:RESTRICT" json:"customer,omitempty"`

	// Balance information
	Balance          int64 `gorm:"not null;default:0" json:"balance"`           // in smallest currency unit (pence)
	AvailableBalance int64 `gorm:"not null;default:0" json:"available_balance"` // after pending transactions

	// Account limits
	OverdraftLimit int64 `gorm:"not null;default:0" json:"overdraft_limit"` // in smallest currency unit

	// Audit fields
	OpenedAt *time.Time `gorm:"index" json:"opened_at,omitempty"`
	ClosedAt *time.Time `gorm:"index" json:"closed_at,omitempty"`
}

// TableName overrides the table name.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries
// to tenant-specific schemas (e.g., tenant_acme_bank.account).
func (Account) TableName() string {
	return "account"
}

// AuditID returns the record ID as a string for audit logging.
// Implements the audit.Auditable interface.
func (a Account) AuditID() string {
	return a.ID.String()
}

// AuditTableName returns the table name for audit logging.
// Implements the audit.Auditable interface.
func (a Account) AuditTableName() string {
	return a.TableName()
}

// AfterCreate is a GORM hook that runs after INSERT operations on Account.
// It writes an audit outbox entry with the new account data.
func (a *Account) AfterCreate(tx *gorm.DB) error {
	return audit.RecordCreate(tx, *a)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on Account.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
func (a *Account) BeforeUpdate(tx *gorm.DB) error {
	// First, call the base model's BeforeUpdate to handle UpdatedBy
	if err := a.BaseModel.BeforeUpdate(tx); err != nil {
		return err
	}
	return audit.CaptureOldValue(tx, *a)
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on Account.
// It retrieves the old values from context and writes an audit outbox entry.
func (a *Account) AfterUpdate(tx *gorm.DB) error {
	return audit.RecordUpdate(tx, *a)
}

// AfterDelete is a GORM hook that runs after DELETE operations on Account.
// It writes an audit outbox entry with the deleted account data.
func (a *Account) AfterDelete(tx *gorm.DB) error {
	return audit.RecordDelete(tx, *a)
}
