package persistence

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ConnectionRepository implements ports.ConnectionRepository using CockroachDB via GORM.
type ConnectionRepository struct {
	db *gorm.DB
}

// NewConnectionRepository creates a new ConnectionRepository.
func NewConnectionRepository(db *gorm.DB) *ConnectionRepository {
	return &ConnectionRepository{db: db}
}

// Upsert creates or fully replaces a provider connection.
// Uses ON CONFLICT (tenant_id, connection_id) DO UPDATE so that repeated configuration
// pushes are idempotent.
func (r *ConnectionRepository) Upsert(ctx context.Context, conn *domain.ProviderConnection) error {
	entity, err := connectionToEntity(conn)
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "connection_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"provider_name",
				"provider_type",
				"protocol",
				"base_url",
				"auth_config",
				"retry_policy",
				"rate_limit_config",
				"health_status",
				"last_health_check_at",
				"circuit_state",
				"circuit_opened_at",
				"failure_count",
				"success_count",
				"status",
				"deprecated_at",
				"updated_at",
			}),
		}).
		Create(entity).Error
}

// FindByID retrieves a provider connection by tenant and connection ID.
// Returns ports.ErrConnectionNotFound if the record does not exist.
func (r *ConnectionRepository) FindByID(ctx context.Context, tenantID string, connectionID string) (*domain.ProviderConnection, error) {
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, ports.ErrConnectionNotFound
	}
	connUUID, err := uuid.Parse(connectionID)
	if err != nil {
		return nil, ports.ErrConnectionNotFound
	}

	var entity ConnectionEntity
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND connection_id = ?", tenantUUID, connUUID).
		First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ports.ErrConnectionNotFound
	}
	if result.Error != nil {
		return nil, result.Error
	}

	return connectionFromEntity(&entity)
}

// ListByTenant retrieves all provider connections for a tenant, ordered by provider_name.
// Returns an empty slice (not an error) when tenantID is not a valid UUID, because no
// records can possibly match a malformed identifier.
func (r *ConnectionRepository) ListByTenant(ctx context.Context, tenantID string) ([]*domain.ProviderConnection, error) {
	tenantUUID, parseErr := uuid.Parse(tenantID)
	if parseErr != nil {
		return []*domain.ProviderConnection{}, nil //nolint:nilerr // malformed UUID cannot match any row; empty list is the correct result
	}

	var entities []ConnectionEntity
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantUUID).
		Order("provider_name ASC").
		Find(&entities).Error; err != nil {
		return nil, err
	}

	conns := make([]*domain.ProviderConnection, 0, len(entities))
	for i := range entities {
		conn, err := connectionFromEntity(&entities[i])
		if err != nil {
			return nil, err
		}
		conns = append(conns, conn)
	}
	return conns, nil
}

// UpdateHealth persists health and circuit-breaker fields only.
// This targeted update prevents clobbering concurrent configuration changes that may
// have updated auth_config, retry_policy, etc.
func (r *ConnectionRepository) UpdateHealth(ctx context.Context, conn *domain.ProviderConnection) error {
	tenantUUID, err := uuid.Parse(conn.TenantID)
	if err != nil {
		return ports.ErrConnectionNotFound
	}
	connUUID, err := uuid.Parse(conn.ConnectionID)
	if err != nil {
		return ports.ErrConnectionNotFound
	}

	result := r.db.WithContext(ctx).
		Model(&ConnectionEntity{}).
		Where("tenant_id = ? AND connection_id = ?", tenantUUID, connUUID).
		Updates(map[string]interface{}{
			"health_status":        healthStatusForDB(conn.HealthStatus),
			"last_health_check_at": conn.LastHealthCheckAt,
			"circuit_state":        string(conn.CircuitState),
			"circuit_opened_at":    conn.CircuitOpenedAt,
			"failure_count":        conn.FailureCount,
			"success_count":        conn.SuccessCount,
			"updated_at":           conn.UpdatedAt,
		})

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ports.ErrConnectionNotFound
	}
	return nil
}
