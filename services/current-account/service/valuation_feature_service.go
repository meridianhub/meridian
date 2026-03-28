// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
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

	// defaultSystemUser is the fallback user when no auth context is available
	defaultSystemUser = "system"
)

// CreateValuationFeature creates a valuation method assignment for an account.
// CRITICAL: Validates that output_instrument matches account's native instrument (currency).
func (s *Service) CreateValuationFeature(ctx context.Context, req *pb.CreateValuationFeatureRequest) (*pb.CreateValuationFeatureResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("create_valuation_feature", operationStatus, time.Since(start))
	}()

	if s.valuationFeatureRepo == nil {
		operationStatus = opStatusValuationFeatureRepoNil
		return nil, status.Error(codes.FailedPrecondition, "valuation feature operations not configured")
	}

	createdBy := defaultSystemUser
	if claims, ok := auth.GetClaimsFromContext(ctx); ok && claims != nil {
		createdBy = claims.Subject
	}

	// Validate account and output instrument
	account, opStatus, err := s.validateAccountForValuationFeature(ctx, req.AccountId, req.OutputInstrument)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	// Parse and validate request inputs
	methodID, parameters, opStatus, err := validateCreateValuationFeatureInputs(req)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	// Create, activate, and save
	feature, opStatus, err := s.buildAndSaveValuationFeature(ctx, account, req.InstrumentCode, methodID, int(req.ValuationMethodVersion), parameters, createdBy, req.AccountId)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	return &pb.CreateValuationFeatureResponse{
		Feature: s.domainToProtoValuationFeature(feature),
	}, nil
}

// validateAccountForValuationFeature retrieves the account and validates the output instrument.
func (s *Service) validateAccountForValuationFeature(ctx context.Context, accountID, outputInstrument string) (domain.CurrentAccount, string, error) {
	account, err := s.repo.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			return account, opStatusAccountNotFound,
				status.Errorf(codes.NotFound, "account not found: %s", accountID)
		}
		return account, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	nativeInstrument := account.Balance().InstrumentCode()
	if outputInstrument != nativeInstrument {
		return account, opStatusMethodOutputMismatch,
			status.Errorf(codes.FailedPrecondition,
				"method output_instrument mismatch: expected %s (account native instrument), got %s",
				nativeInstrument, outputInstrument)
	}

	return account, "", nil
}

// validateCreateValuationFeatureInputs parses the method ID and parameters from the request.
func validateCreateValuationFeatureInputs(req *pb.CreateValuationFeatureRequest) (uuid.UUID, map[string]interface{}, string, error) {
	methodID, err := uuid.Parse(req.ValuationMethodId)
	if err != nil {
		return uuid.Nil, nil, opStatusInvalidRequest,
			status.Errorf(codes.InvalidArgument, "invalid valuation_method_id: %v", err)
	}

	var parameters map[string]interface{}
	if req.Parameters != "" {
		if err := json.Unmarshal([]byte(req.Parameters), &parameters); err != nil {
			return uuid.Nil, nil, opStatusInvalidRequest,
				status.Errorf(codes.InvalidArgument, "invalid parameters JSON: %v", err)
		}
	}

	return methodID, parameters, "", nil
}

// buildAndSaveValuationFeature creates, activates, and persists a valuation feature.
func (s *Service) buildAndSaveValuationFeature(ctx context.Context, account domain.CurrentAccount, instrumentCode string, methodID uuid.UUID, methodVersion int, parameters map[string]interface{}, createdBy, accountID string) (*domain.ValuationFeature, string, error) {
	feature, err := domain.NewValuationFeature(
		account.ID(), instrumentCode, methodID, methodVersion, parameters, createdBy,
	)
	if err != nil {
		return nil, opStatusInvalidRequest,
			status.Errorf(codes.InvalidArgument, "failed to create valuation feature: %v", err)
	}

	if err := feature.Activate(createdBy); err != nil {
		return nil, opStatusFeatureLifecycleError,
			status.Errorf(codes.Internal, "failed to activate valuation feature: %v", err)
	}

	if err := s.valuationFeatureRepo.Create(ctx, feature); err != nil {
		if errors.Is(err, persistence.ErrValuationFeatureAlreadyExists) {
			return nil, opStatusFeatureAlreadyExists,
				status.Errorf(codes.AlreadyExists, "valuation feature already exists for account %s and instrument %s", accountID, instrumentCode)
		}
		return nil, opStatusSaveFailed,
			status.Errorf(codes.Internal, "failed to save valuation feature: %v", err)
	}

	return feature, "", nil
}

