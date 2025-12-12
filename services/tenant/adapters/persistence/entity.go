// Package persistence provides PostgreSQL persistence for tenants.
package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// ErrJSONMapScanFailed indicates the JSONMap column value could not be scanned.
var ErrJSONMapScanFailed = errors.New("failed to scan JSONMap: value is neither []byte nor string")

// TenantEntity is the database representation of a tenant.
// Stored in the platform.tenants table.
type TenantEntity struct {
	ID              string     `gorm:"column:id;primaryKey"`
	DisplayName     string     `gorm:"column:display_name;not null"`
	SettlementAsset string     `gorm:"column:settlement_asset;not null"`
	Subdomain       *string    `gorm:"column:subdomain;uniqueIndex:idx_tenants_subdomain"`
	Status          string     `gorm:"column:status;not null;default:provisioning"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;autoCreateTime;index:idx_tenants_created_at,sort:desc"`
	DeprovisionedAt *time.Time `gorm:"column:deprovisioned_at"`
	Metadata        JSONMap    `gorm:"column:metadata;type:jsonb;default:'{}'"`
	Version         int        `gorm:"column:version;not null;default:1"`
	PartyID         *string    `gorm:"column:party_id;index:idx_tenants_party_id"`
	ErrorMessage    *string    `gorm:"column:error_message"`
}

// TableName returns the table name for GORM.
func (TenantEntity) TableName() string {
	return "platform.tenants"
}

// JSONMap is a custom type for JSONB columns.
type JSONMap map[string]interface{}

// Value implements the driver.Valuer interface for GORM.
func (j JSONMap) Value() (driver.Value, error) {
	if j == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(j)
}

// Scan implements the sql.Scanner interface for GORM.
func (j *JSONMap) Scan(value interface{}) error {
	if value == nil {
		*j = make(map[string]interface{})
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return ErrJSONMapScanFailed
	}

	if len(bytes) == 0 {
		*j = make(map[string]interface{})
		return nil
	}

	return json.Unmarshal(bytes, j)
}
