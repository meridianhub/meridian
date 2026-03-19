// Package service provides gRPC service implementations for the Market Information service.
// BIAN Service Domain: Market Information Management
//
// This file implements the Observation-related gRPC methods for the MarketInformationService.
//
//meridian:large-file - known oversized file; split tracked in backlog
package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

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

// RecordObservationBatch records multiple observations with parallel validation.
// Returns partial success with details for each observation.
func (s *Server) RecordObservationBatch(ctx context.Context, req *pb.RecordObservationBatchRequest) (*pb.RecordObservationBatchResponse, error) {
	if len(req.Observations) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "at least one observation is required")
	}

	// Generate batch ID if not provided
	batchID := req.BatchId
	if batchID == "" {
		batchID = uuid.New().String()
	}

	// Pre-fetch datasets and sources to avoid repeated lookups
	datasets := make(map[string]domain.DataSetDefinition)
	sources := make(map[string]domain.DataSource)
	var fetchMu sync.Mutex

	// Use sync.Map for concurrent error collection
	validationErrors := &sync.Map{}

	// Parallel CEL validation using errgroup
	g, gCtx := errgroup.WithContext(ctx)

	// Limit concurrency to avoid overwhelming the system
	g.SetLimit(50)

	for i, entry := range req.Observations {
		i, entry := i, entry // Capture loop variables

		g.Go(func() error {
			// Check context cancellation
			if gCtx.Err() != nil {
				return gCtx.Err()
			}

			// validateEntry handles validation and stores errors in validationErrors.
			// We always return nil from the goroutine to allow other entries to continue processing.
			// The actual errors are collected in validationErrors sync.Map.
			validateEntry := func() {
				// Fetch or get cached dataset
				fetchMu.Lock()
				dataset, exists := datasets[entry.DatasetCode]
				fetchMu.Unlock()

				if !exists {
					ds, err := s.dataSetRepo.FindByCode(gCtx, entry.DatasetCode)
					if err != nil {
						validationErrors.Store(i, fmt.Sprintf("dataset not found: %s", entry.DatasetCode))
						return
					}
					fetchMu.Lock()
					datasets[entry.DatasetCode] = ds
					dataset = ds
					fetchMu.Unlock()
				}

				// Verify dataset is active
				if dataset.Status() != domain.DataSetStatusActive {
					validationErrors.Store(i, fmt.Sprintf("dataset %s is not active", entry.DatasetCode))
					return
				}

				// Fetch or get cached source
				fetchMu.Lock()
				source, exists := sources[entry.SourceCode]
				fetchMu.Unlock()

				if !exists {
					src, err := s.sourceRepo.FindByCode(gCtx, entry.SourceCode)
					if err != nil {
						validationErrors.Store(i, fmt.Sprintf("source not found: %s", entry.SourceCode))
						return
					}
					fetchMu.Lock()
					sources[entry.SourceCode] = src
					source = src
					fetchMu.Unlock()
				}

				// Verify source is active
				if !source.IsActive() {
					validationErrors.Store(i, fmt.Sprintf("source %s is not active", entry.SourceCode))
					return
				}

				// Validate required timestamps (guard against nil dereference)
				if entry.ObservedAt == nil {
					validationErrors.Store(i, "observed_at timestamp is required")
					return
				}
				if entry.ValidFrom == nil {
					validationErrors.Store(i, "valid_from timestamp is required")
					return
				}

				// Convert observation context
				observationContext := ToContextMap(entry.Attributes)

				// Validate using CEL expression
				if s.celValidator != nil && dataset.ValidationExpression() != "" {
					validTo := time.Now().Add(100 * 365 * 24 * time.Hour)
					if entry.ValidTo != nil {
						validTo = entry.ValidTo.AsTime()
					}

					validationInput := ValidationInput{
						Value:              entry.Value,
						ObservationContext: observationContext,
						ObservedAt:         entry.ObservedAt.AsTime(),
						ValidFrom:          entry.ValidFrom.AsTime(),
						ValidTo:            validTo,
						SourceID:           source.ID().String(),
						Quality:            int(entry.Quality),
					}

					prg, err := s.celValidator.CompileValidation(dataset.ValidationExpression())
					if err != nil {
						validationErrors.Store(i, fmt.Sprintf("validation expression error: %v", err))
						return
					}

					valid, err := s.celValidator.EvaluateValidation(prg, validationInput)
					if err != nil {
						validationErrors.Store(i, fmt.Sprintf("validation evaluation error: %v", err))
						return
					}

					if !valid {
						errMsg := s.generateValidationErrorMessage(dataset, entry.Value, observationContext)
						validationErrors.Store(i, errMsg)
						return
					}
				}

				// Validate decimal value
				if _, err := decimal.NewFromString(entry.Value); err != nil {
					validationErrors.Store(i, fmt.Sprintf("invalid decimal value: %s", entry.Value))
					return
				}
			}

			validateEntry()
			return nil
		})
	}

	// Wait for all validations to complete
	if err := g.Wait(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Errorf(codes.DeadlineExceeded, "batch validation timed out")
		}
		if errors.Is(err, context.Canceled) {
			return nil, status.Errorf(codes.Canceled, "batch validation was canceled")
		}
		return nil, status.Errorf(codes.Internal, "batch validation failed: %v", err)
	}

	// Process valid observations and build results
	results := make([]*pb.BatchObservationResult, len(req.Observations))
	var successCount, failureCount int32

	for i, entry := range req.Observations {
		// Check if this entry had a validation error
		if errMsgVal, hasError := validationErrors.Load(i); hasError {
			errMsg, ok := errMsgVal.(string)
			if !ok {
				errMsg = "unknown validation error"
			}
			results[i] = &pb.BatchObservationResult{
				Success:         false,
				ErrorMessage:    errMsg,
				ClientReference: entry.ClientReference,
				Index:           int32(i),
			}
			failureCount++
			continue
		}

		// Get cached dataset and source
		fetchMu.Lock()
		dataset := datasets[entry.DatasetCode]
		source := sources[entry.SourceCode]
		fetchMu.Unlock()

		// Compute resolution key
		observationContext := ToContextMap(entry.Attributes)
		resolutionKey, err := s.computeResolutionKey(dataset, observationContext)
		if err != nil {
			results[i] = &pb.BatchObservationResult{
				Success:         false,
				ErrorMessage:    fmt.Sprintf("resolution key error: %v", err),
				ClientReference: entry.ClientReference,
				Index:           int32(i),
			}
			failureCount++
			continue
		}

		// Parse value
		value, _ := decimal.NewFromString(entry.Value) // Already validated

		// Convert quality level
		qualityLevel := protoQualityLevelToDomain(entry.Quality)

		// Determine valid_to
		validTo := time.Now().Add(100 * 365 * 24 * time.Hour)
		if entry.ValidTo != nil {
			validTo = entry.ValidTo.AsTime()
		}

		// Create domain observation
		observation, err := domain.NewMarketPriceObservation(
			entry.DatasetCode,
			source.ID(),
			resolutionKey,
			value,
			dataset.Name(),
			entry.ObservedAt.AsTime(),
			entry.ValidFrom.AsTime(),
			validTo,
			uuid.New(),
			qualityLevel,
			source.TrustLevel(),
			domain.NewObservationContext(observationContext),
		)
		if err != nil {
			results[i] = &pb.BatchObservationResult{
				Success:         false,
				ErrorMessage:    fmt.Sprintf("observation creation error: %v", err),
				ClientReference: entry.ClientReference,
				Index:           int32(i),
			}
			failureCount++
			continue
		}

		// Record the observation
		if err := s.observationRepo.Record(ctx, observation); err != nil {
			results[i] = &pb.BatchObservationResult{
				Success:         false,
				ErrorMessage:    fmt.Sprintf("persistence error: %v", err),
				ClientReference: entry.ClientReference,
				Index:           int32(i),
			}
			failureCount++
			continue
		}

		// Publish event for ACTUAL and VERIFIED quality levels
		if s.eventPublisher != nil && shouldPublishObservationEvent(qualityLevel) {
			if obsPublisher, ok := s.eventPublisher.(ObservationEventPublisher); ok {
				if err := obsPublisher.PublishObservationRecorded(ctx, observation); err != nil {
					s.logger.Error("failed to publish batch observation event",
						"observation_id", observation.ID().String(),
						"error", err)
				}
			} else {
				event := mapObservationToProtoEvent(observation)
				if err := s.eventPublisher.Publish(ctx, event); err != nil {
					s.logger.Error("failed to publish batch observation event",
						"observation_id", observation.ID().String(),
						"error", err)
				}
			}
		}

		results[i] = &pb.BatchObservationResult{
			Success:         true,
			Observation:     domainObservationToProto(observation, entry.Attributes, dataset.Version()),
			ClientReference: entry.ClientReference,
			Index:           int32(i),
		}
		successCount++
	}

	s.logger.Info("batch observation recorded",
		"batch_id", batchID,
		"total", len(req.Observations),
		"success", successCount,
		"failure", failureCount)

	return &pb.RecordObservationBatchResponse{
		Results:      results,
		BatchId:      batchID,
		TotalCount:   int32(len(req.Observations)),
		SuccessCount: successCount,
		FailureCount: failureCount,
	}, nil
}

