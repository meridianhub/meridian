package models

import (
	"time"

	"github.com/google/uuid"
)

// Transaction represents a financial transaction
type Transaction struct {
	BaseModel

	// Transaction identification
	TransactionID   string `gorm:"type:varchar(100);uniqueIndex;not null" json:"transaction_id"`
	TransactionType string `gorm:"type:varchar(50);not null" json:"transaction_type"` // debit, credit, transfer

	// Account relationship
	AccountID uuid.UUID `gorm:"type:uuid;not null;index" json:"account_id"`

	// Transaction details
	Amount      int64  `gorm:"not null" json:"amount"`                              // in smallest currency unit
	Currency    string `gorm:"type:char(3);not null;default:'GBP'" json:"currency"` // ISO 4217
	Description string `gorm:"type:text" json:"description,omitempty"`
	Reference   string `gorm:"type:varchar(100)" json:"reference,omitempty"`

	// Status tracking
	Status string `gorm:"type:varchar(20);not null;default:'pending'" json:"status"` // pending, completed, failed, reversed

	// Counterparty information (for transfers)
	CounterpartyAccountID *uuid.UUID `gorm:"type:uuid;index" json:"counterparty_account_id,omitempty"`
	CounterpartyName      string     `gorm:"type:varchar(255)" json:"counterparty_name,omitempty"`

	// Balance after transaction
	BalanceAfter int64 `gorm:"not null" json:"balance_after"`

	// Processing metadata
	ProcessedAt *time.Time `gorm:"index" json:"processed_at,omitempty"`
	ReversedAt  *time.Time `gorm:"index" json:"reversed_at,omitempty"`
}

// TableName overrides the table name used by Transaction to `transactions`
func (Transaction) TableName() string {
	return "transactions"
}
