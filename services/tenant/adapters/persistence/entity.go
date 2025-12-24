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
// Uses singular, unqualified name per database-per-service architecture.
type TenantEntity struct {
	ID              string     `gorm:"column:id;primaryKey"`
	DisplayName     string     `gorm:"column:display_name;not null"`
	SettlementAsset string     `gorm:"column:settlement_asset;not null"`
	Subdomain       *string    `gorm:"column:subdomain;uniqueIndex:idx_tenant_subdomain"`
	Slug            *string    `gorm:"column:slug;uniqueIndex:idx_tenant_slug"`
	Status          string     `gorm:"column:status;not null;default:provisioning"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;autoCreateTime;index:idx_tenant_created_at,sort:desc"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null;autoUpdateTime"`
	DeprovisionedAt *time.Time `gorm:"column:deprovisioned_at"`
	Metadata        JSONMap    `gorm:"column:metadata;type:jsonb;default:'{}'"`
	Version         int        `gorm:"column:version;not null;default:1"`
	PartyID         *string    `gorm:"column:party_id;index:idx_tenant_party_id"`
	ErrorMessage    *string    `gorm:"column:error_message"`
}

// TableName returns the table name for GORM.
// Uses singular, unqualified name per database-per-service architecture.
func (TenantEntity) TableName() string {
	return "tenant"
}

// AuditID returns the record ID as a string for audit logging.
// Implements the audit.Auditable interface.
func (t TenantEntity) AuditID() string {
	return t.ID
}

// AuditTableName returns the table name for audit logging.
// Implements the audit.Auditable interface.
func (t TenantEntity) AuditTableName() string {
	return t.TableName()
}

// ProvisioningStatusEntity is the database representation of per-service provisioning status.
// Uses singular, unqualified name per database-per-service architecture.
type ProvisioningStatusEntity struct {
	ID               int        `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID         string     `gorm:"column:tenant_id;not null;index:idx_tenant_provisioning_status_tenant_id"`
	ServiceName      string     `gorm:"column:service_name;not null;index:idx_tenant_provisioning_status_service_name"`
	Status           string     `gorm:"column:status;not null;index:idx_tenant_provisioning_status_status"`
	MigrationVersion *string    `gorm:"column:migration_version"`
	ErrorMessage     *string    `gorm:"column:error_message"`
	StartedAt        *time.Time `gorm:"column:started_at"`
	CompletedAt      *time.Time `gorm:"column:completed_at"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;autoCreateTime"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null;autoUpdateTime"`
}

// TableName returns the table name for GORM.
// Uses singular, unqualified name per database-per-service architecture.
func (ProvisioningStatusEntity) TableName() string {
	return "tenant_provisioning_status"
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
