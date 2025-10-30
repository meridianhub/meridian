package persistence

import (
	"time"

	"github.com/google/uuid"
)

// CurrentAccountEntity represents the database persistence model for current accounts
// Optimized for database concerns: audit fields, indexes, constraints
type CurrentAccountEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Business fields
	AccountID             string    `gorm:"uniqueIndex;not null;size:100"`
	AccountIdentification string    `gorm:"uniqueIndex;not null;size:34"` // IBAN
	CustomerID            string    `gorm:"not null;index;size:100"`
	BalanceCents          int64     `gorm:"not null;default:0"`
	AvailableBalanceCents int64     `gorm:"not null;default:0"`
	Currency              string    `gorm:"not null;size:3;index"`
	Status                string    `gorm:"not null;size:50;index"`
	OverdraftLimitCents   int64     `gorm:"not null;default:0"`
	OverdraftEnabled      bool      `gorm:"not null;default:false"`
	OverdraftRate         float64   `gorm:"not null;default:0"`
	BalanceUpdatedAt      time.Time `gorm:"not null;default:now()"`

	// Audit fields
	CreatedAt time.Time  `gorm:"not null;default:now()"`
	UpdatedAt time.Time  `gorm:"not null;default:now()"`
	CreatedBy string     `gorm:"size:255"`
	UpdatedBy string     `gorm:"size:255"`
	Version   int        `gorm:"not null;default:1"`
	DeletedAt *time.Time `gorm:"index"`
}

// TableName overrides the default table name
func (CurrentAccountEntity) TableName() string {
	return "current_accounts"
}
