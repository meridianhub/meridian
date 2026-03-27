package handler

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
)

// UpdateDefinition modifies a DRAFT account type definition.
func (s *AccountTypeService) UpdateDefinition(ctx context.Context, req *pb.UpdateDefinitionRequest) (*pb.UpdateDefinitionResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	existing, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "UpdateDefinition", req.GetId())
	}

	defaultConversionMethodID, defaultConversionMethodVersion, parseErr := parseConversionMethodPair(
		req.GetDefaultConversionMethodId(), req.GetDefaultConversionMethodVersion())
	if parseErr != nil {
		return nil, parseErr
	}

	updates := &accounttype.Definition{
		DisplayName:                    req.GetDisplayName(),
		Description:                    req.GetDescription(),
		InstrumentCode:                 req.GetInstrumentCode(),
		DefaultSagaPrefix:              req.GetDefaultSagaPrefix(),
		DefaultConversionMethodID:      defaultConversionMethodID,
		DefaultConversionMethodVersion: defaultConversionMethodVersion,
		ValidationCEL:                  req.GetValidationCel(),
		BucketingCEL:                   req.GetBucketingCel(),
		EligibilityCEL:                 req.GetEligibilityCel(),
		AttributeSchema:                toRawJSON(req.GetAttributeSchema()),
		Attributes:                     stringMapToAnyMap(req.GetAttributes()),
	}

	if err := s.registry.UpdateDefinition(ctx, existing.Code, existing.Version, updates); err != nil {
		return nil, s.mapDomainError(ctx, err, "UpdateDefinition", req.GetId())
	}

	updated, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "UpdateDefinition", req.GetId())
	}

	s.logger.Info("account type updated",
		"code", existing.Code,
		"version", existing.Version)

	return &pb.UpdateDefinitionResponse{
		Definition: accountTypeToProto(updated),
	}, nil
}

// ActivateAccountType transitions an account type from DRAFT to ACTIVE.
func (s *AccountTypeService) ActivateAccountType(ctx context.Context, req *pb.ActivateAccountTypeRequest) (*pb.ActivateAccountTypeResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	existing, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "ActivateAccountType", req.GetId())
	}

	if err := s.registry.ActivateAccountType(ctx, existing.Code, existing.Version); err != nil {
		return nil, s.mapDomainError(ctx, err, "ActivateAccountType", req.GetId())
	}

	activated, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "ActivateAccountType", req.GetId())
	}

	s.logger.Info("account type activated",
		"code", existing.Code,
		"version", existing.Version)

	return &pb.ActivateAccountTypeResponse{
		Definition: accountTypeToProto(activated),
	}, nil
}

// DeprecateAccountType transitions an account type from ACTIVE to DEPRECATED.
func (s *AccountTypeService) DeprecateAccountType(ctx context.Context, req *pb.DeprecateAccountTypeRequest) (*pb.DeprecateAccountTypeResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	existing, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "DeprecateAccountType", req.GetId())
	}

	var successorID *uuid.UUID
	if req.GetSuccessorId() != "" {
		parsed, parseErr := uuid.Parse(req.GetSuccessorId())
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid successor_id: %v", parseErr)
		}
		successorID = &parsed
	}

	if err := s.registry.DeprecateAccountType(ctx, existing.Code, existing.Version, successorID); err != nil {
		return nil, s.mapDomainError(ctx, err, "DeprecateAccountType", req.GetId())
	}

	deprecated, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "DeprecateAccountType", req.GetId())
	}

	s.logger.Info("account type deprecated",
		"code", existing.Code,
		"version", existing.Version)

	return &pb.DeprecateAccountTypeResponse{
		Definition: accountTypeToProto(deprecated),
	}, nil
}
