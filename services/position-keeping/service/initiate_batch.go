package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

const (
	// MaxBatchSize is the maximum number of logs that can be created in a single batch
	MaxBatchSize = 10_000

	// BatchProcessingTimeout is the maximum time allowed for batch processing
	BatchProcessingTimeout = 5 * time.Minute
)

// ErrAccountIDRequired is returned when account_id is missing
var ErrAccountIDRequired = errors.New("account_id is required")

// InitiateFinancialPositionLogBatch creates multiple financial position logs atomically.
//
// Design decisions:
//   - Atomic transactions: All logs are created in a single database transaction via CreateBatch.
//     Either all succeed or all fail (no partial batches).
//   - Parallel validation: Individual log validation is done in parallel using goroutines.
//   - Idempotency: Entire batch is idempotent using a single idempotency key.
//   - Error handling: Validation errors are collected and returned; database errors cause rollback.
//   - Event publishing: Single BulkTransactionCaptured event published for the entire batch.
func (s *PositionKeepingService) InitiateFinancialPositionLogBatch(
	ctx context.Context,
	req *positionkeepingv1.InitiateFinancialPositionLogBatchRequest,
) (resp *positionkeepingv1.InitiateFinancialPositionLogBatchResponse, err error) {
	// Create context with timeout for the entire batch operation
	batchCtx, cancel := context.WithTimeout(ctx, BatchProcessingTimeout)
	defer cancel()

	// Validate request
	if err := validateBatchRequest(req); err != nil {
		return nil, err
	}

	// Check idempotency and acquire lock if key provided
	idempotencyKey, cachedResponse, err := s.checkBatchIdempotencyAndAcquireLock(batchCtx, req)
	if err != nil {
		return nil, err
	}
	if cachedResponse != nil {
		return cachedResponse, nil
	}

	// Clean up pending idempotency key on error
	if idempotencyKey != nil {
		defer func() {
			if err != nil {
				_ = s.idempotency.Delete(batchCtx, *idempotencyKey)
			}
		}()
	}

	// Generate batch ID if not provided
	batchID := uuid.New()
	if req.BatchId != "" {
		batchID, err = uuid.Parse(req.BatchId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid batch_id: %v", err)
		}
	}

	// Process batch: validate and create domain logs in parallel
	logs, results, err := s.processBatchRequests(batchCtx, req.Requests)
	if err != nil {
		return nil, err
	}

	// Count successes and failures
	// Safe conversion: batch size validated to be <= MaxBatchSize (10,000)
	totalCount := int32(len(req.Requests)) // #nosec G115
	successCount := int32(0)
	failureCount := int32(0)
	successfulLogs := make([]*domain.FinancialPositionLog, 0, len(logs))
	logIDs := make([]uuid.UUID, 0, len(logs))

	for i, result := range results {
		if result.Success {
			successCount++
			// Defensive nil check: logs[i] should always be non-nil when result.Success is true,
			// but we guard against potential race conditions or future code changes
			if logs[i] != nil {
				successfulLogs = append(successfulLogs, logs[i])
				logIDs = append(logIDs, logs[i].LogID)
			}
		} else {
			failureCount++
		}
	}

	// If there were validation failures, return early with detailed errors
	if failureCount > 0 {
		return &positionkeepingv1.InitiateFinancialPositionLogBatchResponse{
			Results:      results,
			BatchId:      batchID.String(),
			TotalCount:   totalCount,
			SuccessCount: successCount,
			FailureCount: failureCount,
		}, nil
	}

	// Persist all logs atomically using CreateBatch
	if err := s.repository.CreateBatch(batchCtx, successfulLogs); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to persist batch: %v", err)
	}

	// Publish BulkTransactionCaptured event to outbox.
	// NOTE: CreateBatch does not support outbox writes within its internal transaction.
	// The event is written separately here; the outbox worker delivers it to Kafka.
	// At-least-once delivery is guaranteed by the outbox pattern.
	if len(successfulLogs) > 0 {
		// Safe conversion: successfulLogs length <= MaxBatchSize (10,000)
		transactionCount := int32(len(successfulLogs)) // #nosec G115
		event := &domain.BulkTransactionCaptured{
			BatchID:          batchID,
			TransactionCount: transactionCount,
			LogIDs:           logIDs,
			Source:           domain.TransactionSourceImported,
			CorrelationID:    fmt.Sprintf("batch-%s", batchID.String()),
			Timestamp:        time.Now().UTC(),
			Version:          1,
		}
		if err := s.eventPublisher.Publish(batchCtx, event); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to publish batch event: %v", err)
		}
	}

	// Store idempotency result if key was provided
	if idempotencyKey != nil {
		// Serialize the complete response for caching (including full Log proto messages)
		// Use protojson for proper proto message serialization
		responseProto := &positionkeepingv1.InitiateFinancialPositionLogBatchResponse{
			Results:      results,
			BatchId:      batchID.String(),
			TotalCount:   totalCount,
			SuccessCount: successCount,
			FailureCount: failureCount,
		}

		marshaler := protojson.MarshalOptions{
			UseProtoNames:   true,
			EmitUnpopulated: false,
		}
		resultData, err := marshaler.Marshal(responseProto)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to marshal idempotency result: %v", err)
		}

		if err := s.idempotency.StoreResult(batchCtx, idempotency.Result{
			Key:         *idempotencyKey,
			Status:      idempotency.StatusCompleted,
			Data:        resultData,
			CompletedAt: time.Now(),
			TTL:         24 * time.Hour,
		}); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to store idempotency result: %v", err)
		}
	}

	// Build successful response
	resp = &positionkeepingv1.InitiateFinancialPositionLogBatchResponse{
		Results:      results,
		BatchId:      batchID.String(),
		TotalCount:   totalCount,
		SuccessCount: successCount,
		FailureCount: failureCount,
	}
	return resp, nil
}

