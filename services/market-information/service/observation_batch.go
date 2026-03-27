// Package service provides gRPC service implementations for the Market Information service.
// BIAN Service Domain: Market Information Management
//
// This file implements the batch observation recording operation.
package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

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
