// Package service provides gRPC service implementations for the Market Information service.
// BIAN Service Domain: Market Information Management
//
// This file implements the single-observation recording operations and their validation helpers.
package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// defaultResolutionKey is used when no resolution key can be computed.
const defaultResolutionKey = "default"

// RecordObservation records a new market data observation.
// Returns INVALID_ARGUMENT if validation fails.
// Returns NOT_FOUND if data set or source doesn't exist.
func (s *Server) RecordObservation(ctx context.Context, req *pb.RecordObservationRequest) (*pb.RecordObservationResponse, error) {
	// 1. Load and verify dataset and source
	dataset, source, err := s.loadAndVerifyDatasetAndSource(ctx, req.DatasetCode, req.SourceCode)
	if err != nil {
		return nil, err
	}

	// 2. Validate required timestamps BEFORE any usage (guard against nil dereference)
	if err := validateRecordTimestamps(req); err != nil {
		return nil, err
	}

	// 3. Compute resolution key and validate observation
	observationContext := ToContextMap(req.Attributes)
	resolutionKey, err := s.computeAndValidateObservation(dataset, req, observationContext, source)
	if err != nil {
		return nil, err
	}

	// 4. Build and persist the domain observation
	observation, err := s.buildAndPersistObservation(ctx, req, dataset, source, resolutionKey, observationContext)
	if err != nil {
		return nil, err
	}

	s.logger.Info("observation recorded",
		"observation_id", observation.ID().String(),
		"dataset_code", req.DatasetCode,
		"resolution_key", resolutionKey,
		"quality", observation.QualityLevel().String())

	// 5. Publish event (best-effort)
	s.publishObservationEvent(ctx, observation)

	// 6. Return proto response
	return &pb.RecordObservationResponse{
		Observation: domainObservationToProto(observation, req.Attributes, dataset.Version()),
	}, nil
}

// loadAndVerifyDatasetAndSource loads the dataset and source by code,
// verifying both are in an active state suitable for recording observations.
func (s *Server) loadAndVerifyDatasetAndSource(ctx context.Context, datasetCode, sourceCode string) (domain.DataSetDefinition, domain.DataSource, error) {
	dataset, err := s.dataSetRepo.FindByCode(ctx, datasetCode)
	if err != nil {
		return domain.DataSetDefinition{}, domain.DataSource{}, s.mapObservationDomainError(err, "RecordObservation", datasetCode)
	}
	if dataset.Status() != domain.DataSetStatusActive {
		s.logger.Warn("dataset is not active",
			"dataset_code", datasetCode,
			"status", dataset.Status().String())
		return domain.DataSetDefinition{}, domain.DataSource{}, status.Errorf(codes.FailedPrecondition, "dataset %s is not active (status: %s)", datasetCode, dataset.Status().String())
	}

	source, err := s.sourceRepo.FindByCode(ctx, sourceCode)
	if err != nil {
		return domain.DataSetDefinition{}, domain.DataSource{}, s.mapObservationDomainError(err, "RecordObservation", sourceCode)
	}
	if !source.IsActive() {
		s.logger.Warn("data source is not active",
			"source_code", sourceCode)
		return domain.DataSetDefinition{}, domain.DataSource{}, status.Errorf(codes.FailedPrecondition, "data source %s is not active", sourceCode)
	}

	return dataset, source, nil
}

// validateRecordTimestamps validates that required timestamps are present on the request.
func validateRecordTimestamps(req *pb.RecordObservationRequest) error {
	if req.ObservedAt == nil {
		return status.Errorf(codes.InvalidArgument, "observed_at timestamp is required")
	}
	if req.ValidFrom == nil {
		return status.Errorf(codes.InvalidArgument, "valid_from timestamp is required")
	}
	return nil
}

// computeAndValidateObservation computes the resolution key via CEL and validates
// the observation against the dataset's validation expression.
func (s *Server) computeAndValidateObservation(
	dataset domain.DataSetDefinition,
	req *pb.RecordObservationRequest,
	observationContext map[string]string,
	source domain.DataSource,
) (string, error) {
	resolutionKey, err := s.computeResolutionKey(dataset, observationContext)
	if err != nil {
		s.logger.Warn("resolution key computation failed",
			"dataset_code", req.DatasetCode,
			"error", err)
		return "", status.Errorf(codes.InvalidArgument, "failed to compute resolution key: %v", err)
	}

	if err := s.validateObservation(dataset, req, observationContext, source.ID().String()); err != nil {
		s.logger.Warn("observation validation failed",
			"dataset_code", req.DatasetCode,
			"value", req.Value,
			"error", err)
		return "", err
	}

	return resolutionKey, nil
}

