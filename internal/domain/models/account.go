// Package models defines domain models for the Meridian banking platform.
package models

import (
	"time"

	"github.com/google/uuid"
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

// TableName overrides the table name used by Account to `accounts`
func (Account) TableName() string {
	return "accounts"
}