// UpdateValuationFeature performs lifecycle transitions on a valuation feature.
func (s *Service) UpdateValuationFeature(ctx context.Context, req *pb.UpdateValuationFeatureRequest) (*pb.UpdateValuationFeatureResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("update_valuation_feature", operationStatus, time.Since(start))
	}()

	if s.valuationFeatureRepo == nil {
		operationStatus = opStatusValuationFeatureRepoNil
		return nil, status.Error(codes.FailedPrecondition, "valuation feature operations not configured")
	}

	updatedBy := defaultSystemUser
	if claims, ok := auth.GetClaimsFromContext(ctx); ok && claims != nil {
		updatedBy = claims.Subject
	}

	// Retrieve feature
	feature, opStatus, err := s.retrieveValuationFeature(ctx, req.FeatureId)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	// Apply lifecycle transition
	opStatus, err = applyValuationFeatureAction(feature, req.Action, updatedBy)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	// Save changes
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

// retrieveValuationFeature parses the feature ID and retrieves the feature.
func (s *Service) retrieveValuationFeature(ctx context.Context, featureIDStr string) (*domain.ValuationFeature, string, error) {
	featureID, err := uuid.Parse(featureIDStr)
	if err != nil {
		return nil, opStatusInvalidFeatureID,
			status.Errorf(codes.InvalidArgument, "invalid feature_id: %v", err)
	}

	feature, err := s.valuationFeatureRepo.FindByID(ctx, featureID)
	if err != nil {
		if errors.Is(err, persistence.ErrValuationFeatureNotFound) {
			return nil, opStatusFeatureNotFound,
				status.Errorf(codes.NotFound, "valuation feature not found: %s", featureIDStr)
		}
		return nil, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve valuation feature: %v", err)
	}
	return feature, "", nil
}

// applyValuationFeatureAction applies the lifecycle transition to a valuation feature.
func applyValuationFeatureAction(feature *domain.ValuationFeature, action pb.ValuationFeatureAction, updatedBy string) (string, error) {
	switch action {
	case pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_ACTIVATE:
		if err := feature.Activate(updatedBy); err != nil {
			if errors.Is(err, domain.ErrInvalidValuationFeatureTransition) {
				return opStatusFeatureLifecycleError,
					status.Errorf(codes.FailedPrecondition, "cannot activate feature: %v", err)
			}
			return opStatusFeatureLifecycleError,
				status.Errorf(codes.Internal, "failed to activate valuation feature: %v", err)
		}

	case pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE:
		if err := feature.Terminate(updatedBy); err != nil {
			if errors.Is(err, domain.ErrInvalidValuationFeatureTransition) {
				return opStatusFeatureLifecycleError,
					status.Errorf(codes.FailedPrecondition, "cannot terminate feature: %v", err)
			}
			return opStatusFeatureLifecycleError,
				status.Errorf(codes.Internal, "failed to terminate valuation feature: %v", err)
		}

	case pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_UNSPECIFIED:
		return opStatusInvalidFeatureAction,
			status.Error(codes.InvalidArgument, "action must be specified")
	default:
		return opStatusInvalidFeatureAction,
			status.Errorf(codes.InvalidArgument, "unsupported action: %v", action)
	}
	return "", nil
}

// GetValuationFeature retrieves a valuation feature by ID or by account+instrument with bi-temporal query.
func (s *Service) GetValuationFeature(ctx context.Context, req *pb.GetValuationFeatureRequest) (*pb.GetValuationFeatureResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("get_valuation_feature", operationStatus, time.Since(start))
	}()

	if s.valuationFeatureRepo == nil {
		operationStatus = opStatusValuationFeatureRepoNil
		return nil, status.Error(codes.FailedPrecondition, "valuation feature operations not configured")
	}

	feature, opStatus, err := s.resolveValuationFeatureByRequest(ctx, req)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	return &pb.GetValuationFeatureResponse{
		Feature: s.domainToProtoValuationFeature(feature),
	}, nil
}

