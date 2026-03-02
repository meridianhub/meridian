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

// RouteRepository implements ports.RouteRepository using CockroachDB via GORM.
type RouteRepository struct {
	db *gorm.DB
}

// NewRouteRepository creates a new RouteRepository.
func NewRouteRepository(db *gorm.DB) *RouteRepository {
	return &RouteRepository{db: db}
}

// Upsert creates or fully replaces an instruction route.
// Uses ON CONFLICT (tenant_id, instruction_type) DO UPDATE for idempotency.
func (r *RouteRepository) Upsert(ctx context.Context, route *domain.Route) error {
	entity, err := routeToEntity(route)
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "instruction_type"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"connection_id",
				"fallback_connection_id",
				"outbound_mapping",
				"inbound_mapping",
				"http_method",
				"path_template",
				"updated_at",
			}),
		}).
		Create(entity).Error
}

// FindByInstructionType retrieves an instruction route by tenant and instruction type.
// Returns ports.ErrRouteNotFound if the record does not exist.
func (r *RouteRepository) FindByInstructionType(ctx context.Context, tenantID string, instructionType string) (*domain.Route, error) {
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, ports.ErrRouteNotFound
	}

	var entity RouteEntity
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND instruction_type = ?", tenantUUID, instructionType).
		First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ports.ErrRouteNotFound
	}
	if result.Error != nil {
		return nil, result.Error
	}

	return routeFromEntity(&entity), nil
}

// ListByTenant retrieves all instruction routes for a tenant, ordered by instruction_type.
// Returns an empty slice (not an error) when tenantID is not a valid UUID.
func (r *RouteRepository) ListByTenant(ctx context.Context, tenantID string) ([]*domain.Route, error) {
	tenantUUID, parseErr := uuid.Parse(tenantID)
	if parseErr != nil {
		return []*domain.Route{}, nil //nolint:nilerr // malformed UUID cannot match any row
	}

	var entities []RouteEntity
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantUUID).
		Order("instruction_type ASC").
		Find(&entities).Error; err != nil {
		return nil, err
	}

	routes := make([]*domain.Route, 0, len(entities))
	for i := range entities {
		routes = append(routes, routeFromEntity(&entities[i]))
	}
	return routes, nil
}
