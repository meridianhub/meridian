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
// RecordObservationBatch records multiple observations with parallel validation.
// Returns partial success with details for each observation.
func (s *Server) RecordObservationBatch(ctx context.Context, req *pb.RecordObservationBatchRequest) (*pb.RecordObservationBatchResponse, error) {
	if len(req.Observations) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "at least one observation is required")
	}

	batchID := req.BatchId
	if batchID == "" {
		batchID = uuid.New().String()
	}

	bc := &batchContext{
		server:   s,
		datasets: make(map[string]domain.DataSetDefinition),
		sources:  make(map[string]domain.DataSource),
	}

	validationErrors, err := bc.validateBatchEntries(ctx, req.Observations)
	if err != nil {
		return nil, err
	}

	results, successCount, failureCount := bc.processValidObservations(ctx, req.Observations, validationErrors)

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

// batchContext holds shared state for batch observation processing.
type batchContext struct {
	server   *Server
	datasets map[string]domain.DataSetDefinition
	sources  map[string]domain.DataSource
	fetchMu  sync.Mutex
}

// validateBatchEntries runs parallel CEL validation on all entries.
// Returns a sync.Map of index -> error message for failed entries.
func (bc *batchContext) validateBatchEntries(ctx context.Context, entries []*pb.BatchObservationEntry) (*sync.Map, error) {
	validationErrors := &sync.Map{}
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(50)

	for i, entry := range entries {
		i, entry := i, entry
		g.Go(func() error {
			if gCtx.Err() != nil {
				return gCtx.Err()
			}
			bc.validateSingleEntry(gCtx, i, entry, validationErrors)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Errorf(codes.DeadlineExceeded, "batch validation timed out")
		}
		if errors.Is(err, context.Canceled) {
			return nil, status.Errorf(codes.Canceled, "batch validation was canceled")
		}
		return nil, status.Errorf(codes.Internal, "batch validation failed: %v", err)
	}
	return validationErrors, nil
}

// validateSingleEntry validates one observation entry, storing any error in validationErrors.
func (bc *batchContext) validateSingleEntry(ctx context.Context, idx int, entry *pb.BatchObservationEntry, validationErrors *sync.Map) {
	dataset, err := bc.fetchDataset(ctx, entry.DatasetCode)
	if err != nil {
		validationErrors.Store(idx, fmt.Sprintf("dataset not found: %s", entry.DatasetCode))
		return
	}
	if dataset.Status() != domain.DataSetStatusActive {
		validationErrors.Store(idx, fmt.Sprintf("dataset %s is not active", entry.DatasetCode))
		return
	}

	source, err := bc.fetchSource(ctx, entry.SourceCode)
	if err != nil {
		validationErrors.Store(idx, fmt.Sprintf("source not found: %s", entry.SourceCode))
		return
	}
	if !source.IsActive() {
		validationErrors.Store(idx, fmt.Sprintf("source %s is not active", entry.SourceCode))
		return
	}

	if entry.ObservedAt == nil {
		validationErrors.Store(idx, "observed_at timestamp is required")
		return
	}
	if entry.ValidFrom == nil {
		validationErrors.Store(idx, "valid_from timestamp is required")
		return
	}

	if errMsg := bc.validateCEL(dataset, source, entry); errMsg != "" {
		validationErrors.Store(idx, errMsg)
		return
	}

	if _, err := decimal.NewFromString(entry.Value); err != nil {
		validationErrors.Store(idx, fmt.Sprintf("invalid decimal value: %s", entry.Value))
	}
}

// validateCEL runs CEL validation if configured for the dataset.
func (bc *batchContext) validateCEL(dataset domain.DataSetDefinition, source domain.DataSource, entry *pb.BatchObservationEntry) string {
	if bc.server.celValidator == nil || dataset.ValidationExpression() == "" {
		return ""
	}

	validTo := time.Now().Add(100 * 365 * 24 * time.Hour)
	if entry.ValidTo != nil {
		validTo = entry.ValidTo.AsTime()
	}

	observationContext := ToContextMap(entry.Attributes)
	validationInput := ValidationInput{
		Value:              entry.Value,
		ObservationContext: observationContext,
		ObservedAt:         entry.ObservedAt.AsTime(),
		ValidFrom:          entry.ValidFrom.AsTime(),
		ValidTo:            validTo,
		SourceID:           source.ID().String(),
		Quality:            int(entry.Quality),
	}

	prg, err := bc.server.celValidator.CompileValidation(dataset.ValidationExpression())
	if err != nil {
		return fmt.Sprintf("validation expression error: %v", err)
	}
	valid, err := bc.server.celValidator.EvaluateValidation(prg, validationInput)
	if err != nil {
		return fmt.Sprintf("validation evaluation error: %v", err)
	}
	if !valid {
		return bc.server.generateValidationErrorMessage(dataset, entry.Value, observationContext)
	}
	return ""
}

func (bc *batchContext) fetchDataset(ctx context.Context, code string) (domain.DataSetDefinition, error) {
	bc.fetchMu.Lock()
	ds, exists := bc.datasets[code]
	bc.fetchMu.Unlock()
	if exists {
		return ds, nil
	}

	ds, err := bc.server.dataSetRepo.FindByCode(ctx, code)
	if err != nil {
		return ds, err
	}
	bc.fetchMu.Lock()
	bc.datasets[code] = ds
	bc.fetchMu.Unlock()
	return ds, nil
}

func (bc *batchContext) fetchSource(ctx context.Context, code string) (domain.DataSource, error) {
	bc.fetchMu.Lock()
	src, exists := bc.sources[code]
	bc.fetchMu.Unlock()
	if exists {
		return src, nil
	}

	src, err := bc.server.sourceRepo.FindByCode(ctx, code)
	if err != nil {
		return src, err
	}
	bc.fetchMu.Lock()
	bc.sources[code] = src
	bc.fetchMu.Unlock()
	return src, nil
}

// processValidObservations processes entries that passed validation and persists them.
func (bc *batchContext) processValidObservations(ctx context.Context, entries []*pb.BatchObservationEntry, validationErrors *sync.Map) ([]*pb.BatchObservationResult, int32, int32) {
	results := make([]*pb.BatchObservationResult, len(entries))
	var successCount, failureCount int32

	for i, entry := range entries {
		if errMsgVal, hasError := validationErrors.Load(i); hasError {
			errMsg, _ := errMsgVal.(string)
			if errMsg == "" {
				errMsg = "unknown validation error"
			}
			results[i] = &pb.BatchObservationResult{
				Success: false, ErrorMessage: errMsg,
				ClientReference: entry.ClientReference, Index: int32(i),
			}
			failureCount++
			continue
		}

		result := bc.processSingleObservation(ctx, entry, i)
		results[i] = result
		if result.Success {
			successCount++
		} else {
			failureCount++
		}
	}
	return results, successCount, failureCount
}

func (bc *batchContext) processSingleObservation(ctx context.Context, entry *pb.BatchObservationEntry, idx int) *pb.BatchObservationResult {
	bc.fetchMu.Lock()
	dataset := bc.datasets[entry.DatasetCode]
	source := bc.sources[entry.SourceCode]
	bc.fetchMu.Unlock()

	observationContext := ToContextMap(entry.Attributes)
	resolutionKey, err := bc.server.computeResolutionKey(dataset, observationContext)
	if err != nil {
		return &pb.BatchObservationResult{
			Success: false, ErrorMessage: fmt.Sprintf("resolution key error: %v", err),
			ClientReference: entry.ClientReference, Index: int32(idx),
		}
	}

	value, _ := decimal.NewFromString(entry.Value)
	qualityLevel := protoQualityLevelToDomain(entry.Quality)
	validTo := time.Now().Add(100 * 365 * 24 * time.Hour)
	if entry.ValidTo != nil {
		validTo = entry.ValidTo.AsTime()
	}

	observation, err := domain.NewMarketPriceObservation(
		entry.DatasetCode, source.ID(), resolutionKey, value, dataset.Name(),
		entry.ObservedAt.AsTime(), entry.ValidFrom.AsTime(), validTo,
		uuid.New(), qualityLevel, source.TrustLevel(),
		domain.NewObservationContext(observationContext),
	)
	if err != nil {
		return &pb.BatchObservationResult{
			Success: false, ErrorMessage: fmt.Sprintf("observation creation error: %v", err),
			ClientReference: entry.ClientReference, Index: int32(idx),
		}
	}

	if err := bc.server.observationRepo.Record(ctx, observation); err != nil {
		return &pb.BatchObservationResult{
			Success: false, ErrorMessage: fmt.Sprintf("persistence error: %v", err),
			ClientReference: entry.ClientReference, Index: int32(idx),
		}
	}

	bc.publishObservationEvent(ctx, observation, qualityLevel)

	return &pb.BatchObservationResult{
		Success:         true,
		Observation:     domainObservationToProto(observation, entry.Attributes, dataset.Version()),
		ClientReference: entry.ClientReference,
		Index:           int32(idx),
	}
}

func (bc *batchContext) publishObservationEvent(ctx context.Context, observation domain.MarketPriceObservation, qualityLevel domain.QualityLevel) {
	if bc.server.eventPublisher == nil || !shouldPublishObservationEvent(qualityLevel) {
		return
	}
	if obsPublisher, ok := bc.server.eventPublisher.(ObservationEventPublisher); ok {
		if err := obsPublisher.PublishObservationRecorded(ctx, observation); err != nil {
			bc.server.logger.Error("failed to publish batch observation event",
				"observation_id", observation.ID().String(), "error", err)
		}
	} else {
		event := mapObservationToProtoEvent(observation)
		if err := bc.server.eventPublisher.Publish(ctx, event); err != nil {
			bc.server.logger.Error("failed to publish batch observation event",
				"observation_id", observation.ID().String(), "error", err)
		}
	}
}
