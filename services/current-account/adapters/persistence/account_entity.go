// Package persistence provides database persistence for the current account domain
package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidStatusHistoryScan is returned when scanning an incompatible type into StatusHistoryJSON.
var ErrInvalidStatusHistoryScan = errors.New("cannot scan into StatusHistoryJSON")

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
	InstrumentCode        string     `gorm:"column:instrument_code;type:varchar(32);not null;default:'GBP'"`      // Instrument code (e.g. GBP, kWh)
	Dimension             string     `gorm:"column:dimension;type:varchar(20);not null;default:'CURRENCY'"`       // Asset dimension (e.g. CURRENCY, ELECTRICITY)
	Status                string     `gorm:"column:status;type:varchar(20);not null;default:'active'"`
	PartyID               uuid.UUID  `gorm:"column:party_id;type:uuid;not null;index"`
	OrgPartyID            *uuid.UUID `gorm:"column:org_party_id;type:uuid"`             // NULL for personal accounts, set for org-scoped accounts
	OverdraftLimit        int64      `gorm:"column:overdraft_limit;not null;default:0"` // in smallest currency unit
	OverdraftRate         float64    `gorm:"column:overdraft_rate;type:numeric(5,4);not null;default:0"`
	ProductTypeCode       *string    `gorm:"column:product_type_code;type:varchar(50)"` // NULL for legacy accounts
	ProductTypeVersion    *int       `gorm:"column:product_type_version"`               // NULL for legacy accounts

	// Balance fields - NOT persisted to database (gorm:"-"), but kept for in-memory use.
	// Balance computation is delegated to Position Keeping service per BIAN architecture.
	// These fields are populated by the repository from domain model for backward compatibility.
	// The actual balance comes from Position Keeping service at runtime; these are placeholders
	// that allow existing code to work during the transition period.
	Balance          int64      `gorm:"-"` // in smallest currency unit (pence) - NOT PERSISTED
	AvailableBalance int64      `gorm:"-"` // after pending transactions - NOT PERSISTED
	BalanceUpdatedAt *time.Time `gorm:"-"` // NOT PERSISTED

	OpenedAt     *time.Time `gorm:"column:opened_at;index"`
	ClosedAt     *time.Time `gorm:"column:closed_at;index"`
	FreezeReason *string    `gorm:"column:freeze_reason;type:varchar(1000)"` // Reason when account is frozen

	// Status audit trail - JSONB array of status changes
	// Note: default is handled in code, not database, for GORM AutoMigrate compatibility
	StatusHistory StatusHistoryJSON `gorm:"column:status_history;type:jsonb;not null"`

	// Optimistic locking
	Version int64 `gorm:"column:version;not null;default:1"`

	// Audit fields - must match BaseModel columns from migration
	CreatedAt time.Time  `gorm:"column:created_at;not null;default:now()"`
	CreatedBy string     `gorm:"column:created_by;type:varchar(100);not null"`
	UpdatedAt time.Time  `gorm:"column:updated_at;not null;default:now()"`
	UpdatedBy string     `gorm:"column:updated_by;type:varchar(100);not null"`
	DeletedAt *time.Time `gorm:"column:deleted_at;index"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (CurrentAccountEntity) TableName() string {
	return "account"
}

// StatusHistoryEntry represents a single status change record for audit trail.
type StatusHistoryEntry struct {
	FromStatus string    `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Reason     string    `json:"reason"`
	Timestamp  time.Time `json:"timestamp"`
	ChangedBy  string    `json:"changed_by"`
}

// StatusHistoryJSON is a custom type for handling JSONB status_history column.
type StatusHistoryJSON []StatusHistoryEntry

// Value implements driver.Valuer for database writes.
func (s StatusHistoryJSON) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	return json.Marshal(s)
}

// Scan implements sql.Scanner for database reads.
func (s *StatusHistoryJSON) Scan(value interface{}) error {
	if value == nil {
		*s = StatusHistoryJSON{}
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("%w: unsupported type %T", ErrInvalidStatusHistoryScan, value)
	}

	return json.Unmarshal(bytes, s)
}
