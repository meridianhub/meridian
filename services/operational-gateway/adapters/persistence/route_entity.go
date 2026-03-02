package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// RouteEntity is the GORM persistence model for the instruction_routes table.
// The composite primary key is (tenant_id, instruction_type).
type RouteEntity struct {
	TenantID             uuid.UUID  `gorm:"column:tenant_id;type:uuid;not null;primaryKey"`
	InstructionType      string     `gorm:"column:instruction_type;type:varchar(255);not null;primaryKey"`
	ConnectionID         uuid.UUID  `gorm:"column:connection_id;type:uuid;not null"`
	FallbackConnectionID *uuid.UUID `gorm:"column:fallback_connection_id;type:uuid"`
	OutboundMapping      string     `gorm:"column:outbound_mapping;type:varchar(255);not null;default:''"`
	InboundMapping       string     `gorm:"column:inbound_mapping;type:varchar(255);not null;default:''"`
	HTTPMethod           string     `gorm:"column:http_method;type:varchar(10);not null;default:''"`
	PathTemplate         string     `gorm:"column:path_template;type:varchar(1024);not null;default:''"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null;default:now()"`
}

// TableName returns the table name matching the migration schema.
func (RouteEntity) TableName() string {
	return "instruction_routes"
}

// routeToEntity converts a domain Route to a RouteEntity for persistence.
func routeToEntity(r *domain.Route) (*RouteEntity, error) {
	tenantUUID, err := uuid.Parse(r.TenantID)
	if err != nil {
		return nil, ports.ErrRouteNotFound
	}
	connUUID, err := uuid.Parse(r.ConnectionID)
	if err != nil {
		return nil, ports.ErrRouteNotFound
	}

	entity := &RouteEntity{
		TenantID:        tenantUUID,
		InstructionType: r.InstructionType,
		ConnectionID:    connUUID,
		OutboundMapping: r.OutboundMapping,
		InboundMapping:  r.InboundMapping,
		HTTPMethod:      r.HTTPMethod,
		PathTemplate:    r.PathTemplate,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}

	if r.FallbackConnectionID != "" {
		fallbackUUID, err := uuid.Parse(r.FallbackConnectionID)
		if err != nil {
			return nil, ports.ErrRouteNotFound
		}
		entity.FallbackConnectionID = &fallbackUUID
	}

	return entity, nil
}

// routeFromEntity converts a RouteEntity to a domain Route.
func routeFromEntity(e *RouteEntity) *domain.Route {
	r := &domain.Route{
		TenantID:        e.TenantID.String(),
		InstructionType: e.InstructionType,
		ConnectionID:    e.ConnectionID.String(),
		OutboundMapping: e.OutboundMapping,
		InboundMapping:  e.InboundMapping,
		HTTPMethod:      e.HTTPMethod,
		PathTemplate:    e.PathTemplate,
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
	if e.FallbackConnectionID != nil {
		r.FallbackConnectionID = e.FallbackConnectionID.String()
	}
	return r
}
