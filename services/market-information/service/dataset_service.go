// Package service provides gRPC service implementations for the Market Information service.
// BIAN Service Domain: Market Information Management
//
// This file implements the DataSet-related gRPC methods for the MarketInformationService.
package service

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// RegisterDataSet creates a new data set definition in DRAFT status.
// Returns ALREADY_EXISTS if code already exists.
// Returns INVALID_ARGUMENT if CEL expressions fail compilation.
func (s *Server) RegisterDataSet(ctx context.Context, req *pb.RegisterDataSetRequest) (*pb.RegisterDataSetResponse, error) {
	// Check if dataset with this code already exists
	exists, err := s.dataSetRepo.ExistsByCode(ctx, req.Code)
	if err != nil {
		s.logger.Error("failed to check dataset existence",
			"code", req.Code,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to check dataset existence: %v", err)
	}
	if exists {
		s.logger.Warn("dataset code already exists",
			"code", req.Code)
		return nil, status.Errorf(codes.AlreadyExists, "dataset already exists: %s", req.Code)
	}

	// Validate CEL expressions if validator is configured
	if s.celValidator != nil {
		if err := s.validateCelExpressions(
			req.ValidationExpression,
			req.ResolutionKeyExpression,
			req.ErrorMessageExpression,
		); err != nil {
			return nil, err
		}
	}

	// Map proto category to domain category
	domainCategory, err := protoCategoryToDomain(req.Category)
	if err != nil {
		s.logger.Warn("invalid data category",
			"code", req.Code,
			"category", req.Category)
		return nil, status.Errorf(codes.InvalidArgument, "invalid data category: %v", err)
	}

	// Create domain entity using NewDataSetDefinition constructor
	// The constructor creates the entity in DRAFT status with version 1
	dataset, err := domain.NewDataSetDefinition(
		req.Code,
		req.DisplayName,
		req.Description,
		domainCategory,
		req.ValidationExpression,
		req.ResolutionKeyExpression,
		req.ErrorMessageExpression,
	)
	if err != nil {
		s.logger.Warn("failed to create dataset entity",
			"code", req.Code,
			"error", err)
		return nil, s.mapDataSetDomainError(err, "RegisterDataSet", req.Code)
	}

	// Persist the dataset
	if err := s.dataSetRepo.Save(ctx, dataset); err != nil {
		return nil, s.mapDataSetDomainError(err, "RegisterDataSet", req.Code)
	}

	s.logger.Info("dataset registered",
		"code", dataset.Code(),
		"id", dataset.ID().String(),
		"status", dataset.Status())

	return &pb.RegisterDataSetResponse{
		Dataset: domainDataSetToProto(dataset),
	}, nil
}

// UpdateDataSet modifies a DRAFT data set definition.
// Returns NOT_FOUND if data set doesn't exist.
// Returns FAILED_PRECONDITION if not in DRAFT status.
// Returns INVALID_ARGUMENT if CEL expressions fail compilation.
func (s *Server) UpdateDataSet(ctx context.Context, req *pb.UpdateDataSetRequest) (*pb.UpdateDataSetResponse, error) {
	// Retrieve existing dataset by code and version
	existing, err := s.dataSetRepo.FindByCodeAndVersion(ctx, req.Code, int(req.Version))
	if err != nil {
		return nil, s.mapDataSetDomainError(err, "UpdateDataSet", req.Code)
	}

	// Enforce DRAFT-only updates
	if existing.Status() != domain.DataSetStatusDraft {
		s.logger.Warn("cannot update non-draft dataset",
			"code", req.Code,
			"version", req.Version,
			"status", existing.Status())
		return nil, status.Errorf(codes.FailedPrecondition,
			"dataset must be in DRAFT status to update, current status: %s", existing.Status())
	}

	// Track if any CEL expressions changed
	validationChanged := req.ValidationExpression != "" && req.ValidationExpression != existing.ValidationExpression()
	resolutionKeyChanged := req.ResolutionKeyExpression != "" && req.ResolutionKeyExpression != existing.ResolutionKeyExpression()
	errorMessageChanged := req.ErrorMessageExpression != "" && req.ErrorMessageExpression != existing.ErrorMessageExpression()

	// Validate CEL expressions if any changed and validator is configured
	if s.celValidator != nil && (validationChanged || resolutionKeyChanged || errorMessageChanged) {
		// Use new values if provided, otherwise use existing
		validationExpr := existing.ValidationExpression()
		if req.ValidationExpression != "" {
			validationExpr = req.ValidationExpression
		}
		resolutionKeyExpr := existing.ResolutionKeyExpression()
		if req.ResolutionKeyExpression != "" {
			resolutionKeyExpr = req.ResolutionKeyExpression
		}
		errorMessageExpr := existing.ErrorMessageExpression()
		if req.ErrorMessageExpression != "" {
			errorMessageExpr = req.ErrorMessageExpression
		}

		if err := s.validateCelExpressions(validationExpr, resolutionKeyExpr, errorMessageExpr); err != nil {
			return nil, err
		}
	}

	// Apply updates using domain methods
	updated := existing

	// Update description if provided (allow clearing with empty string check)
	if req.Description != existing.Description() {
		updated, err = updated.UpdateDescription(req.Description)
		if err != nil {
			return nil, s.mapDataSetDomainError(err, "UpdateDataSet", req.Code)
		}
	}

	// Update CEL expressions if provided
	if validationChanged {
		updated, err = updated.UpdateValidationExpression(req.ValidationExpression)
		if err != nil {
			return nil, s.mapDataSetDomainError(err, "UpdateDataSet", req.Code)
		}
	}

	if resolutionKeyChanged {
		updated, err = updated.UpdateResolutionKeyExpression(req.ResolutionKeyExpression)
		if err != nil {
			return nil, s.mapDataSetDomainError(err, "UpdateDataSet", req.Code)
		}
	}

	if errorMessageChanged {
		updated, err = updated.UpdateErrorMessageExpression(req.ErrorMessageExpression)
		if err != nil {
			return nil, s.mapDataSetDomainError(err, "UpdateDataSet", req.Code)
		}
	}

	// Persist the updated dataset
	if err := s.dataSetRepo.Save(ctx, updated); err != nil {
		return nil, s.mapDataSetDomainError(err, "UpdateDataSet", req.Code)
	}

	s.logger.Info("dataset updated",
		"code", updated.Code(),
		"version", updated.Version())

	return &pb.UpdateDataSetResponse{
		Dataset: domainDataSetToProto(updated),
	}, nil
}

// ActivateDataSet transitions a data set from DRAFT to ACTIVE.
// Returns NOT_FOUND if data set doesn't exist.
// Returns FAILED_PRECONDITION if not in DRAFT status.
// Returns INVALID_ARGUMENT if CEL expressions fail validation.
func (s *Server) ActivateDataSet(ctx context.Context, req *pb.ActivateDataSetRequest) (*pb.ActivateDataSetResponse, error) {
	// Retrieve existing dataset
	existing, err := s.dataSetRepo.FindByCodeAndVersion(ctx, req.Code, int(req.Version))
	if err != nil {
		return nil, s.mapDataSetDomainError(err, "ActivateDataSet", req.Code)
	}

	// Perform full CEL validation before activation (compile all expressions)
	if s.celValidator != nil {
		if err := s.validateCelExpressions(
			existing.ValidationExpression(),
			existing.ResolutionKeyExpression(),
			existing.ErrorMessageExpression(),
		); err != nil {
			s.logger.Warn("CEL validation failed during activation",
				"code", req.Code,
				"version", req.Version,
				"error", err)
			return nil, err
		}
	}

	// Transition DRAFT -> ACTIVE using domain method
	activated, err := existing.ActivateDataSet()
	if err != nil {
		s.logger.Warn("failed to activate dataset",
			"code", req.Code,
			"version", req.Version,
			"current_status", existing.Status(),
			"error", err)
		return nil, s.mapDataSetDomainError(err, "ActivateDataSet", req.Code)
	}

	// Persist the activated dataset
	if err := s.dataSetRepo.Save(ctx, activated); err != nil {
		return nil, s.mapDataSetDomainError(err, "ActivateDataSet", req.Code)
	}

	s.logger.Info("dataset activated",
		"code", activated.Code(),
		"version", activated.Version(),
		"status", activated.Status())

	return &pb.ActivateDataSetResponse{
		Dataset: domainDataSetToProto(activated),
	}, nil
}

// DeprecateDataSet transitions a data set from ACTIVE to DEPRECATED.
// Returns NOT_FOUND if data set doesn't exist.
// Returns FAILED_PRECONDITION if not in ACTIVE status.
func (s *Server) DeprecateDataSet(ctx context.Context, req *pb.DeprecateDataSetRequest) (*pb.DeprecateDataSetResponse, error) {
	// Retrieve existing dataset
	existing, err := s.dataSetRepo.FindByCodeAndVersion(ctx, req.Code, int(req.Version))
	if err != nil {
		return nil, s.mapDataSetDomainError(err, "DeprecateDataSet", req.Code)
	}

	// Transition to DEPRECATED using domain method
	// Domain validates the transition (DRAFT->DEPRECATED and ACTIVE->DEPRECATED are both valid)
	deprecated, err := existing.DeprecateDataSet()
	if err != nil {
		s.logger.Warn("failed to deprecate dataset",
			"code", req.Code,
			"version", req.Version,
			"current_status", existing.Status(),
			"error", err)
		return nil, s.mapDataSetDomainError(err, "DeprecateDataSet", req.Code)
	}

	// Persist the deprecated dataset
	if err := s.dataSetRepo.Save(ctx, deprecated); err != nil {
		return nil, s.mapDataSetDomainError(err, "DeprecateDataSet", req.Code)
	}

	s.logger.Info("dataset deprecated",
		"code", deprecated.Code(),
		"version", deprecated.Version(),
		"status", deprecated.Status())

	return &pb.DeprecateDataSetResponse{
		Dataset: domainDataSetToProto(deprecated),
	}, nil
}

// RetrieveDataSet fetches a specific data set by code and version.
// If version is 0, returns the latest (current) version.
// Returns NOT_FOUND if data set doesn't exist.
func (s *Server) RetrieveDataSet(ctx context.Context, req *pb.RetrieveDataSetRequest) (*pb.RetrieveDataSetResponse, error) {
	var dataset domain.DataSetDefinition
	var err error

	if req.Version == 0 {
		// Version 0 means get the latest (current) version
		dataset, err = s.dataSetRepo.FindByCode(ctx, req.Code)
	} else {
		// Get specific version
		dataset, err = s.dataSetRepo.FindByCodeAndVersion(ctx, req.Code, int(req.Version))
	}

	if err != nil {
		return nil, s.mapDataSetDomainError(err, "RetrieveDataSet", req.Code)
	}

	s.logger.Debug("dataset retrieved",
		"code", dataset.Code(),
		"version", dataset.Version())

	return &pb.RetrieveDataSetResponse{
		Dataset: domainDataSetToProto(dataset),
	}, nil
}

// ListDataSets returns data sets matching the filter criteria.
// Supports filtering by status, category, and pagination.
func (s *Server) ListDataSets(ctx context.Context, req *pb.ListDataSetsRequest) (*pb.ListDataSetsResponse, error) {
	// Build domain filters from proto request
	filters := domain.DataSetFilters{}

	// Apply status filter if specified (not UNSPECIFIED)
	if req.StatusFilter != pb.DataSetStatus_DATA_SET_STATUS_UNSPECIFIED {
		domainStatus, err := protoStatusToDomain(req.StatusFilter)
		if err != nil {
			s.logger.Warn("invalid status filter",
				"status_filter", req.StatusFilter)
			return nil, status.Errorf(codes.InvalidArgument, "invalid status filter: %v", err)
		}
		filters.Status = &domainStatus
	}

	// Apply category filter if specified (not UNSPECIFIED)
	if req.CategoryFilter != pb.DataCategory_DATA_CATEGORY_UNSPECIFIED {
		domainCategory, err := protoCategoryToDomain(req.CategoryFilter)
		if err != nil {
			s.logger.Warn("invalid category filter",
				"category_filter", req.CategoryFilter)
			return nil, status.Errorf(codes.InvalidArgument, "invalid category filter: %v", err)
		}
		filters.Category = &domainCategory
	}

	// Apply pagination
	pageSize := int(req.PageSize)
	if pageSize == 0 {
		pageSize = 50 // Default page size
	}
	if pageSize > 100 {
		pageSize = 100 // Max page size from proto validation
	}
	filters.Limit = pageSize

	// TODO: Implement proper cursor-based pagination using page_token
	// For now, we only support the first page
	filters.Offset = 0

	// Delegate to repository
	datasets, err := s.dataSetRepo.List(ctx, filters)
	if err != nil {
		s.logger.Error("failed to list datasets",
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to list datasets: %v", err)
	}

	// Convert to proto messages
	pbDatasets := make([]*pb.DataSetDefinition, len(datasets))
	for i, ds := range datasets {
		pbDatasets[i] = domainDataSetToProto(ds)
	}

	s.logger.Debug("listed datasets",
		"count", len(pbDatasets),
		"status_filter", req.StatusFilter,
		"category_filter", req.CategoryFilter)

	return &pb.ListDataSetsResponse{
		Datasets:      pbDatasets,
		NextPageToken: "", // TODO: Implement pagination token
	}, nil
}

// validateCelExpressions validates all three CEL expressions using the CEL validator.
// Returns a gRPC InvalidArgument error if any expression fails to compile.
func (s *Server) validateCelExpressions(validation, resolutionKey, errorMessage string) error {
	// Compile validation expression
	if validation != "" {
		_, err := s.celValidator.CompileValidation(validation)
		if err != nil {
			s.logger.Warn("validation expression compilation failed",
				"expression", validation,
				"error", err)
			return status.Errorf(codes.InvalidArgument,
				"validation_expression compilation failed: %v", err)
		}
	}

	// Compile resolution key expression
	if resolutionKey != "" {
		_, err := s.celValidator.CompileResolutionKey(resolutionKey)
		if err != nil {
			s.logger.Warn("resolution key expression compilation failed",
				"expression", resolutionKey,
				"error", err)
			return status.Errorf(codes.InvalidArgument,
				"resolution_key_expression compilation failed: %v", err)
		}
	}

	// Compile error message expression (optional, can be empty)
	if errorMessage != "" {
		_, err := s.celValidator.CompileErrorMessage(errorMessage)
		if err != nil {
			s.logger.Warn("error message expression compilation failed",
				"expression", errorMessage,
				"error", err)
			return status.Errorf(codes.InvalidArgument,
				"error_message_expression compilation failed: %v", err)
		}
	}

	return nil
}

// mapDataSetDomainError converts domain errors to appropriate gRPC status codes.
func (s *Server) mapDataSetDomainError(err error, operation, code string) error {
	switch {
	case errors.Is(err, domain.ErrDataSetNotFound):
		s.logger.Warn("dataset not found",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.NotFound, "dataset not found: %s", code)

	case errors.Is(err, domain.ErrDuplicateDataSetCode):
		s.logger.Warn("dataset code already exists",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.AlreadyExists, "dataset already exists: %s", code)

	case errors.Is(err, domain.ErrInvalidStatusTransition):
		s.logger.Warn("invalid status transition",
			"operation", operation,
			"code", code,
			"error", err)
		return status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)

	case errors.Is(err, domain.ErrDataSetDeprecated):
		s.logger.Warn("dataset is deprecated",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.FailedPrecondition, "dataset is deprecated: %s", code)

	case errors.Is(err, domain.ErrVersionMismatch):
		s.logger.Warn("version mismatch",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.Aborted, "version mismatch: dataset was modified concurrently")

	case errors.Is(err, domain.ErrCodeRequired):
		s.logger.Warn("dataset code required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "dataset code is required")

	case errors.Is(err, domain.ErrNameRequired):
		s.logger.Warn("dataset name required",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.InvalidArgument, "dataset name (display_name) is required")

	case errors.Is(err, domain.ErrValidationExpressionRequired):
		s.logger.Warn("validation expression required",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.InvalidArgument, "validation_expression is required")

	case errors.Is(err, domain.ErrResolutionKeyExpressionRequired):
		s.logger.Warn("resolution key expression required",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.InvalidArgument, "resolution_key_expression is required")

	case errors.Is(err, domain.ErrInvalidDataCategory):
		s.logger.Warn("invalid data category",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.InvalidArgument, "invalid data category")

	default:
		s.logger.Error("internal error",
			"operation", operation,
			"code", code,
			"error", err)
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// domainDataSetToProto converts a domain DataSetDefinition to proto DataSetDefinition.
func domainDataSetToProto(ds domain.DataSetDefinition) *pb.DataSetDefinition {
	pbDataSet := &pb.DataSetDefinition{
		Id:                      ds.ID().String(),
		Code:                    ds.Code(),
		Version:                 int32(ds.Version()),
		Category:                domainCategoryToProto(ds.DataCategory()),
		Unit:                    "", // Unit is not stored in domain; could be derived from category
		ResolutionKeyExpression: ds.ResolutionKeyExpression(),
		ValidationExpression:    ds.ValidationExpression(),
		ErrorMessageExpression:  ds.ErrorMessageExpression(),
		Status:                  domainStatusToProto(ds.Status()),
		DisplayName:             ds.Name(),
		Description:             ds.Description(),
		CreatedAt:               timestamppb.New(ds.CreatedAt()),
	}

	// Set UpdatedAt if different from CreatedAt
	if !ds.UpdatedAt().Equal(ds.CreatedAt()) {
		pbDataSet.UpdatedAt = timestamppb.New(ds.UpdatedAt())
	}

	// Set EffectiveFrom to CreatedAt as default (domain doesn't have separate effective dates)
	pbDataSet.EffectiveFrom = timestamppb.New(ds.CreatedAt())

	return pbDataSet
}

// domainStatusToProto converts domain DataSetStatus to proto DataSetStatus.
func domainStatusToProto(status domain.DataSetStatus) pb.DataSetStatus {
	switch status {
	case domain.DataSetStatusDraft:
		return pb.DataSetStatus_DATA_SET_STATUS_DRAFT
	case domain.DataSetStatusActive:
		return pb.DataSetStatus_DATA_SET_STATUS_ACTIVE
	case domain.DataSetStatusDeprecated:
		return pb.DataSetStatus_DATA_SET_STATUS_DEPRECATED
	default:
		return pb.DataSetStatus_DATA_SET_STATUS_UNSPECIFIED
	}
}

// protoStatusToDomain converts proto DataSetStatus to domain DataSetStatus.
func protoStatusToDomain(status pb.DataSetStatus) (domain.DataSetStatus, error) {
	switch status {
	case pb.DataSetStatus_DATA_SET_STATUS_UNSPECIFIED:
		return "", domain.ErrInvalidDataSetStatus
	case pb.DataSetStatus_DATA_SET_STATUS_DRAFT:
		return domain.DataSetStatusDraft, nil
	case pb.DataSetStatus_DATA_SET_STATUS_ACTIVE:
		return domain.DataSetStatusActive, nil
	case pb.DataSetStatus_DATA_SET_STATUS_DEPRECATED:
		return domain.DataSetStatusDeprecated, nil
	default:
		return "", domain.ErrInvalidDataSetStatus
	}
}

// domainCategoryToProto converts domain DataCategory to proto DataCategory.
// Note: The domain uses a simplified two-value category system (PRICING/CONTEXTUAL)
// while proto has more granular categories. We map PRICING to FX_RATE as default.
func domainCategoryToProto(category domain.DataCategory) pb.DataCategory {
	switch category {
	case domain.DataCategoryPricing:
		// Default PRICING to FX_RATE in proto as the domain doesn't have granular categories
		return pb.DataCategory_DATA_CATEGORY_FX_RATE
	case domain.DataCategoryContextual:
		// CONTEXTUAL maps to INDEX_VALUE as a reasonable default for reference data
		return pb.DataCategory_DATA_CATEGORY_INDEX_VALUE
	default:
		return pb.DataCategory_DATA_CATEGORY_UNSPECIFIED
	}
}

// protoCategoryToDomain converts proto DataCategory to domain DataCategory.
// Groups proto categories into domain's simpler PRICING/CONTEXTUAL model.
func protoCategoryToDomain(category pb.DataCategory) (domain.DataCategory, error) {
	switch category {
	// Price-based categories -> PRICING
	case pb.DataCategory_DATA_CATEGORY_FX_RATE,
		pb.DataCategory_DATA_CATEGORY_INTEREST_RATE,
		pb.DataCategory_DATA_CATEGORY_COMMODITY_PRICE,
		pb.DataCategory_DATA_CATEGORY_EQUITY_PRICE,
		pb.DataCategory_DATA_CATEGORY_ENERGY_PRICE,
		pb.DataCategory_DATA_CATEGORY_CARBON_PRICE,
		pb.DataCategory_DATA_CATEGORY_BENCHMARK_RATE,
		pb.DataCategory_DATA_CATEGORY_CREDIT_SPREAD:
		return domain.DataCategoryPricing, nil

	// Index/reference categories -> CONTEXTUAL
	case pb.DataCategory_DATA_CATEGORY_INDEX_VALUE,
		pb.DataCategory_DATA_CATEGORY_VOLATILITY:
		return domain.DataCategoryContextual, nil

	case pb.DataCategory_DATA_CATEGORY_UNSPECIFIED:
		return "", domain.ErrInvalidDataCategory

	default:
		return "", domain.ErrInvalidDataCategory
	}
}
