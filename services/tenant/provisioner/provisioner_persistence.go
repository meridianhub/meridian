package provisioner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dbpkg "github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/gorm"
)

// bypassCtx wraps the context with tenant guard bypass.
// The provisioner operates at platform level — all DB operations bypass tenant scoping.
func bypassCtx(ctx context.Context) context.Context {
	return dbpkg.WithTenantGuardBypass(ctx)
}

// provisioningEntity is the database entity for the tenant_provisioning table.
type provisioningEntity struct {
	TenantID        string     `gorm:"column:tenant_id;primaryKey"`
	State           string     `gorm:"column:state"`
	ServiceSchemas  string     `gorm:"column:service_schemas;type:jsonb"`
	ErrorMessage    *string    `gorm:"column:error_message"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	DeprovisionedAt *time.Time `gorm:"column:deprovisioned_at"`
	Version         int        `gorm:"column:version"`
}

// TableName specifies the table name for GORM.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (provisioningEntity) TableName() string {
	return "tenant_provisioning"
}

// getProvisioningStatusLocked retrieves the status without acquiring the mutex.
// Caller must hold the mutex.
func (p *PostgresProvisioner) getProvisioningStatusLocked(ctx context.Context, tenantID tenant.TenantID) (*ProvisioningStatus, error) {
	var entity provisioningEntity
	result := p.platformDB.WithContext(bypassCtx(ctx)).Where("tenant_id = ?", tenantID.String()).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrProvisioningStatusNotFound
	}
	if result.Error != nil {
		return nil, result.Error
	}

	return p.entityToStatus(tenantID, &entity)
}

// saveProvisioningStatus saves or updates the provisioning status using upsert.
func (p *PostgresProvisioner) saveProvisioningStatus(ctx context.Context, status *ProvisioningStatus) error {
	entity, err := p.statusToEntity(status)
	if err != nil {
		return err
	}

	// Use PostgreSQL UPSERT (ON CONFLICT DO UPDATE)
	// This is atomic and handles both insert and update cases
	// Uses unqualified table name (tenant_provisioning) for search_path routing
	upsertSQL := `
		INSERT INTO tenant_provisioning
			(tenant_id, state, service_schemas, error_message, created_at, updated_at, deprovisioned_at, version)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT (tenant_id) DO UPDATE SET
			state = EXCLUDED.state,
			service_schemas = EXCLUDED.service_schemas,
			error_message = EXCLUDED.error_message,
			updated_at = EXCLUDED.updated_at,
			deprovisioned_at = EXCLUDED.deprovisioned_at,
			version = tenant_provisioning.version + 1
	`

	return p.platformDB.WithContext(bypassCtx(ctx)).Exec(
		upsertSQL,
		entity.TenantID,
		entity.State,
		entity.ServiceSchemas,
		entity.ErrorMessage,
		entity.CreatedAt,
		entity.UpdatedAt,
		entity.DeprovisionedAt,
	).Error
}

// deleteProvisioningStatus removes the provisioning status record.
func (p *PostgresProvisioner) deleteProvisioningStatus(ctx context.Context, tenantID tenant.TenantID) error {
	return p.platformDB.WithContext(bypassCtx(ctx)).
		Where("tenant_id = ?", tenantID.String()).
		Delete(&provisioningEntity{}).Error
}

// entityToStatus converts database entity to domain model.
func (p *PostgresProvisioner) entityToStatus(tenantID tenant.TenantID, entity *provisioningEntity) (*ProvisioningStatus, error) {
	var services []ServiceSchemaStatus
	if entity.ServiceSchemas != "" && entity.ServiceSchemas != "[]" {
		if err := json.Unmarshal([]byte(entity.ServiceSchemas), &services); err != nil {
			return nil, fmt.Errorf("unmarshal service_schemas: %w", err)
		}
	}

	status := &ProvisioningStatus{
		TenantID:        tenantID,
		State:           ProvisioningState(entity.State),
		Services:        services,
		CreatedAt:       entity.CreatedAt,
		UpdatedAt:       entity.UpdatedAt,
		DeprovisionedAt: entity.DeprovisionedAt,
	}

	if entity.ErrorMessage != nil {
		status.ErrorMessage = *entity.ErrorMessage
	}

	return status, nil
}

// statusToEntity converts domain model to database entity.
func (p *PostgresProvisioner) statusToEntity(status *ProvisioningStatus) (*provisioningEntity, error) {
	servicesJSON, err := json.Marshal(status.Services)
	if err != nil {
		return nil, fmt.Errorf("marshal services: %w", err)
	}

	entity := &provisioningEntity{
		TenantID:        status.TenantID.String(),
		State:           string(status.State),
		ServiceSchemas:  string(servicesJSON),
		CreatedAt:       status.CreatedAt,
		UpdatedAt:       status.UpdatedAt,
		DeprovisionedAt: status.DeprovisionedAt,
		Version:         1, // Will be incremented during update
	}

	if status.ErrorMessage != "" {
		entity.ErrorMessage = &status.ErrorMessage
	}

	return entity, nil
}

// createInitialServiceStatuses creates the initial service status list.
func (p *PostgresProvisioner) createInitialServiceStatuses(tenantID tenant.TenantID) []ServiceSchemaStatus {
	statuses := make([]ServiceSchemaStatus, len(p.config.Services))
	schemaName := tenantID.SchemaName()

	for i, svc := range p.config.Services {
		statuses[i] = ServiceSchemaStatus{
			ServiceName: svc.Name,
			SchemaName:  schemaName,
			State:       ServiceStatePending,
		}
	}

	return statuses
}