// validateBatchRequest validates the batch request
func validateBatchRequest(req *positionkeepingv1.InitiateFinancialPositionLogBatchRequest) error {
	if len(req.Requests) == 0 {
		return status.Error(codes.InvalidArgument, "requests cannot be empty")
	}

	if len(req.Requests) > MaxBatchSize {
		return status.Errorf(codes.InvalidArgument, "batch size %d exceeds maximum of %d", len(req.Requests), MaxBatchSize)
	}

	return nil
}

// processBatchRequests validates and creates domain logs in parallel
func (s *PositionKeepingService) processBatchRequests(
	ctx context.Context,
	requests []*positionkeepingv1.BatchInitiateRequest,
) ([]*domain.FinancialPositionLog, []*positionkeepingv1.BatchInitiateResult, error) {
	logs := make([]*domain.FinancialPositionLog, len(requests))
	results := make([]*positionkeepingv1.BatchInitiateResult, len(requests))

	// Use worker pool to process requests in parallel
	// Limit concurrency to avoid overwhelming the system
	const maxWorkers = 100
	semaphore := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex // Protect shared slices

	for i, req := range requests {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return nil, nil, status.Errorf(codes.Canceled, "batch processing cancelled: %v", ctx.Err())
		default:
		}

		wg.Add(1)
		semaphore <- struct{}{} // Acquire semaphore

		go func(index int, batchReq *positionkeepingv1.BatchInitiateRequest) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release semaphore

			result := &positionkeepingv1.BatchInitiateResult{
				AccountId: batchReq.AccountId,
			}

			// Validate and create domain log
			log, err := s.createDomainLogFromBatchRequest(batchReq)
			if err != nil {
				result.Success = false
				result.ErrorMessage = err.Error()
			} else {
				result.Success = true
				result.Log = toProtoFinancialPositionLog(log)
			}

			mu.Lock()
			if log != nil {
				logs[index] = log
			}
			results[index] = result
			mu.Unlock()
		}(i, req)
	}

	wg.Wait()

	return logs, results, nil
}

// createDomainLogFromBatchRequest creates a domain log from a batch request
func (s *PositionKeepingService) createDomainLogFromBatchRequest(
	req *positionkeepingv1.BatchInitiateRequest,
) (*domain.FinancialPositionLog, error) {
	// Validate account ID
	if req.AccountId == "" {
		return nil, ErrAccountIDRequired
	}

	// Convert initial entry from proto to domain if provided
	var initialEntry *domain.TransactionLogEntry
	var err error
	if req.InitialEntry != nil {
		initialEntry, err = protoEntryToDomain(req.InitialEntry)
		if err != nil {
			return nil, fmt.Errorf("invalid initial entry: %w", err)
		}
	}

	// Convert lineage from proto to domain if provided
	var lineage *domain.TransactionLineage
	if req.TransactionLineage != nil {
		lineage, err = protoLineageToDomain(req.TransactionLineage)
		if err != nil {
			return nil, fmt.Errorf("invalid transaction lineage: %w", err)
		}
	}

	// Create domain log
	log, err := domain.NewFinancialPositionLog(req.AccountId, initialEntry, lineage)
	if err != nil {
		return nil, fmt.Errorf("failed to create log: %w", err)
	}

	return log, nil
}

// checkBatchIdempotencyAndAcquireLock checks for completed batch operations and acquires a pending lock
func (s *PositionKeepingService) checkBatchIdempotencyAndAcquireLock(
	ctx context.Context,
	req *positionkeepingv1.InitiateFinancialPositionLogBatchRequest,
) (*idempotency.Key, *positionkeepingv1.InitiateFinancialPositionLogBatchResponse, error) {
	// No idempotency key provided
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, nil, nil
	}

	// Require batch_id when idempotency key is provided to ensure deterministic entity ID
	if req.BatchId == "" {
		return nil, nil, status.Error(codes.InvalidArgument, "batch_id is required when idempotency_key is provided")
	}

	// Use batch_id as deterministic entity ID
	batchID := req.BatchId

	key := idempotency.Key{
		Namespace: "position-keeping",
		Operation: "initiate-batch",
		EntityID:  batchID,
		RequestID: req.IdempotencyKey.Key,
	}

	// Check if operation was already completed
	result, err := s.idempotency.Check(ctx, key)
	if err == nil && result.Status == idempotency.StatusCompleted {
		// Return cached result - deserialize complete response with full Log data
		cachedResponse := &positionkeepingv1.InitiateFinancialPositionLogBatchResponse{}
		unmarshaler := protojson.UnmarshalOptions{
			DiscardUnknown: true,
		}
		if err := unmarshaler.Unmarshal(result.Data, cachedResponse); err != nil {
			return nil, nil, status.Errorf(codes.Internal, "failed to decode cached idempotency response: %v", err)
		}

		return &key, cachedResponse, nil
	}

	// Mark operation as pending
	if err := s.idempotency.MarkPending(ctx, key, 10*time.Minute); err != nil {
		return nil, nil, status.Errorf(codes.Internal, "failed to mark batch operation as pending: %v", err)
	}

	return &key, nil, nil
}
