package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WithPartyTypeDefinitionService attaches a party type definition service to the gRPC service.
// When set, party type definition gRPC operations are enabled.
func (s *Service) WithPartyTypeDefinitionService(ptd *PartyTypeDefinitionService) *Service {
	s.partyTypeService = ptd
	return s
}

// RegisterPartyType creates a new tenant-configurable party type definition.
func (s *Service) RegisterPartyType(ctx context.Context, req *pb.RegisterPartyTypeRequest) (*pb.RegisterPartyTypeResponse, error) {
	if s.partyTypeService == nil {
		return nil, status.Error(codes.Unimplemented, "party type definition operations not configured")
	}

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "tenant context required: %v", err)
	}

	entity, err := s.partyTypeService.Register(ctx, RegisterPartyTypeInput{
		TenantID:        tenantID.String(),
		PartyType:       req.PartyType,
		AttributeSchema: req.AttributeSchema,
		ValidationCEL:   req.ValidationCel,
		EligibilityCEL:  req.EligibilityCel,
		ErrorMessageCEL: req.ErrorMessageCel,
	})
	if err != nil {
		if errors.Is(err, persistence.ErrPartyTypeDefinitionExists) {
			return nil, status.Errorf(codes.AlreadyExists, "party type definition already exists for type %q", req.PartyType)
		}
		if errors.Is(err, ErrAttributeSchemaEmpty) ||
			errors.Is(err, ErrAttributeSchemaTooBig) ||
			errors.Is(err, ErrAttributeSchemaInvalidJSON) ||
			errors.Is(err, ErrValidationCELInvalid) ||
			errors.Is(err, ErrEligibilityCELInvalid) ||
			errors.Is(err, ErrErrorMessageCELInvalid) {
			return nil, status.Errorf(codes.InvalidArgument, "invalid party type definition: %v", err)
		}
		s.logger.Error("failed to register party type definition",
			"party_type", req.PartyType,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to register party type definition: %v", err)
	}

	s.logger.Info("party type definition registered",
		"id", entity.ID.String(),
		"tenant_id", tenantID.String(),
		"party_type", entity.PartyType)

	return &pb.RegisterPartyTypeResponse{
		PartyTypeDefinition: partyTypeDefinitionToProto(entity),
	}, nil
}

// GetPartyType retrieves a party type definition by ID.
func (s *Service) GetPartyType(ctx context.Context, req *pb.GetPartyTypeRequest) (*pb.GetPartyTypeResponse, error) {
	if s.partyTypeService == nil {
		return nil, status.Error(codes.Unimplemented, "party type definition operations not configured")
	}

	if _, err := tenant.RequireFromContext(ctx); err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "tenant context required: %v", err)
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party type definition ID format: %v", err)
	}

	entity, err := s.partyTypeService.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyTypeDefinitionNotFound) {
			return nil, status.Errorf(codes.NotFound, "party type definition not found: %s", req.Id)
		}
		s.logger.Error("failed to retrieve party type definition",
			"id", req.Id,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve party type definition: %v", err)
	}

	return &pb.GetPartyTypeResponse{
		PartyTypeDefinition: partyTypeDefinitionToProto(entity),
	}, nil
}

// ListPartyTypes lists all party type definitions for the tenant.
func (s *Service) ListPartyTypes(ctx context.Context, req *pb.ListPartyTypesRequest) (*pb.ListPartyTypesResponse, error) {
	if s.partyTypeService == nil {
		return nil, status.Error(codes.Unimplemented, "party type definition operations not configured")
	}

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "tenant context required: %v", err)
	}

	entities, err := s.partyTypeService.ListByTenant(ctx, tenantID.String(), req.PartyType)
	if err != nil {
		s.logger.Error("failed to list party type definitions",
			"tenant_id", tenantID.String(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to list party type definitions: %v", err)
	}

	defs := make([]*pb.PartyTypeDefinition, len(entities))
	for i, e := range entities {
		defs[i] = partyTypeDefinitionToProto(e)
	}

	return &pb.ListPartyTypesResponse{
		PartyTypeDefinitions: defs,
	}, nil
}

// UpdatePartyType updates a party type definition.
func (s *Service) UpdatePartyType(ctx context.Context, req *pb.UpdatePartyTypeRequest) (*pb.UpdatePartyTypeResponse, error) {
	if s.partyTypeService == nil {
		return nil, status.Error(codes.Unimplemented, "party type definition operations not configured")
	}

	if _, err := tenant.RequireFromContext(ctx); err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "tenant context required: %v", err)
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party type definition ID format: %v", err)
	}

	input, err := buildPartyTypeUpdateInput(req)
	if err != nil {
		return nil, err
	}

	entity, err := s.partyTypeService.Update(ctx, id, input)
	if err != nil {
		return nil, mapPartyTypeUpdateError(s, req.Id, err)
	}

	s.logger.Info("party type definition updated",
		"id", req.Id,
		"version", entity.Version)

	return &pb.UpdatePartyTypeResponse{
		PartyTypeDefinition: partyTypeDefinitionToProto(entity),
	}, nil
}