// getDatasetVersion looks up the version for a dataset by code.
// Returns 1 as fallback if the dataset cannot be found.
func (s *Server) getDatasetVersion(ctx context.Context, datasetCode string) int {
	dataset, err := s.dataSetRepo.FindByCode(ctx, datasetCode)
	if err != nil {
		s.logger.Warn("failed to get dataset version, using fallback",
			"dataset_code", datasetCode,
			"error", err)
		return 1 // Fallback to version 1
	}
	return dataset.Version()
}

// RetrieveObservation fetches a specific observation with bi-temporal query support.
// Returns NOT_FOUND if observation doesn't exist at the requested times.
func (s *Server) RetrieveObservation(ctx context.Context, req *pb.RetrieveObservationRequest) (*pb.RetrieveObservationResponse, error) {
	// Parse observation ID
	obsID, err := uuid.Parse(req.ObservationId)
	if err != nil {
		s.logger.Warn("invalid observation ID",
			"observation_id", req.ObservationId,
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid observation ID: %s", req.ObservationId)
	}

	// Retrieve observation by ID
	observation, err := s.observationRepo.FindByID(ctx, obsID)
	if err != nil {
		return nil, s.mapObservationDomainError(err, "RetrieveObservation", req.ObservationId)
	}

	// Apply knowledge_base_time filter if provided (bi-temporal query)
	if req.KnowledgeBaseTime != nil {
		knowledgeTime := req.KnowledgeBaseTime.AsTime()

		// Check if the observation was known at the requested time
		// An observation is "known" if it was created before the knowledge base time
		// and not yet superseded at that time
		if observation.CreatedAt().After(knowledgeTime) {
			s.logger.Debug("observation not known at requested time",
				"observation_id", req.ObservationId,
				"knowledge_base_time", knowledgeTime,
				"created_at", observation.CreatedAt())
			return nil, status.Errorf(codes.NotFound, "observation %s was not known at %v", req.ObservationId, knowledgeTime)
		}

		// Check if superseded before the knowledge base time
		if observation.SupersededAt() != nil && observation.SupersededAt().Before(knowledgeTime) {
			s.logger.Debug("observation was superseded before requested time",
				"observation_id", req.ObservationId,
				"knowledge_base_time", knowledgeTime,
				"superseded_at", observation.SupersededAt())
			return nil, status.Errorf(codes.NotFound, "observation %s was superseded before %v", req.ObservationId, knowledgeTime)
		}
	}

	// Get dataset version for proto conversion
	datasetVersion := s.getDatasetVersion(ctx, observation.DataSetCode())

	return &pb.RetrieveObservationResponse{
		Observation: domainObservationToProto(observation, nil, datasetVersion),
	}, nil
}

// ListObservations returns observations matching the filter criteria with cursor-based pagination.
// Supports filtering by data set, time ranges, quality level, and pagination.
func (s *Server) ListObservations(ctx context.Context, req *pb.ListObservationsRequest) (*pb.ListObservationsResponse, error) {
	// Apply pagination defaults
	pageSize := int(req.PageSize)
	if pageSize < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "page_size cannot be negative")
	}
	if pageSize == 0 {
		pageSize = 100 // Default
	}
	if pageSize > 1000 {
		pageSize = 1000 // Max from proto validation
	}

	// Build query from request filters
	query := domain.ObservationQuery{
		DataSetCode:       req.DatasetCode,
		IncludeSuperseded: req.IncludeSuperseded,
		Limit:             pageSize,
		PageToken:         req.PageToken,
	}

	// Apply resolution key filter
	if req.ResolutionKeyValue != "" {
		query.ResolutionKey = &req.ResolutionKeyValue
	}

	// Apply time range filters
	if req.ObservedFrom != nil {
		t := req.ObservedFrom.AsTime()
		query.ObservedAfter = &t
	}
	if req.ObservedTo != nil {
		t := req.ObservedTo.AsTime()
		query.ObservedBefore = &t
	}

	// Apply quality filter
	if req.QualityFilter != pb.QualityLevel_QUALITY_LEVEL_UNSPECIFIED {
		qualityLevel := protoQualityLevelToDomain(req.QualityFilter)
		query.QualityLevel = &qualityLevel
	}

	// Execute query
	observations, nextPageToken, err := s.observationRepo.Query(ctx, query)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidPageToken) {
			s.logger.Warn("invalid page token",
				"page_token", req.PageToken)
			return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: malformed cursor")
		}
		s.logger.Error("failed to query observations",
			"dataset_code", req.DatasetCode,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to query observations: %v", err)
	}

	// Get total count for pagination info
	totalCount, err := s.observationRepo.CountByDataset(ctx, req.DatasetCode, req.IncludeSuperseded)
	if err != nil {
		// Log warning but don't fail the request - 0 means unknown per proto spec
		s.logger.Warn("failed to get observation count",
			"dataset_code", req.DatasetCode,
			"error", err)
		totalCount = 0
	}

	// Get dataset version for proto conversion
	datasetVersion := s.getDatasetVersion(ctx, req.DatasetCode)

	// Convert to proto messages
	pbObservations := make([]*pb.MarketPriceObservation, len(observations))
	for i, obs := range observations {
		pbObservations[i] = domainObservationToProto(obs, nil, datasetVersion)
	}

	s.logger.Debug("listed observations",
		"dataset_code", req.DatasetCode,
		"count", len(pbObservations),
		"total_count", totalCount,
		"has_more", nextPageToken != "")

	return &pb.ListObservationsResponse{
		Observations:  pbObservations,
		NextPageToken: nextPageToken,
		TotalCount:    int32(totalCount),
	}, nil
}