// resolveValuationFeatureByRequest resolves a valuation feature by ID or by account+instrument lookup.
func (s *Service) resolveValuationFeatureByRequest(ctx context.Context, req *pb.GetValuationFeatureRequest) (*domain.ValuationFeature, string, error) {
	if req.FeatureId != "" {
		return s.getValuationFeatureByID(ctx, req.FeatureId)
	}
	if req.AccountId != "" && req.InstrumentCode != "" {
		return s.getValuationFeatureByAccountAndInstrument(ctx, req.AccountId, req.InstrumentCode, req.KnowledgeAt)
	}
	return nil, opStatusMissingAccountOrInstrument,
		status.Error(codes.InvalidArgument, "must provide either feature_id or (account_id + instrument_code)")
}

// getValuationFeatureByID retrieves a valuation feature by its UUID.
func (s *Service) getValuationFeatureByID(ctx context.Context, featureIDStr string) (*domain.ValuationFeature, string, error) {
	featureID, parseErr := uuid.Parse(featureIDStr)
	if parseErr != nil {
		return nil, opStatusInvalidFeatureID,
			status.Errorf(codes.InvalidArgument, "invalid feature_id: %v", parseErr)
	}

	feature, err := s.valuationFeatureRepo.FindByID(ctx, featureID)
	if err != nil {
		if errors.Is(err, persistence.ErrValuationFeatureNotFound) {
			return nil, opStatusFeatureNotFound,
				status.Errorf(codes.NotFound, "valuation feature not found: %s", featureIDStr)
		}
		return nil, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve valuation feature: %v", err)
	}
	return feature, "", nil
}

// getValuationFeatureByAccountAndInstrument performs a bi-temporal lookup by account+instrument.
func (s *Service) getValuationFeatureByAccountAndInstrument(ctx context.Context, accountID, instrumentCode string, knowledgeAtPb *timestamppb.Timestamp) (*domain.ValuationFeature, string, error) {
	account, accountErr := s.repo.FindByID(ctx, accountID)
	if accountErr != nil {
		if errors.Is(accountErr, persistence.ErrAccountNotFound) {
			return nil, opStatusAccountNotFound,
				status.Errorf(codes.NotFound, "account not found: %s", accountID)
		}
		return nil, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve account: %v", accountErr)
	}

	knowledgeAt := time.Now()
	if knowledgeAtPb != nil {
		knowledgeAt = knowledgeAtPb.AsTime()
	}

	feature, err := s.valuationFeatureRepo.FindByAccountIDAndInstrument(ctx, account.ID(), instrumentCode, knowledgeAt)
	if err != nil {
		if errors.Is(err, persistence.ErrValuationFeatureNotFound) {
			return nil, opStatusFeatureNotFound,
				status.Errorf(codes.NotFound, "no active valuation feature found for account %s and instrument %s at %v",
					accountID, instrumentCode, knowledgeAt)
		}
		return nil, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve valuation feature: %v", err)
	}
	return feature, "", nil
}

// ListValuationFeatures retrieves all valuation features for an account.
func (s *Service) ListValuationFeatures(ctx context.Context, req *pb.ListValuationFeaturesRequest) (*pb.ListValuationFeaturesResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("list_valuation_features", operationStatus, time.Since(start))
	}()

	// Validate valuation feature repository is configured
	if s.valuationFeatureRepo == nil {
		operationStatus = opStatusValuationFeatureRepoNil
		return nil, status.Error(codes.FailedPrecondition, "valuation feature operations not configured")
	}

	// Resolve account ID from string to UUID
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Convert lifecycle status filter if provided
	var statusFilter *domain.ValuationFeatureLifecycleStatus
	if req.LifecycleStatus != pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_UNSPECIFIED {
		domainStatus := s.protoToDomainLifecycleStatus(req.LifecycleStatus)
		statusFilter = &domainStatus
	}

	// Retrieve features
	features, err := s.valuationFeatureRepo.FindByAccountID(ctx, account.ID(), statusFilter)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to list valuation features: %v", err)
	}

	// Convert to proto
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
