// Package persistence provides GORM-based persistence implementations
// for the reconciliation service domain entities.
package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrUnsupportedJSONMapType is returned when a JSONMap Scan receives an unsupported type.
var ErrUnsupportedJSONMapType = errors.New("unsupported type for JSONMap")

// DisputeEntity is the GORM entity for the dispute table.
type DisputeEntity struct {
	ID         uuid.UUID  `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	CreatedAt  time.Time  `gorm:"not null;default:now()"`
	UpdatedAt  time.Time  `gorm:"not null;default:now()"`
	DisputeID  uuid.UUID  `gorm:"column:dispute_id;uniqueIndex:idx_dispute_dispute_id;type:uuid;not null"`
	VarianceID uuid.UUID  `gorm:"column:variance_id;index:idx_dispute_variance_id;type:uuid;not null"`
	RunID      uuid.UUID  `gorm:"column:run_id;index:idx_dispute_run_id;type:uuid;not null"`
	AccountID  string     `gorm:"column:account_id;index:idx_dispute_account_id;size:34;not null"`
	Status     string     `gorm:"column:status;index:idx_dispute_status;size:20;not null;default:OPEN"`
	Reason     string     `gorm:"column:reason;type:text;not null"`
	Resolution *string    `gorm:"column:resolution;type:text"`
	RaisedBy   string     `gorm:"column:raised_by;size:100;not null"`
	ResolvedBy *string    `gorm:"column:resolved_by;size:100"`
	ResolvedAt *time.Time `gorm:"column:resolved_at"`
	Attributes JSONMap    `gorm:"column:attributes;type:jsonb"`
}

// TableName returns the table name for the dispute entity.
func (DisputeEntity) TableName() string {
	return "dispute"
}

// JSONMap is a map[string]string that implements driver.Valuer and sql.Scanner for JSONB.
type JSONMap map[string]string

// Value implements the driver.Valuer interface for JSONMap.
func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return []byte("null"), nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSONMap: %w", err)
	}
	return b, nil
}

// Scan implements the sql.Scanner interface for JSONMap.
func (m *JSONMap) Scan(value interface{}) error {
	if value == nil {
		*m = nil
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedJSONMapType, value)
	}
	return json.Unmarshal(bytes, m)
}