// defaultResolutionKey is used when no resolution key can be computed.
const defaultResolutionKey = "default"

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

// mapObservationDomainError converts domain errors to appropriate gRPC status codes.
func (s *Server) mapObservationDomainError(err error, operation, identifier string) error {
	switch {
	case errors.Is(err, domain.ErrObservationNotFound):
		s.logger.Warn("observation not found",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.NotFound, "observation not found: %s", identifier)

	case errors.Is(err, domain.ErrDataSetNotFound):
		s.logger.Warn("dataset not found",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.NotFound, "dataset not found: %s", identifier)

	case errors.Is(err, domain.ErrDataSourceNotFound):
		s.logger.Warn("data source not found",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.NotFound, "data source not found: %s", identifier)

	case errors.Is(err, domain.ErrDataSetDeprecated):
		s.logger.Warn("dataset is deprecated",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.FailedPrecondition, "dataset is deprecated: %s", identifier)

	case errors.Is(err, domain.ErrInvalidTemporalBounds):
		s.logger.Warn("invalid temporal bounds",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.InvalidArgument, "invalid temporal bounds: valid_from must be before valid_to")

	case errors.Is(err, domain.ErrInvalidQualityLevel):
		s.logger.Warn("invalid quality level",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.InvalidArgument, "invalid quality level")

	case errors.Is(err, domain.ErrDataSetCodeRequired):
		s.logger.Warn("dataset code required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "dataset code is required")

	case errors.Is(err, domain.ErrSourceIDRequired):
		s.logger.Warn("source ID required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "source ID is required")

	case errors.Is(err, domain.ErrResolutionKeyRequired):
		s.logger.Warn("resolution key required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "resolution key is required")

	case errors.Is(err, domain.ErrUnitRequired):
		s.logger.Warn("unit required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "unit is required")

	case errors.Is(err, domain.ErrCausationIDRequired):
		s.logger.Warn("causation ID required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "causation ID is required")

	case errors.Is(err, domain.ErrInvalidTrustLevel):
		s.logger.Warn("invalid trust level",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "trust level must be between 0 and 100")

	default:
		s.logger.Error("internal error",
			"operation", operation,
			"identifier", identifier,
			"error", err)
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// shouldPublishObservationEvent determines if an observation event should be published.
// Only publish for ACTUAL and VERIFIED quality levels (not ESTIMATE).
func shouldPublishObservationEvent(quality domain.QualityLevel) bool {
	return quality == domain.QualityLevelActual || quality == domain.QualityLevelVerified
}

// protoQualityLevelToDomain converts proto QualityLevel to domain QualityLevel.
func protoQualityLevelToDomain(protoQuality pb.QualityLevel) domain.QualityLevel {
	switch protoQuality {
	case pb.QualityLevel_QUALITY_LEVEL_UNSPECIFIED:
		return domain.QualityLevelEstimate // Default to lowest quality
	case pb.QualityLevel_QUALITY_LEVEL_ESTIMATE:
		return domain.QualityLevelEstimate
	case pb.QualityLevel_QUALITY_LEVEL_PROVISIONAL:
		// Map PROVISIONAL to ESTIMATE (domain doesn't have PROVISIONAL)
		return domain.QualityLevelEstimate
	case pb.QualityLevel_QUALITY_LEVEL_ACTUAL:
		return domain.QualityLevelActual
	case pb.QualityLevel_QUALITY_LEVEL_REVISED:
		// Map REVISED to VERIFIED (corrected values are verified)
		return domain.QualityLevelVerified
	default:
		return domain.QualityLevelEstimate // Default to lowest quality
	}
}

// domainQualityLevelToProto converts domain QualityLevel to proto QualityLevel.
func domainQualityLevelToProto(domainQuality domain.QualityLevel) pb.QualityLevel {
	switch domainQuality {
	case domain.QualityLevelEstimate:
		return pb.QualityLevel_QUALITY_LEVEL_ESTIMATE
	case domain.QualityLevelActual:
		return pb.QualityLevel_QUALITY_LEVEL_ACTUAL
	case domain.QualityLevelVerified:
		// Domain VERIFIED maps to proto ACTUAL (highest reliable quality)
		return pb.QualityLevel_QUALITY_LEVEL_ACTUAL
	default:
		return pb.QualityLevel_QUALITY_LEVEL_UNSPECIFIED
	}
}

// domainObservationToProto converts a domain MarketPriceObservation to proto.
// The datasetVersion parameter should be obtained from the dataset definition.
// If the version cannot be determined, pass 1 as a fallback.
func domainObservationToProto(obs domain.MarketPriceObservation, attributes []*quantityv1.AttributeEntry, datasetVersion int) *pb.MarketPriceObservation {
	pbObs := &pb.MarketPriceObservation{
		Id:                 obs.ID().String(),
		DatasetCode:        obs.DataSetCode(),
		DatasetVersion:     int32(datasetVersion),
		ResolutionKeyValue: obs.ResolutionKey(),
		ObservedAt:         timestamppb.New(obs.ObservedAt()),
		ValidFrom:          timestamppb.New(obs.ValidFrom()),
		ValidTo:            timestamppb.New(obs.ValidTo()),
		Value:              obs.Value().String(),
		Quality:            domainQualityLevelToProto(obs.QualityLevel()),
		SourceId:           obs.SourceID().String(),
		CreatedAt:          timestamppb.New(obs.CreatedAt()),
		Attributes:         attributes,
	}

	// Set optional superseded fields
	if obs.SupersededAt() != nil {
		pbObs.SupersededAt = timestamppb.New(*obs.SupersededAt())
	}
	if obs.SupersededBy() != nil {
		pbObs.SupersededById = obs.SupersededBy().String()
	}

	return pbObs
}
