package persistence

import (
	"context"
	"strings"

	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
// Returns an InvalidArgument error for payment.* instruction types — these must be
// dispatched via the financial-gateway, not the operational-gateway.
// Returns ports.ErrRouteNotFound if no matching route is configured for allowed types.
func (r *DBRouteResolver) Resolve(ctx context.Context, tenantID string, instructionType string) (*ports.InstructionRoute, error) {
	if strings.HasPrefix(instructionType, "payment.") {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"payment instructions must use financial-gateway, not operational-gateway (instruction_type: %q)",
			instructionType,
		)
	}

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
