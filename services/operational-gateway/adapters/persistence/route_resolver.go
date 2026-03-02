package persistence

import (
	"context"

	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// DBRouteResolver implements ports.RouteResolver by looking up routes from the database
// via RouteRepository.
type DBRouteResolver struct {
	routeRepo ports.RouteRepository
}

// NewDBRouteResolver creates a new DBRouteResolver backed by the given RouteRepository.
func NewDBRouteResolver(routeRepo ports.RouteRepository) *DBRouteResolver {
	return &DBRouteResolver{routeRepo: routeRepo}
}

// Resolve looks up the InstructionRoute for the given tenant and instruction type.
// Returns ports.ErrRouteNotFound if no matching route is configured.
func (r *DBRouteResolver) Resolve(ctx context.Context, tenantID string, instructionType string) (*ports.InstructionRoute, error) {
	route, err := r.routeRepo.FindByInstructionType(ctx, tenantID, instructionType)
	if err != nil {
		return nil, err
	}

	return &ports.InstructionRoute{
		InstructionType: route.InstructionType,
		HTTPMethod:      route.HTTPMethod,
		PathTemplate:    route.PathTemplate,
		OutboundMapping: route.OutboundMapping,
		InboundMapping:  route.InboundMapping,
	}, nil
}
