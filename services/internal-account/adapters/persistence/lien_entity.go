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

// LienEntity represents the database persistence model for liens.
type LienEntity struct {
	ID        uuid.UUID `gorm:"primaryKey"`
	AccountID uuid.UUID `gorm:"not null;index:idx_lien_account_status"`

	AmountCents    int64  `gorm:"not null;check:amount_cents > 0"`
	InstrumentCode string `gorm:"column:instrument_code;not null;size:32"`

	BucketID string `gorm:"column:bucket_id;size:255;not null;default:'';index:idx_lien_account_bucket"`
	Status   string `gorm:"not null;size:20;index:idx_lien_account_status;check:status IN ('ACTIVE','EXECUTED','TERMINATED')"`

	PaymentOrderReference string `gorm:"not null;size:255;uniqueIndex:idx_lien_payment_order"`
	TerminationReason     string `gorm:"size:1000"`

	ExpiresAt *time.Time `gorm:"index:idx_lien_expires_at"`

	ReservedQuantity  JSONBMap `gorm:"column:reserved_quantity;type:jsonb"`
	ValuedAmount      JSONBMap `gorm:"column:valued_amount;type:jsonb"`
	ValuationAnalysis JSONBMap `gorm:"column:valuation_analysis;type:jsonb"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	Version   int       `gorm:"not null;default:1"`
}

// TableName overrides the default table name.
func (LienEntity) TableName() string {
	return "lien"
}

// JSONBMap represents a JSONB column that can be null.
type JSONBMap json.RawMessage

// Value implements driver.Valuer for database writes.
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
