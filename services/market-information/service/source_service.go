// Package service provides gRPC service implementations for the Market Information service.
// BIAN Service Domain: Market Information Management
//
// This file implements the DataSource-related gRPC methods for the MarketInformationService.
package service

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// RegisterDataSource creates a new data source.
// Returns ALREADY_EXISTS if code exists.
// Returns INVALID_ARGUMENT if validation fails.
func (s *Server) RegisterDataSource(ctx context.Context, req *pb.RegisterDataSourceRequest) (*pb.RegisterDataSourceResponse, error) {
	// Validate trust_level range (proto validation already covers 0-100, but belt-and-suspenders)
	if req.TrustLevel < 0 || req.TrustLevel > 100 {
		s.logger.Warn("invalid trust level",
			"code", req.Code,
			"trust_level", req.TrustLevel)
		return nil, status.Errorf(codes.InvalidArgument, "trust_level must be between 0 and 100, got %d", req.TrustLevel)
	}

	// Create domain entity using NewDataSource constructor
	// Default to SourceTypeAPI since proto doesn't include source_type
	source, err := domain.NewDataSource(
		req.Code,
		req.Name,
		req.Description,
		domain.SourceTypeAPI,
		int(req.TrustLevel),
	)
	if err != nil {
		s.logger.Warn("failed to create data source entity",
			"code", req.Code,
			"error", err)
		return nil, s.mapSourceDomainError(err, "RegisterDataSource", req.Code)
	}

	// Persist the data source
	if err := s.sourceRepo.Save(ctx, source); err != nil {
		return nil, s.mapSourceDomainError(err, "RegisterDataSource", req.Code)
	}

	s.logger.Info("data source registered",
		"code", source.Code(),
		"id", source.ID().String(),
		"trust_level", source.TrustLevel())

	return &pb.RegisterDataSourceResponse{
		Source: domainSourceToProto(source),
	}, nil
}

// UpdateDataSource modifies an existing data source.
// Returns NOT_FOUND if data source doesn't exist.
// Returns INVALID_ARGUMENT if validation fails.
func (s *Server) UpdateDataSource(ctx context.Context, req *pb.UpdateDataSourceRequest) (*pb.UpdateDataSourceResponse, error) {
	// Validate trust_level range if provided (proto allows 0, which is a valid value)
	if req.TrustLevel < 0 || req.TrustLevel > 100 {
		s.logger.Warn("invalid trust level",
			"code", req.Code,
			"trust_level", req.TrustLevel)
		return nil, status.Errorf(codes.InvalidArgument, "trust_level must be between 0 and 100, got %d", req.TrustLevel)
	}

	// Retrieve existing source
	existing, err := s.sourceRepo.FindByCode(ctx, req.Code)
	if err != nil {
		return nil, s.mapSourceDomainError(err, "UpdateDataSource", req.Code)
	}

	// Build updated source using builder pattern to preserve existing values
	// Only update fields that are provided in the request
	builder := domain.NewDataSourceBuilder().
		WithID(existing.ID()).
		WithCode(existing.Code()).
		WithSourceType(existing.SourceType()).
		WithIsActive(existing.IsActive()).
		WithCreatedAt(existing.CreatedAt()).
		WithUpdatedAt(time.Now())

	// Update name if provided, otherwise keep existing
	if req.Name != "" {
		builder.WithName(req.Name)
	} else {
		builder.WithName(existing.Name())
	}

	// Update description (can be set to empty string intentionally)
	// We always use the request value for description to allow clearing
	builder.WithDescription(req.Description)

	// Update trust level - proto int32 default is 0, which is valid
	// Always use the request value as it's validated
	builder.WithTrustLevel(int(req.TrustLevel))

	updated := builder.Build()

	// Persist the updated source
	if err := s.sourceRepo.Save(ctx, updated); err != nil {
		return nil, s.mapSourceDomainError(err, "UpdateDataSource", req.Code)
	}

	s.logger.Info("data source updated",
		"code", updated.Code(),
		"id", updated.ID().String(),
		"trust_level", updated.TrustLevel())

	return &pb.UpdateDataSourceResponse{
		Source: domainSourceToProto(updated),
	}, nil
}

