package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrUnsupportedJSONBType is returned when Scan receives an unexpected type.
var ErrUnsupportedJSONBType = errors.New("unsupported type for JSONBMap")

// LienEntity represents the database persistence model for liens
// Optimized for database concerns: audit fields, indexes, constraints
type LienEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"primaryKey"`

	// Foreign key to account table
	AccountID uuid.UUID `gorm:"not null;index:idx_lien_account_status"`

	// Monetary amount
	AmountCents    int64  `gorm:"column:amount_cents;not null;check:amount_cents > 0"`
	InstrumentCode string `gorm:"column:instrument_code;not null;size:32"` // Instrument code (e.g. GBP, kWh)
	Dimension      string `gorm:"column:dimension;not null;size:20"`       // Asset dimension (e.g. CURRENCY, ENERGY)
	Precision      int    `gorm:"column:precision;not null;default:2"`     // Decimal places for the instrument

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

	// Valuation fields for atomic price lock (nullable for backward compatibility)
	// ReservedQuantity stores the original input before valuation (e.g., 100 kWh)
	ReservedQuantity JSONBMap `gorm:"column:reserved_quantity;type:jsonb"`
	// ValuedAmount stores the price-locked valuation result (e.g., 35.00 GBP)
	ValuedAmount JSONBMap `gorm:"column:valued_amount;type:jsonb"`
	// ValuationAnalysis stores the full valuation audit trail
	ValuationAnalysis JSONBMap `gorm:"column:valuation_analysis;type:jsonb"`

	// Audit fields
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	Version   int       `gorm:"not null;default:1"`
}

// JSONBMap represents a JSONB column that can be null.
// It implements the driver.Valuer and sql.Scanner interfaces for GORM.
type JSONBMap json.RawMessage

// Value implements driver.Valuer for database writes.
// Returns nil for SQL NULL per driver.Valuer contract.
func (j JSONBMap) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil //nolint:nilnil // driver.Valuer requires nil,nil for SQL NULL
	}
	return []byte(j), nil
}

// Scan implements sql.Scanner for database reads.
func (j *JSONBMap) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	switch v := value.(type) {
	case []byte:
		*j = JSONBMap(v)
	case string:
		*j = JSONBMap(v)
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedJSONBType, value)
	}
	return nil
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (LienEntity) TableName() string {
	return "lien"
}
