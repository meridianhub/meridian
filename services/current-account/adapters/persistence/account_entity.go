// Package persistence provides database persistence for the current account domain
package persistence

import (
	"time"

	"github.com/google/uuid"
)

// CurrentAccountEntity represents the database persistence model for current accounts.
// This entity MUST match the schema defined in migrations/current_account/*.sql
// The mapping between domain model and entity is handled by toEntity/toDomain functions.
type CurrentAccountEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Business fields - these column names must match the migration schema
	AccountID             string     `gorm:"column:account_id;type:varchar(100);uniqueIndex;not null"`            // Business account identifier
	AccountIdentification string     `gorm:"column:account_identification;type:varchar(34);uniqueIndex;not null"` // IBAN format
	AccountType           string     `gorm:"column:account_type;type:varchar(50);not null"`                       // current, savings, etc.
	Currency              string     `gorm:"column:currency;type:char(3);not null;default:'GBP'"`                 // ISO 4217
	Status                string     `gorm:"column:status;type:varchar(20);not null;default:'active'"`
	PartyID               uuid.UUID  `gorm:"column:party_id;type:uuid;not null;index"`
	Balance               int64      `gorm:"column:balance;not null;default:0"`           // in smallest currency unit (pence)
	AvailableBalance      int64      `gorm:"column:available_balance;not null;default:0"` // after pending transactions
	OverdraftLimit        int64      `gorm:"column:overdraft_limit;not null;default:0"`   // in smallest currency unit
	OverdraftRate         float64    `gorm:"column:overdraft_rate;type:numeric(5,4);not null;default:0"`
	BalanceUpdatedAt      *time.Time `gorm:"column:balance_updated_at"`
	OpenedAt              *time.Time `gorm:"column:opened_at;index"`
	ClosedAt              *time.Time `gorm:"column:closed_at;index"`

	// Optimistic locking
	Version int64 `gorm:"column:version;not null;default:1"`

	// Audit fields - must match BaseModel columns from migration
	CreatedAt time.Time  `gorm:"column:created_at;not null;default:now()"`
	CreatedBy string     `gorm:"column:created_by;type:varchar(100);not null"`
	UpdatedAt time.Time  `gorm:"column:updated_at;not null;default:now()"`
	UpdatedBy string     `gorm:"column:updated_by;type:varchar(100);not null"`
	DeletedAt *time.Time `gorm:"column:deleted_at;index"`
}

// TableName overrides the default table name with schema prefix
func (CurrentAccountEntity) TableName() string {
	return "current_account.accounts"
}
