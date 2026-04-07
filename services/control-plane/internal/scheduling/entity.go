// Package scheduling provides persistence types for tenant-level cron schedules.
package scheduling

import (
	"time"

	"github.com/google/uuid"
)

// TenantScheduleEntity is the GORM entity for the tenant_schedule table.
// This table lives in per-tenant schemas and bridges manifest scheduled: triggers
// to the CronScheduler infrastructure.
type TenantScheduleEntity struct {
	ID                uuid.UUID  `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	ScheduleName      string     `gorm:"column:schedule_name;type:varchar(128);not null;uniqueIndex:uq_tenant_schedule_name"`
	SagaName          string     `gorm:"column:saga_name;type:varchar(128);not null"`
	CronExpr          string     `gorm:"column:cron_expr;type:varchar(64);not null"`
	Enabled           bool       `gorm:"column:enabled;not null;default:true"`
	ManifestVersionID *uuid.UUID `gorm:"column:manifest_version_id;type:uuid"`
	Metadata          *string    `gorm:"column:metadata;type:jsonb"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null;autoCreateTime"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;not null;autoUpdateTime"`
}

// TableName returns the table name for GORM.
func (TenantScheduleEntity) TableName() string {
	return "tenant_schedule"
}
