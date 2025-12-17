// Package models defines domain models for the Meridian banking platform.
package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
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

// AfterCreate is a GORM hook that runs after INSERT operations on Account.
// It writes an audit outbox entry with the new account data.
func (a *Account) AfterCreate(tx *gorm.DB) error {
	return recordAudit(tx, "account", "INSERT", a.ID, nil, a)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on Account.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
func (a *Account) BeforeUpdate(tx *gorm.DB) error {
	// First, call the base model's BeforeUpdate to handle UpdatedBy
	if err := a.BaseModel.BeforeUpdate(tx); err != nil {
		return err
	}

	// Capture old values before the update
	var old Account
	if err := tx.First(&old, a.ID).Error; err != nil {
		return fmt.Errorf("failed to fetch old account values: %w", err)
	}

	// Store old values in transaction context for AfterUpdate to access
	if tx.Statement != nil && tx.Statement.Context != nil {
		ctx := context.WithValue(tx.Statement.Context, auditAccountOldValueKey, &old)
		tx.Statement.Context = ctx
	}

	return nil
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on Account.
// It retrieves the old values from context and writes an audit outbox entry.
func (a *Account) AfterUpdate(tx *gorm.DB) error {
	// Retrieve old values from context (captured in BeforeUpdate)
	var old *Account
	if tx.Statement != nil && tx.Statement.Context != nil {
		if oldValue := tx.Statement.Context.Value(auditAccountOldValueKey); oldValue != nil {
			var ok bool
			old, ok = oldValue.(*Account)
			if !ok {
				return ErrAccountOldValueType
			}
		}
	}

	if old == nil {
		return ErrAccountOldValueNotFound
	}

	return recordAudit(tx, "account", "UPDATE", a.ID, old, a)
}

// AfterDelete is a GORM hook that runs after DELETE operations on Account.
// It writes an audit outbox entry with the deleted account data.
func (a *Account) AfterDelete(tx *gorm.DB) error {
	return recordAudit(tx, "account", "DELETE", a.ID, a, nil)
}
