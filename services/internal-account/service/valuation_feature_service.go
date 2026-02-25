// Package service implements gRPC services for the internal account domain.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	ibaobservability "github.com/meridianhub/meridian/services/internal-account/observability"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Valuation feature specific errors
var (
	ErrValuationFeatureRepoNil       = errors.New("valuation feature repository cannot be nil")
	ErrMethodOutputMismatch          = errors.New("valuation method output_instrument does not match account native instrument")
	ErrInvalidValuationFeatureAction = errors.New("invalid valuation feature action")
)

// Valuation feature operation status constants for metrics
const (
	opStatusValuationFeatureRepoNil      = "valuation_feature_repo_nil"
	opStatusInvalidFeatureID             = "invalid_feature_id"
	opStatusFeatureNotFound              = "feature_not_found"
	opStatusFeatureAlreadyExists         = "feature_already_exists"
	opStatusMethodOutputMismatch         = "method_output_mismatch"
	opStatusInvalidFeatureAction         = "invalid_feature_action"
	opStatusFeatureLifecycleError        = "feature_lifecycle_error"
	opStatusMissingAccountOrInstrument   = "missing_account_or_instrument"
	opStatusInvalidRequest               = "invalid_request"
	opStatusValuationFeatureVersionError = "version_conflict"
	opStatusRetrieveFailed               = "retrieve_failed"
	opStatusSaveFailed                   = "save_failed"

	defaultSystemUser = "system"
)

// CreateValuationFeature creates a valuation method assignment for an internal account.
// Validates that output_instrument matches account's native instrument (instrument_code).
func (s *Service) CreateValuationFeature(ctx context.Context, req *pb.CreateValuationFeatureRequest) (*pb.CreateValuationFeatureResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("create_valuation_feature", operationStatus, time.Since(start))
	}()

	if s.valuationFeatureRepo == nil {
		operationStatus = opStatusValuationFeatureRepoNil
		return nil, status.Error(codes.FailedPrecondition, "valuation feature operations not configured")
	}

	createdBy := defaultSystemUser
	if claims, ok := auth.GetClaimsFromContext(ctx); ok && claims != nil {
		createdBy = claims.Subject
	}

	// Resolve account to get UUID and native instrument
	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	// Validate output_instrument matches account's native instrument
	nativeInstrument := account.InstrumentCode()
	if req.OutputInstrument != nativeInstrument {
		operationStatus = opStatusMethodOutputMismatch
		return nil, status.Errorf(codes.FailedPrecondition,
			"method output_instrument mismatch: expected %s (account native instrument), got %s",
			nativeInstrument, req.OutputInstrument)
	}

	methodID, err := uuid.Parse(req.ValuationMethodId)
	if err != nil {
		operationStatus = opStatusInvalidRequest
		return nil, status.Errorf(codes.InvalidArgument, "invalid valuation_method_id: %v", err)
	}

	var parameters map[string]interface{}
	if req.Parameters != "" {
		if err := json.Unmarshal([]byte(req.Parameters), &parameters); err != nil {
			operationStatus = opStatusInvalidRequest
			return nil, status.Errorf(codes.InvalidArgument, "invalid parameters JSON: %v", err)
		}
	}

	feature, err := domain.NewValuationFeature(
		account.ID(),
		req.InstrumentCode,
		methodID,
		int(req.ValuationMethodVersion),
		parameters,
		createdBy,
	)
	if err != nil {
		operationStatus = opStatusInvalidRequest
		return nil, status.Errorf(codes.InvalidArgument, "failed to create valuation feature: %v", err)
	}

	if err := feature.Activate(createdBy); err != nil {
		operationStatus = opStatusFeatureLifecycleError
		return nil, status.Errorf(codes.Internal, "failed to activate valuation feature: %v", err)
	}

	if err := s.valuationFeatureRepo.Create(ctx, feature); err != nil {
		if errors.Is(err, persistence.ErrValuationFeatureAlreadyExists) {
			operationStatus = opStatusFeatureAlreadyExists
			return nil, status.Errorf(codes.AlreadyExists, "valuation feature already exists for account %s and instrument %s", req.AccountId, req.InstrumentCode)
		}
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to save valuation feature: %v", err)
	}

	return &pb.CreateValuationFeatureResponse{
		Feature: s.domainToProtoValuationFeature(feature),
	}, nil
}