// buildAndPersistObservation parses the request value, creates the domain observation,
// and persists it to the repository.
func (s *Server) buildAndPersistObservation(
	ctx context.Context,
	req *pb.RecordObservationRequest,
	dataset domain.DataSetDefinition,
	source domain.DataSource,
	resolutionKey string,
	observationContext map[string]string,
) (domain.MarketPriceObservation, error) {
	value, err := decimal.NewFromString(req.Value)
	if err != nil {
		s.logger.Warn("invalid decimal value",
			"value", req.Value,
			"error", err)
		return domain.MarketPriceObservation{}, status.Errorf(codes.InvalidArgument, "invalid decimal value: %s", req.Value)
	}

	qualityLevel := protoQualityLevelToDomain(req.Quality)
	validTo := time.Now().Add(100 * 365 * 24 * time.Hour)
	if req.ValidTo != nil {
		validTo = req.ValidTo.AsTime()
	}

	observation, err := domain.NewMarketPriceObservation(
		req.DatasetCode,
		source.ID(),
		resolutionKey,
		value,
		dataset.Name(),
		req.ObservedAt.AsTime(),
		req.ValidFrom.AsTime(),
		validTo,
		uuid.New(),
		qualityLevel,
		source.TrustLevel(),
		domain.NewObservationContext(observationContext),
	)
	if err != nil {
		s.logger.Warn("failed to create observation",
			"dataset_code", req.DatasetCode,
			"error", err)
		return domain.MarketPriceObservation{}, s.mapObservationDomainError(err, "RecordObservation", req.DatasetCode)
	}

	if err := s.observationRepo.Record(ctx, observation); err != nil {
		s.logger.Error("failed to record observation",
			"dataset_code", req.DatasetCode,
			"error", err)
		return domain.MarketPriceObservation{}, s.mapObservationDomainError(err, "RecordObservation", req.DatasetCode)
	}

	return observation, nil
}

// publishObservationEvent publishes an observation event to Kafka (best-effort).
// Only publishes for ACTUAL or VERIFIED quality levels.
func (s *Server) publishObservationEvent(ctx context.Context, observation domain.MarketPriceObservation) {
	if s.eventPublisher == nil || !shouldPublishObservationEvent(observation.QualityLevel()) {
		return
	}

	if obsPublisher, ok := s.eventPublisher.(ObservationEventPublisher); ok {
		if err := obsPublisher.PublishObservationRecorded(ctx, observation); err != nil {
			s.logger.Error("failed to publish observation event",
				"observation_id", observation.ID().String(),
				"error", err)
		}
	} else {
		event := mapObservationToProtoEvent(observation)
		if err := s.eventPublisher.Publish(ctx, event); err != nil {
			s.logger.Error("failed to publish observation event",
				"observation_id", observation.ID().String(),
				"error", err)
		}
	}
}

// computeResolutionKey evaluates the resolution key CEL expression.
func (s *Server) computeResolutionKey(dataset domain.DataSetDefinition, observationContext map[string]string) (string, error) {
	if s.celValidator == nil {
		// Fallback: use a simple concatenation of all context values
		if len(observationContext) == 0 {
			return defaultResolutionKey, nil
		}
		// Sort keys for deterministic selection of first non-empty value
		keys := make([]string, 0, len(observationContext))
		for k := range observationContext {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v := observationContext[k]; v != "" {
				return v, nil
			}
		}
		return defaultResolutionKey, nil
	}

	expression := dataset.ResolutionKeyExpression()
	if expression == "" {
		return defaultResolutionKey, nil
	}

	prg, err := s.celValidator.CompileResolutionKey(expression)
	if err != nil {
		return "", fmt.Errorf("failed to compile resolution key expression: %w", err)
	}

	result, err := s.celValidator.EvaluateResolutionKey(prg, ResolutionKeyInput{
		ObservationContext: observationContext,
	})
	if err != nil {
		return "", fmt.Errorf("failed to evaluate resolution key expression: %w", err)
	}

	return result, nil
}

// validateObservation evaluates the validation CEL expression.
func (s *Server) validateObservation(dataset domain.DataSetDefinition, req *pb.RecordObservationRequest, observationContext map[string]string, sourceID string) error {
	if s.celValidator == nil {
		// No validator available - skip validation
		return nil
	}

	expression := dataset.ValidationExpression()
	if expression == "" {
		return nil
	}

	// Determine valid_to for validation
	validTo := time.Now().Add(100 * 365 * 24 * time.Hour)
	if req.ValidTo != nil {
		validTo = req.ValidTo.AsTime()
	}

	validationInput := ValidationInput{
		Value:              req.Value,
		ObservationContext: observationContext,
		ObservedAt:         req.ObservedAt.AsTime(),
		ValidFrom:          req.ValidFrom.AsTime(),
		ValidTo:            validTo,
		SourceID:           sourceID,
		Quality:            int(req.Quality),
	}

	prg, err := s.celValidator.CompileValidation(expression)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "validation expression compilation failed: %v", err)
	}

	valid, err := s.celValidator.EvaluateValidation(prg, validationInput)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "validation evaluation failed: %v", err)
	}

	if !valid {
		// Generate custom error message if expression is available
		errMsg := s.generateValidationErrorMessage(dataset, req.Value, observationContext)
		return status.Errorf(codes.InvalidArgument, "%s", errMsg)
	}

	return nil
}

// generateValidationErrorMessage generates a custom error message using CEL expression.
func (s *Server) generateValidationErrorMessage(dataset domain.DataSetDefinition, value string, observationContext map[string]string) string {
	defaultMsg := fmt.Sprintf("observation value %s failed validation for dataset %s", value, dataset.Code())

	if s.celValidator == nil {
		return defaultMsg
	}

	expression := dataset.ErrorMessageExpression()
	if expression == "" {
		return defaultMsg
	}

	prg, err := s.celValidator.CompileErrorMessage(expression)
	if err != nil {
		return defaultMsg
	}

	msg, err := s.celValidator.EvaluateErrorMessage(prg, ErrorMessageInput{
		Value:              value,
		ObservationContext: observationContext,
		DatasetCode:        dataset.Code(),
	})
	if err != nil {
		return defaultMsg
	}

	return msg
}
