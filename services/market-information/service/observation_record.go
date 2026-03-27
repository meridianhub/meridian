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
	// 1. Load active dataset definition by code
	dataset, err := s.dataSetRepo.FindByCode(ctx, req.DatasetCode)
	if err != nil {
		return nil, s.mapObservationDomainError(err, "RecordObservation", req.DatasetCode)
	}

	// Verify dataset is active
	if dataset.Status() != domain.DataSetStatusActive {
		s.logger.Warn("dataset is not active",
			"dataset_code", req.DatasetCode,
			"status", dataset.Status().String())
		return nil, status.Errorf(codes.FailedPrecondition, "dataset %s is not active (status: %s)", req.DatasetCode, dataset.Status().String())
	}

	// 2. Load the data source
	source, err := s.sourceRepo.FindByCode(ctx, req.SourceCode)
	if err != nil {
		return nil, s.mapObservationDomainError(err, "RecordObservation", req.SourceCode)
	}

	// Verify source is active
	if !source.IsActive() {
		s.logger.Warn("data source is not active",
			"source_code", req.SourceCode)
		return nil, status.Errorf(codes.FailedPrecondition, "data source %s is not active", req.SourceCode)
	}

	// 3. Validate required timestamps BEFORE any usage (guard against nil dereference)
	// This must happen before validateObservation which calls AsTime() on these fields.
	if req.ObservedAt == nil {
		s.logger.Warn("observed_at timestamp is required",
			"dataset_code", req.DatasetCode)
		return nil, status.Errorf(codes.InvalidArgument, "observed_at timestamp is required")
	}
	if req.ValidFrom == nil {
		s.logger.Warn("valid_from timestamp is required",
			"dataset_code", req.DatasetCode)
		return nil, status.Errorf(codes.InvalidArgument, "valid_from timestamp is required")
	}

	// 4. Convert observation context to CEL map
	observationContext := ToContextMap(req.Attributes)

	// 5. Compute resolution key via CEL evaluation (if celValidator is available)
	resolutionKey, err := s.computeResolutionKey(dataset, observationContext)
	if err != nil {
		s.logger.Warn("resolution key computation failed",
			"dataset_code", req.DatasetCode,
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "failed to compute resolution key: %v", err)
	}

	// 6. Evaluate validation expression (reject if false)
	if err := s.validateObservation(dataset, req, observationContext, source.ID().String()); err != nil {
		s.logger.Warn("observation validation failed",
			"dataset_code", req.DatasetCode,
			"value", req.Value,
			"error", err)
		return nil, err // Already formatted as gRPC status error
	}

	// 7. Parse the decimal value
	value, err := decimal.NewFromString(req.Value)
	if err != nil {
		s.logger.Warn("invalid decimal value",
			"value", req.Value,
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid decimal value: %s", req.Value)
	}

	// 8. Convert proto QualityLevel to domain QualityLevel
	qualityLevel := protoQualityLevelToDomain(req.Quality)

	// 9. Determine valid_to (use provided or default to 100 years in future)
	validTo := time.Now().Add(100 * 365 * 24 * time.Hour) // Default: far future
	if req.ValidTo != nil {
		validTo = req.ValidTo.AsTime()
	}

	// 10. Create the domain observation (timestamps already validated in step 3)
	observation, err := domain.NewMarketPriceObservation(
		req.DatasetCode,
		source.ID(),
		resolutionKey,
		value,
		dataset.Name(), // Use dataset name as unit
		req.ObservedAt.AsTime(),
		req.ValidFrom.AsTime(),
		validTo,
		uuid.New(), // CausationID - generate new for this request
		qualityLevel,
		source.TrustLevel(),
		domain.NewObservationContext(observationContext),
	)
	if err != nil {
		s.logger.Warn("failed to create observation",
			"dataset_code", req.DatasetCode,
			"error", err)
		return nil, s.mapObservationDomainError(err, "RecordObservation", req.DatasetCode)
	}

	// 11. Persist the observation
	if err := s.observationRepo.Record(ctx, observation); err != nil {
		s.logger.Error("failed to record observation",
			"dataset_code", req.DatasetCode,
			"error", err)
		return nil, s.mapObservationDomainError(err, "RecordObservation", req.DatasetCode)
	}

	s.logger.Info("observation recorded",
		"observation_id", observation.ID().String(),
		"dataset_code", req.DatasetCode,
		"resolution_key", resolutionKey,
		"quality", qualityLevel.String())

	// 12. Publish ObservationRecorded event to Kafka ONLY if quality is ACTUAL or VERIFIED (not ESTIMATE)
	if s.eventPublisher != nil && shouldPublishObservationEvent(qualityLevel) {
		// Use the specialized publisher if available, otherwise use generic publisher
		if obsPublisher, ok := s.eventPublisher.(ObservationEventPublisher); ok {
			if err := obsPublisher.PublishObservationRecorded(ctx, observation); err != nil {
				// Log but don't fail the request - observation is already persisted
				s.logger.Error("failed to publish observation event",
					"observation_id", observation.ID().String(),
					"error", err)
			}
		} else {
			// Fallback to generic publisher
			event := mapObservationToProtoEvent(observation)
			if err := s.eventPublisher.Publish(ctx, event); err != nil {
				s.logger.Error("failed to publish observation event",
					"observation_id", observation.ID().String(),
					"error", err)
			}
		}
	}

	// 13. Return proto response
	return &pb.RecordObservationResponse{
		Observation: domainObservationToProto(observation, req.Attributes, dataset.Version()),
	}, nil
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
