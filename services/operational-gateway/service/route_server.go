package service

import (
	"context"
	"errors"
	"log/slog"
	"os"

	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// InstructionRouteService implements InstructionRouteServiceServer.
type InstructionRouteService struct {
	opgatewayv1.UnimplementedInstructionRouteServiceServer
	routeRepo      ports.RouteRepository
	connectionRepo ports.ConnectionRepository
	logger         *slog.Logger
}

// ErrRouteRepoNil is returned when the route repository is nil.
var ErrRouteRepoNil = errors.New("route repository cannot be nil")

// NewInstructionRouteService creates a new InstructionRouteService.
func NewInstructionRouteService(
	routeRepo ports.RouteRepository,
	connectionRepo ports.ConnectionRepository,
	logger *slog.Logger,
) (*InstructionRouteService, error) {
	if routeRepo == nil {
		return nil, ErrRouteRepoNil
	}
	if connectionRepo == nil {
		return nil, ErrConnectionRepoNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &InstructionRouteService{
		routeRepo:      routeRepo,
		connectionRepo: connectionRepo,
		logger:         logger,
	}, nil
}

// UpsertRoute creates or replaces an instruction route for the authenticated tenant.
func (s *InstructionRouteService) UpsertRoute(
	ctx context.Context,
	req *opgatewayv1.UpsertRouteRequest,
) (*opgatewayv1.UpsertRouteResponse, error) {
	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	if req.InstructionType == "" {
		return nil, status.Error(codes.InvalidArgument, "instruction_type is required")
	}
	if req.ConnectionId == "" {
		return nil, status.Error(codes.InvalidArgument, "connection_id is required")
	}

	tenantIDStr := tenantIDToUUID(tid)

	// Verify the connection exists and belongs to the tenant.
	if _, findErr := s.connectionRepo.FindByID(ctx, tenantIDStr, req.ConnectionId); findErr != nil {
		if errors.Is(findErr, ports.ErrConnectionNotFound) {
			return nil, status.Errorf(codes.NotFound, "connection not found: %s", req.ConnectionId)
		}
		s.logger.Error("failed to verify connection", "error", findErr)
		return nil, status.Error(codes.Internal, "failed to verify connection")
	}

	// Verify the fallback connection if provided.
	if req.FallbackConnectionId != "" {
		if _, findErr := s.connectionRepo.FindByID(ctx, tenantIDStr, req.FallbackConnectionId); findErr != nil {
			if errors.Is(findErr, ports.ErrConnectionNotFound) {
				return nil, status.Errorf(codes.NotFound, "fallback connection not found: %s", req.FallbackConnectionId)
			}
			s.logger.Error("failed to verify fallback connection", "error", findErr)
			return nil, status.Error(codes.Internal, "failed to verify fallback connection")
		}
	}

	route, err := domain.NewRoute(tenantIDStr, req.InstructionType, req.ConnectionId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid route: %v", err)
	}
	route.FallbackConnectionID = req.FallbackConnectionId
	route.OutboundMapping = req.OutboundMapping
	route.InboundMapping = req.InboundMapping
	route.HTTPMethod = req.HttpMethod
	route.PathTemplate = req.PathTemplate

	if err := s.routeRepo.Upsert(ctx, route); err != nil {
		s.logger.Error("failed to upsert instruction route", "error", err)
		return nil, status.Error(codes.Internal, "failed to upsert route")
	}

	return &opgatewayv1.UpsertRouteResponse{
		Route: routeToProto(route),
	}, nil
}

// GetRoute retrieves an instruction route by instruction_type.
func (s *InstructionRouteService) GetRoute(
	ctx context.Context,
	req *opgatewayv1.GetRouteRequest,
) (*opgatewayv1.GetRouteResponse, error) {
	if req.InstructionType == "" {
		return nil, status.Error(codes.InvalidArgument, "instruction_type is required")
	}

	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	route, err := s.routeRepo.FindByInstructionType(ctx, tenantIDToUUID(tid), req.InstructionType)
	if err != nil {
		if errors.Is(err, ports.ErrRouteNotFound) {
			return nil, status.Errorf(codes.NotFound, "route not found: %s", req.InstructionType)
		}
		s.logger.Error("failed to retrieve route", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve route")
	}

	return &opgatewayv1.GetRouteResponse{
		Route: routeToProto(route),
	}, nil
}

// ListRoutes returns all instruction routes for the authenticated tenant.
func (s *InstructionRouteService) ListRoutes(
	ctx context.Context,
	_ *opgatewayv1.ListRoutesRequest,
) (*opgatewayv1.ListRoutesResponse, error) {
	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	routes, err := s.routeRepo.ListByTenant(ctx, tenantIDToUUID(tid))
	if err != nil {
		s.logger.Error("failed to list routes", "error", err)
		return nil, status.Error(codes.Internal, "failed to list routes")
	}

	protoRoutes := make([]*opgatewayv1.InstructionRoute, 0, len(routes))
	for _, r := range routes {
		protoRoutes = append(protoRoutes, routeToProto(r))
	}

	return &opgatewayv1.ListRoutesResponse{
		Routes: protoRoutes,
	}, nil
}

// DeprecateRoute transitions an instruction route from ACTIVE to DEPRECATED.
func (s *InstructionRouteService) DeprecateRoute(
	ctx context.Context,
	req *opgatewayv1.DeprecateRouteRequest,
) (*opgatewayv1.DeprecateRouteResponse, error) {
	if req.InstructionType == "" {
		return nil, status.Error(codes.InvalidArgument, "instruction_type is required")
	}

	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	route, err := s.routeRepo.FindByInstructionType(ctx, tenantIDToUUID(tid), req.InstructionType)
	if err != nil {
		if errors.Is(err, ports.ErrRouteNotFound) {
			return nil, status.Errorf(codes.NotFound, "route not found: %s", req.InstructionType)
		}
		s.logger.Error("failed to retrieve route for deprecation", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve route")
	}

	if err := route.Deprecate(); err != nil {
		if errors.Is(err, domain.ErrRouteNotActive) {
			return nil, status.Errorf(codes.FailedPrecondition, "route is not in ACTIVE status: %s", req.InstructionType)
		}
		return nil, status.Errorf(codes.Internal, "failed to deprecate route: %v", err)
	}

	if err := s.routeRepo.Upsert(ctx, route); err != nil {
		s.logger.Error("failed to persist deprecated route", "error", err)
		return nil, status.Error(codes.Internal, "failed to persist deprecated route")
	}

	return &opgatewayv1.DeprecateRouteResponse{
		Route: routeToProto(route),
	}, nil
}

// routeToProto converts a domain Route to its proto representation.
func routeToProto(r *domain.Route) *opgatewayv1.InstructionRoute {
	p := &opgatewayv1.InstructionRoute{
		InstructionType:      r.InstructionType,
		ConnectionId:         r.ConnectionID,
		FallbackConnectionId: r.FallbackConnectionID,
		OutboundMapping:      r.OutboundMapping,
		InboundMapping:       r.InboundMapping,
		HttpMethod:           r.HTTPMethod,
		PathTemplate:         r.PathTemplate,
		Status:               domainToProtoRouteStatus(r.Status),
		CreatedAt:            timestamppb.New(r.CreatedAt),
		UpdatedAt:            timestamppb.New(r.UpdatedAt),
	}
	if r.DeprecatedAt != nil {
		p.DeprecatedAt = timestamppb.New(*r.DeprecatedAt)
	}
	return p
}