// buildPartyTypeUpdateInput constructs the UpdatePartyTypeInput from the request, applying field mask if provided.
func buildPartyTypeUpdateInput(req *pb.UpdatePartyTypeRequest) (UpdatePartyTypeInput, error) {
	input := UpdatePartyTypeInput{
		// #nosec G115 - Version is bounded by database constraints
		Version: int64(req.Version),
	}

	if req.UpdateMask != nil && len(req.UpdateMask.Paths) > 0 {
		for _, path := range req.UpdateMask.Paths {
			switch path {
			case "attribute_schema":
				input.AttributeSchema = &req.AttributeSchema
			case "validation_cel":
				input.ValidationCEL = &req.ValidationCel
			case "eligibility_cel":
				input.EligibilityCEL = &req.EligibilityCel
			case "error_message_cel":
				input.ErrorMessageCEL = &req.ErrorMessageCel
			default:
				return UpdatePartyTypeInput{}, status.Errorf(codes.InvalidArgument, "unsupported update mask path: %q", path)
			}
		}
	} else {
		input.AttributeSchema = &req.AttributeSchema
		input.ValidationCEL = &req.ValidationCel
		input.EligibilityCEL = &req.EligibilityCel
		input.ErrorMessageCEL = &req.ErrorMessageCel
	}

	return input, nil
}

// mapPartyTypeUpdateError maps domain/persistence errors from party type updates to gRPC status errors.
func mapPartyTypeUpdateError(s *Service, reqID string, err error) error {
	if errors.Is(err, persistence.ErrPartyTypeDefinitionNotFound) {
		return status.Errorf(codes.NotFound, "party type definition not found: %s", reqID)
	}
	if errors.Is(err, persistence.ErrPartyTypeVersionConflict) {
		return status.Errorf(codes.Aborted, "version conflict: party type definition was modified by another transaction")
	}
	if errors.Is(err, ErrAttributeSchemaEmpty) ||
		errors.Is(err, ErrAttributeSchemaTooBig) ||
		errors.Is(err, ErrAttributeSchemaInvalidJSON) ||
		errors.Is(err, ErrValidationCELInvalid) ||
		errors.Is(err, ErrEligibilityCELInvalid) ||
		errors.Is(err, ErrErrorMessageCELInvalid) {
		return status.Errorf(codes.InvalidArgument, "invalid update: %v", err)
	}
	s.logger.Error("failed to update party type definition",
		"id", reqID, "error", err)
	return status.Errorf(codes.Internal, "failed to update party type definition: %v", err)
}

// partyTypeDefinitionToProto converts a persistence entity to a proto PartyTypeDefinition.
func partyTypeDefinitionToProto(e *persistence.PartyTypeDefinitionEntity) *pb.PartyTypeDefinition {
	return &pb.PartyTypeDefinition{
		Id:              e.ID.String(),
		TenantId:        e.TenantID,
		PartyType:       e.PartyType,
		AttributeSchema: e.AttributeSchema,
		ValidationCel:   e.ValidationCEL,
		EligibilityCel:  e.EligibilityCEL,
		ErrorMessageCel: e.ErrorMessageCEL,
		// #nosec G115 - Version is bounded by database constraints
		Version:   int32(e.Version),
		CreatedAt: timestamppb.New(e.CreatedAt),
		UpdatedAt: timestamppb.New(e.UpdatedAt),
	}
}