// UpdateValuationFeature performs lifecycle transitions on a valuation feature.
func (s *Service) UpdateValuationFeature(ctx context.Context, req *pb.UpdateValuationFeatureRequest) (*pb.UpdateValuationFeatureResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("update_valuation_feature", operationStatus, time.Since(start))
	}()

	if s.valuationFeatureRepo == nil {
		operationStatus = opStatusValuationFeatureRepoNil
		return nil, status.Error(codes.FailedPrecondition, "valuation feature operations not configured")
	}

	updatedBy := defaultSystemUser
	if claims, ok := auth.GetClaimsFromContext(ctx); ok && claims != nil {
		updatedBy = claims.Subject
	}

	featureID, err := uuid.Parse(req.FeatureId)
	if err != nil {
		operationStatus = opStatusInvalidFeatureID
		return nil, status.Errorf(codes.InvalidArgument, "invalid feature_id: %v", err)
	}

	feature, err := s.valuationFeatureRepo.FindByID(ctx, featureID)
	if err != nil {
		if errors.Is(err, persistence.ErrValuationFeatureNotFound) {
			operationStatus = opStatusFeatureNotFound
			return nil, status.Errorf(codes.NotFound, "valuation feature not found: %s", req.FeatureId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve valuation feature: %v", err)
	}

	switch req.Action {
	case pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_ACTIVATE:
		if err := feature.Activate(updatedBy); err != nil {
			if errors.Is(err, domain.ErrInvalidValuationFeatureTransition) {
				operationStatus = opStatusFeatureLifecycleError
				return nil, status.Errorf(codes.FailedPrecondition, "cannot activate feature: %v", err)
			}
			operationStatus = opStatusFeatureLifecycleError
			return nil, status.Errorf(codes.Internal, "failed to activate valuation feature: %v", err)
		}

	case pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE:
		if err := feature.Terminate(updatedBy); err != nil {
			if errors.Is(err, domain.ErrInvalidValuationFeatureTransition) {
				operationStatus = opStatusFeatureLifecycleError
				return nil, status.Errorf(codes.FailedPrecondition, "cannot terminate feature: %v", err)
			}
			operationStatus = opStatusFeatureLifecycleError
			return nil, status.Errorf(codes.Internal, "failed to terminate valuation feature: %v", err)
		}

	case pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_UNSPECIFIED:
		operationStatus = opStatusInvalidFeatureAction
		return nil, status.Error(codes.InvalidArgument, "action must be specified")
	default:
		operationStatus = opStatusInvalidFeatureAction
		return nil, status.Errorf(codes.InvalidArgument, "unsupported action: %v", req.Action)
	}

	if err := s.valuationFeatureRepo.Update(ctx, feature); err != nil {
		if errors.Is(err, persistence.ErrValuationFeatureVersionConflict) {
			operationStatus = opStatusValuationFeatureVersionError
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		}
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to update valuation feature: %v", err)
	}

	return &pb.UpdateValuationFeatureResponse{
		Feature: s.domainToProtoValuationFeature(feature),
	}, nil
}

// GetValuationFeature retrieves a valuation feature by ID or by account+instrument with bi-temporal query.
func (s *Service) GetValuationFeature(ctx context.Context, req *pb.GetValuationFeatureRequest) (*pb.GetValuationFeatureResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("get_valuation_feature", operationStatus, time.Since(start))
	}()

	if s.valuationFeatureRepo == nil {
		operationStatus = opStatusValuationFeatureRepoNil
		return nil, status.Error(codes.FailedPrecondition, "valuation feature operations not configured")
	}

	var feature *domain.ValuationFeature
	var err error

	if req.FeatureId != "" {
		featureID, parseErr := uuid.Parse(req.FeatureId)
		if parseErr != nil {
			operationStatus = opStatusInvalidFeatureID
			return nil, status.Errorf(codes.InvalidArgument, "invalid feature_id: %v", parseErr)
		}

		feature, err = s.valuationFeatureRepo.FindByID(ctx, featureID)
		if err != nil {
			if errors.Is(err, persistence.ErrValuationFeatureNotFound) {
				operationStatus = opStatusFeatureNotFound
				return nil, status.Errorf(codes.NotFound, "valuation feature not found: %s", req.FeatureId)
			}
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to retrieve valuation feature: %v", err)
		}
	} else if req.AccountId != "" && req.InstrumentCode != "" {
		account, accountErr := s.findAccountByID(ctx, req.AccountId)
		if accountErr != nil {
			operationStatus = opStatusAccountNotFound
			return nil, accountErr
		}

		knowledgeAt := time.Now()
		if req.KnowledgeAt != nil {
			knowledgeAt = req.KnowledgeAt.AsTime()
		}

		feature, err = s.valuationFeatureRepo.FindByAccountIDAndInstrument(ctx, account.ID(), req.InstrumentCode, knowledgeAt)
		if err != nil {
			if errors.Is(err, persistence.ErrValuationFeatureNotFound) {
				operationStatus = opStatusFeatureNotFound
				return nil, status.Errorf(codes.NotFound, "no active valuation feature found for account %s and instrument %s at %v",
					req.AccountId, req.InstrumentCode, knowledgeAt)
			}
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to retrieve valuation feature: %v", err)
		}
	} else {
		operationStatus = opStatusMissingAccountOrInstrument
		return nil, status.Error(codes.InvalidArgument, "must provide either feature_id or (account_id + instrument_code)")
	}

	return &pb.GetValuationFeatureResponse{
		Feature: s.domainToProtoValuationFeature(feature),
	}, nil
}