// DeactivateDataSource marks a data source as inactive (soft delete).
// Returns NOT_FOUND if data source doesn't exist.
func (s *Server) DeactivateDataSource(ctx context.Context, req *pb.DeactivateDataSourceRequest) (*pb.DeactivateDataSourceResponse, error) {
	// Retrieve existing source
	existing, err := s.sourceRepo.FindByCode(ctx, req.Code)
	if err != nil {
		return nil, s.mapSourceDomainError(err, "DeactivateDataSource", req.Code)
	}

	// Check if already inactive (idempotent operation)
	if !existing.IsActive() {
		s.logger.Debug("data source already inactive",
			"code", req.Code,
			"id", existing.ID().String())
		return &pb.DeactivateDataSourceResponse{
			Source: domainSourceToProto(existing),
		}, nil
	}

	// Build deactivated source
	deactivated := domain.NewDataSourceBuilder().
		WithID(existing.ID()).
		WithCode(existing.Code()).
		WithName(existing.Name()).
		WithDescription(existing.Description()).
		WithSourceType(existing.SourceType()).
		WithTrustLevel(existing.TrustLevel()).
		WithIsActive(false).
		WithCreatedAt(existing.CreatedAt()).
		WithUpdatedAt(time.Now()).
		Build()

	// Persist the deactivated source
	if err := s.sourceRepo.Save(ctx, deactivated); err != nil {
		return nil, s.mapSourceDomainError(err, "DeactivateDataSource", req.Code)
	}

	s.logger.Info("data source deactivated",
		"code", deactivated.Code(),
		"id", deactivated.ID().String())

	return &pb.DeactivateDataSourceResponse{
		Source: domainSourceToProto(deactivated),
	}, nil
}

// ListDataSources returns data sources matching the filter criteria.
func (s *Server) ListDataSources(ctx context.Context, req *pb.ListDataSourcesRequest) (*pb.ListDataSourcesResponse, error) {
	// Delegate to repository with active/inactive filter
	sources, err := s.sourceRepo.List(ctx, req.ActiveOnly)
	if err != nil {
		s.logger.Error("failed to list data sources",
			"active_only", req.ActiveOnly,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to list data sources: %v", err)
	}

	// Apply pagination
	pageSize := int(req.PageSize)
	if pageSize == 0 {
		pageSize = 50 // Default page size
	}
	if pageSize > 100 {
		pageSize = 100 // Max page size from proto validation
	}

	// TODO: Implement proper cursor-based pagination
	if len(sources) > pageSize {
		sources = sources[:pageSize]
	}

	// Convert to proto messages
	pbSources := make([]*pb.DataSource, len(sources))
	for i, source := range sources {
		pbSources[i] = domainSourceToProto(source)
	}

	s.logger.Debug("listed data sources",
		"active_only", req.ActiveOnly,
		"count", len(pbSources))

	return &pb.ListDataSourcesResponse{
		Sources:       pbSources,
		NextPageToken: "", // TODO: Implement pagination token
	}, nil
}

// mapSourceDomainError converts domain errors to appropriate gRPC status codes.
func (s *Server) mapSourceDomainError(err error, operation, code string) error {
	switch {
	case errors.Is(err, domain.ErrDataSourceNotFound):
		s.logger.Warn("data source not found",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.NotFound, "data source not found: %s", code)

	case errors.Is(err, domain.ErrDuplicateDataSourceCode):
		s.logger.Warn("data source code already exists",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.AlreadyExists, "data source already exists: %s", code)

	case errors.Is(err, domain.ErrDataSourceCodeRequired):
		s.logger.Warn("data source code required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "data source code is required")

	case errors.Is(err, domain.ErrDataSourceNameRequired):
		s.logger.Warn("data source name required",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.InvalidArgument, "data source name is required")

	case errors.Is(err, domain.ErrInvalidSourceType):
		s.logger.Warn("invalid source type",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.InvalidArgument, "invalid source type")

	case errors.Is(err, domain.ErrInvalidTrustLevel):
		s.logger.Warn("invalid trust level",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.InvalidArgument, "trust level must be between 0 and 100")

	default:
		s.logger.Error("internal error",
			"operation", operation,
			"code", code,
			"error", err)
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// domainSourceToProto converts a domain DataSource to proto DataSource.
func domainSourceToProto(source domain.DataSource) *pb.DataSource {
	pbSource := &pb.DataSource{
		Id:          source.ID().String(),
		Code:        source.Code(),
		Name:        source.Name(),
		Description: source.Description(),
		TrustLevel:  int32(source.TrustLevel()),
		IsActive:    source.IsActive(),
		CreatedAt:   timestamppb.New(source.CreatedAt()),
	}

	// Only set UpdatedAt if it's different from CreatedAt (indicates an update occurred)
	if !source.UpdatedAt().Equal(source.CreatedAt()) {
		pbSource.UpdatedAt = timestamppb.New(source.UpdatedAt())
	}

	return pbSource
}