// ListValuationFeatures retrieves all valuation features for an internal account.
func (s *Service) ListValuationFeatures(ctx context.Context, req *pb.ListValuationFeaturesRequest) (*pb.ListValuationFeaturesResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("list_valuation_features", operationStatus, time.Since(start))
	}()

	if s.valuationFeatureRepo == nil {
		operationStatus = opStatusValuationFeatureRepoNil
		return nil, status.Error(codes.FailedPrecondition, "valuation feature operations not configured")
	}

	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	var statusFilter *domain.ValuationFeatureLifecycleStatus
	if req.LifecycleStatus != pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_UNSPECIFIED {
		domainStatus := s.protoToDomainLifecycleStatus(req.LifecycleStatus)
		statusFilter = &domainStatus
	}

	features, err := s.valuationFeatureRepo.FindByAccountID(ctx, account.ID(), statusFilter)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to list valuation features: %v", err)
	}

	protoFeatures := make([]*pb.ValuationFeature, len(features))
	for i, f := range features {
		protoFeatures[i] = s.domainToProtoValuationFeature(f)
	}

	return &pb.ListValuationFeaturesResponse{
		Features: protoFeatures,
	}, nil
}

// domainToProtoValuationFeature converts a domain valuation feature to proto
func (s *Service) domainToProtoValuationFeature(f *domain.ValuationFeature) *pb.ValuationFeature {
	var parametersJSON string
	if f.Parameters != nil {
		jsonBytes, err := json.Marshal(f.Parameters)
		if err != nil {
			s.logger.Warn("failed to marshal valuation feature parameters",
				"feature_id", f.ID, "error", err)
		} else {
			parametersJSON = string(jsonBytes)
		}
	}

	return &pb.ValuationFeature{
		Id:                     f.ID.String(),
		AccountId:              f.AccountID.String(),
		InstrumentCode:         f.InstrumentCode,
		ValuationMethodId:      f.ValuationMethodID.String(),
		ValuationMethodVersion: int32(f.ValuationMethodVersion),
		Parameters:             parametersJSON,
		LifecycleStatus:        s.domainToProtoLifecycleStatus(f.LifecycleStatus),
		ValidFrom:              timestamppb.New(f.ValidFrom),
		ValidTo:                timestamppb.New(f.ValidTo),
		CreatedAt:              timestamppb.New(f.CreatedAt),
		CreatedBy:              f.CreatedBy,
		UpdatedAt:              timestamppb.New(f.UpdatedAt),
		UpdatedBy:              f.UpdatedBy,
		Version:                int32(f.Version),
	}
}

// domainToProtoLifecycleStatus converts domain lifecycle status to proto enum
func (s *Service) domainToProtoLifecycleStatus(domainStatus domain.ValuationFeatureLifecycleStatus) pb.ValuationFeatureLifecycleStatus {
	switch domainStatus {
	case domain.ValuationFeatureLifecycleStatusInitiated:
		return pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_INITIATED
	case domain.ValuationFeatureLifecycleStatusActive:
		return pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE
	case domain.ValuationFeatureLifecycleStatusTerminated:
		return pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED
	default:
		return pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_UNSPECIFIED
	}
}

// protoToDomainLifecycleStatus converts proto enum to domain lifecycle status
func (s *Service) protoToDomainLifecycleStatus(protoStatus pb.ValuationFeatureLifecycleStatus) domain.ValuationFeatureLifecycleStatus {
	switch protoStatus {
	case pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_INITIATED:
		return domain.ValuationFeatureLifecycleStatusInitiated
	case pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE:
		return domain.ValuationFeatureLifecycleStatusActive
	case pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED:
		return domain.ValuationFeatureLifecycleStatusTerminated
	case pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_UNSPECIFIED:
		return domain.ValuationFeatureLifecycleStatusInitiated
	default:
		return domain.ValuationFeatureLifecycleStatusInitiated
	}
}
