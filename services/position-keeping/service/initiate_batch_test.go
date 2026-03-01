package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

var (
	errNotFound = errors.New("not found")
	errDBFailed = errors.New("database connection failed")
)

// TestInitiateFinancialPositionLogBatch_Success tests successful batch creation
func TestInitiateFinancialPositionLogBatch_Success(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{
			{AccountId: "ACC001"},
			{AccountId: "ACC002"},
			{AccountId: "ACC003"},
		},
	}

	// Mock CreateBatch
	mockRepo.On("CreateBatch", mock.Anything, mock.AnythingOfType("[]*domain.FinancialPositionLog")).
		Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int32(3), resp.TotalCount)
	assert.Equal(t, int32(3), resp.SuccessCount)
	assert.Equal(t, int32(0), resp.FailureCount)
	assert.Len(t, resp.Results, 3)
	assert.NotEmpty(t, resp.BatchId, "Batch ID should be set")

	// Verify all results are successful
	for i, result := range resp.Results {
		assert.True(t, result.Success, "Result %d should be successful", i)
		assert.NotNil(t, result.Log, "Result %d should have a log", i)
		assert.Empty(t, result.ErrorMessage, "Result %d should have no error", i)
	}

	// Verify event was published
	events := mockEventPublisher.GetPublishedEvents()
	assert.Len(t, events, 1, "Expected BulkTransactionCaptured event")

	mockRepo.AssertExpectations(t)
}

// TestInitiateFinancialPositionLogBatch_WithInitialEntry tests batch with initial entries
func TestInitiateFinancialPositionLogBatch_WithInitialEntry(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{
			{
				AccountId: "ACC001",
				InitialEntry: &positionkeepingv1.TransactionLogEntry{
					EntryId:       uuid.NewString(),
					TransactionId: uuid.NewString(),
					AccountId:     "ACC001",
					Amount: &commonv1.MoneyAmount{
						Amount: &money.Money{
							CurrencyCode: "GBP",
							Units:        100,
							Nanos:        0,
						},
					},
					Direction:   commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
					Timestamp:   timestamppb.Now(),
					Description: "Test transaction",
					Reference:   "REF-001",
				},
			},
			{
				AccountId: "ACC002",
				InitialEntry: &positionkeepingv1.TransactionLogEntry{
					EntryId:       uuid.NewString(),
					TransactionId: uuid.NewString(),
					AccountId:     "ACC002",
					Amount: &commonv1.MoneyAmount{
						Amount: &money.Money{
							CurrencyCode: "GBP",
							Units:        200,
							Nanos:        0,
						},
					},
					Direction:   commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
					Timestamp:   timestamppb.Now(),
					Description: "Test transaction 2",
					Reference:   "REF-002",
				},
			},
		},
	}

	// Mock CreateBatch
	mockRepo.On("CreateBatch", mock.Anything, mock.AnythingOfType("[]*domain.FinancialPositionLog")).
		Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int32(2), resp.TotalCount)
	assert.Equal(t, int32(2), resp.SuccessCount)
	assert.Equal(t, int32(0), resp.FailureCount)

	// Verify logs have transaction entries
	for i, result := range resp.Results {
		assert.True(t, result.Success, "Result %d should be successful", i)
		assert.NotNil(t, result.Log, "Result %d should have a log", i)
		assert.Len(t, result.Log.TransactionLogEntries, 1, "Result %d should have 1 transaction entry", i)
	}

	mockRepo.AssertExpectations(t)
}

// TestInitiateFinancialPositionLogBatch_EmptyRequest tests validation of empty request
func TestInitiateFinancialPositionLogBatch_EmptyRequest(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{},
	}

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "requests cannot be empty")
}

// TestInitiateFinancialPositionLogBatch_ExceedsMaxSize tests validation of max batch size
func TestInitiateFinancialPositionLogBatch_ExceedsMaxSize(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	// Create 10,001 requests (exceeds max of 10,000)
	requests := make([]*positionkeepingv1.BatchInitiateRequest, 10001)
	for i := range requests {
		requests[i] = &positionkeepingv1.BatchInitiateRequest{
			AccountId: uuid.NewString(),
		}
	}

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: requests,
	}

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "exceeds maximum")
}

// TestInitiateFinancialPositionLogBatch_PartialValidationFailures tests mixed success/failure
func TestInitiateFinancialPositionLogBatch_PartialValidationFailures(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{
			{AccountId: "ACC001"}, // Valid
			{AccountId: ""},       // Invalid - empty account ID
			{AccountId: "ACC003"}, // Valid
		},
	}

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.NoError(t, err, "Partial failures should not return error")
	require.NotNil(t, resp)
	assert.Equal(t, int32(3), resp.TotalCount)
	assert.Equal(t, int32(2), resp.SuccessCount)
	assert.Equal(t, int32(1), resp.FailureCount)

	// Verify individual results
	assert.True(t, resp.Results[0].Success)
	assert.False(t, resp.Results[1].Success)
	assert.NotEmpty(t, resp.Results[1].ErrorMessage, "Failed result should have error message")
	assert.True(t, resp.Results[2].Success)

	// No database calls should be made for partial failures
	mockRepo.AssertNotCalled(t, "CreateBatch")
}

// TestInitiateFinancialPositionLogBatch_Idempotency tests idempotent batch creation
func TestInitiateFinancialPositionLogBatch_Idempotency(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	batchID := uuid.NewString()
	idempotencyKey := uuid.NewString()

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{
			{AccountId: "ACC001"},
			{AccountId: "ACC002"},
		},
		BatchId: batchID,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: idempotencyKey,
		},
	}

	// Mock idempotency check - operation already completed
	cachedData := map[string]interface{}{
		"batch_id":      batchID,
		"success_count": int32(2),
		"log_ids":       []string{uuid.NewString(), uuid.NewString()},
	}
	cachedJSON, _ := json.Marshal(cachedData)
	cachedResult := &idempotency.Result{
		Status: idempotency.StatusCompleted,
		Data:   cachedJSON,
	}

	mockIdempotency.On("Check", mock.Anything, mock.AnythingOfType("idempotency.Key")).
		Return(cachedResult, nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.NoError(t, err, "Expected no error for idempotent request")
	require.NotNil(t, resp)
	assert.Equal(t, batchID, resp.BatchId)
	assert.Equal(t, int32(2), resp.SuccessCount)

	// Verify no database calls for cached result
	mockRepo.AssertNotCalled(t, "CreateBatch")
	mockIdempotency.AssertExpectations(t)
}

// TestInitiateFinancialPositionLogBatch_LargeBatch tests handling of large batch
func TestInitiateFinancialPositionLogBatch_LargeBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large batch test in short mode")
	}

	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	// Create 1,000 requests
	requests := make([]*positionkeepingv1.BatchInitiateRequest, 1000)
	for i := range requests {
		requests[i] = &positionkeepingv1.BatchInitiateRequest{
			AccountId: uuid.NewString(),
		}
	}

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: requests,
	}

	// Mock CreateBatch
	mockRepo.On("CreateBatch", mock.Anything, mock.AnythingOfType("[]*domain.FinancialPositionLog")).
		Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int32(1000), resp.TotalCount)
	assert.Equal(t, int32(1000), resp.SuccessCount)
	assert.Equal(t, int32(0), resp.FailureCount)

	mockRepo.AssertExpectations(t)
}

// TestInitiateFinancialPositionLogBatch_CustomBatchID tests client-provided batch ID
func TestInitiateFinancialPositionLogBatch_CustomBatchID(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	customBatchID := uuid.NewString()
	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{
			{AccountId: "ACC001"},
		},
		BatchId: customBatchID,
	}

	// Mock CreateBatch
	mockRepo.On("CreateBatch", mock.Anything, mock.AnythingOfType("[]*domain.FinancialPositionLog")).
		Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, customBatchID, resp.BatchId, "Should use client-provided batch ID")

	mockRepo.AssertExpectations(t)
}

// TestInitiateFinancialPositionLogBatch_InvalidBatchID tests validation of invalid batch ID
func TestInitiateFinancialPositionLogBatch_InvalidBatchID(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{
			{AccountId: "ACC001"},
		},
		BatchId: "not-a-valid-uuid",
	}

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid batch_id")
}

// TestInitiateFinancialPositionLogBatch_IdempotencyRequiresBatchID tests that batch_id is required with idempotency_key
func TestInitiateFinancialPositionLogBatch_IdempotencyRequiresBatchID(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{
			{AccountId: "ACC001"},
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
		// Intentionally omit BatchId
	}

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "batch_id is required when idempotency_key is provided")
}

// TestInitiateFinancialPositionLogBatch_ConcurrentBatches tests concurrent batch processing
func TestInitiateFinancialPositionLogBatch_ConcurrentBatches(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrent batch test in short mode")
	}

	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	// Mock CreateBatch to be concurrent-safe
	mockRepo.On("CreateBatch", mock.Anything, mock.AnythingOfType("[]*domain.FinancialPositionLog")).
		Return(nil)

	// Act - Process 10 batches concurrently
	const numBatches = 10
	results := make(chan error, numBatches)

	for i := 0; i < numBatches; i++ {
		go func() {
			req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
				Requests: []*positionkeepingv1.BatchInitiateRequest{
					{AccountId: uuid.NewString()},
					{AccountId: uuid.NewString()},
				},
			}
			_, err := svc.InitiateFinancialPositionLogBatch(ctx, req)
			results <- err
		}()
	}

	// Assert
	for i := 0; i < numBatches; i++ {
		err := <-results
		assert.NoError(t, err, "Batch %d should succeed", i)
	}

	mockRepo.AssertExpectations(t)
}

// Benchmark tests
func BenchmarkInitiateFinancialPositionLogBatch_SmallBatch(b *testing.B) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(b, mockRepo, mockEventPublisher, mockIdempotency)

	mockRepo.On("CreateBatch", ctx, mock.Anything).Return(nil)

	requests := make([]*positionkeepingv1.BatchInitiateRequest, 10)
	for i := range requests {
		requests[i] = &positionkeepingv1.BatchInitiateRequest{
			AccountId: uuid.NewString(),
		}
	}

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: requests,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.InitiateFinancialPositionLogBatch(ctx, req)
	}
}

func BenchmarkInitiateFinancialPositionLogBatch_MediumBatch(b *testing.B) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(b, mockRepo, mockEventPublisher, mockIdempotency)

	mockRepo.On("CreateBatch", ctx, mock.Anything).Return(nil)

	requests := make([]*positionkeepingv1.BatchInitiateRequest, 100)
	for i := range requests {
		requests[i] = &positionkeepingv1.BatchInitiateRequest{
			AccountId: uuid.NewString(),
		}
	}

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: requests,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.InitiateFinancialPositionLogBatch(ctx, req)
	}
}

func BenchmarkInitiateFinancialPositionLogBatch_LargeBatch(b *testing.B) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(b, mockRepo, mockEventPublisher, mockIdempotency)

	mockRepo.On("CreateBatch", ctx, mock.Anything).Return(nil)

	requests := make([]*positionkeepingv1.BatchInitiateRequest, 1000)
	for i := range requests {
		requests[i] = &positionkeepingv1.BatchInitiateRequest{
			AccountId: uuid.NewString(),
		}
	}

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: requests,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.InitiateFinancialPositionLogBatch(ctx, req)
	}
}

func TestInitiateFinancialPositionLogBatch_DatabaseFailureWithIdempotencyCleanup(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	batchID := uuid.New()
	idempotencyKey := uuid.NewString()

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{
			{AccountId: "ACC001"},
			{AccountId: "ACC002"},
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: idempotencyKey,
		},
		BatchId: batchID.String(),
	}

	// Setup expectations - idempotency check returns not found (first request)
	mockIdempotency.On("Check", mock.Anything, mock.Anything).
		Return(nil, errNotFound)

	// Mark as pending succeeds
	mockIdempotency.On("MarkPending", mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	// Database failure - CreateBatch returns error
	mockRepo.On("CreateBatch", mock.Anything, mock.Anything).
		Return(errDBFailed)

	// Expect idempotency key to be deleted on error
	mockIdempotency.On("Delete", mock.Anything, mock.Anything).
		Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to persist batch")

	// Verify idempotency key was deleted (rollback)
	mockIdempotency.AssertCalled(t, "Delete", mock.Anything, mock.Anything)

	// Verify no event was published (since batch failed)
	assert.Empty(t, mockEventPublisher.GetPublishedEvents())
}

func TestInitiateFinancialPositionLogBatch_ContextCancellation(t *testing.T) {
	// Arrange
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{
			{AccountId: "ACC001"},
			{AccountId: "ACC002"},
		},
	}

	// Act
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.Canceled, st.Code())
	assert.Contains(t, st.Message(), "batch processing cancelled")

	// Verify no database operations were attempted
	mockRepo.AssertNotCalled(t, "CreateBatch")
	mockIdempotency.AssertNotCalled(t, "StoreResult")
}

// TestInitiateFinancialPositionLogBatch_NilPointerRegressionOnValidationFailure is a regression test
// for a bug where nil logs were stored in the logs slice when validation failed, which could
// lead to nil pointer panics when accessing logs[i] in the downstream success counting loop.
// This test specifically verifies that mixed valid/invalid batches don't cause panics.
func TestInitiateFinancialPositionLogBatch_NilPointerRegressionOnValidationFailure(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockMeasurementRepo := new(MockMeasurementRepository)

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, newTestOutboxPublisher(t))
	require.NoError(t, err)

	// Create a batch with alternating valid and invalid entries to maximize
	// the chance of triggering race conditions in the parallel processing
	req := &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{
		Requests: []*positionkeepingv1.BatchInitiateRequest{
			{AccountId: ""},       // Invalid - empty account ID (logs[0] = nil)
			{AccountId: "ACC001"}, // Valid
			{AccountId: ""},       // Invalid - empty account ID (logs[2] = nil)
			{AccountId: "ACC002"}, // Valid
			{AccountId: ""},       // Invalid - empty account ID (logs[4] = nil)
			{AccountId: "ACC003"}, // Valid
			{AccountId: ""},       // Invalid - empty account ID (logs[6] = nil)
		},
	}

	// Act - This should NOT panic even though some logs[i] will be nil
	// Previously this would panic at: logIDs = append(logIDs, logs[i].LogID)
	resp, err := svc.InitiateFinancialPositionLogBatch(ctx, req)

	// Assert
	require.NoError(t, err, "Partial failures should not return error")
	require.NotNil(t, resp, "Response should not be nil")

	// Verify counts match expectations
	assert.Equal(t, int32(7), resp.TotalCount)
	assert.Equal(t, int32(3), resp.SuccessCount, "Should have 3 successful entries")
	assert.Equal(t, int32(4), resp.FailureCount, "Should have 4 failed entries")

	// Verify individual results are correctly marked
	for i, result := range resp.Results {
		switch i {
		case 0, 2, 4, 6: // Invalid entries
			assert.False(t, result.Success, "Entry %d should have failed", i)
			assert.Contains(t, result.ErrorMessage, "account_id is required")
			assert.Nil(t, result.Log, "Failed entry %d should have no log", i)
		case 1, 3, 5: // Valid entries
			assert.True(t, result.Success, "Entry %d should be successful", i)
			assert.NotNil(t, result.Log, "Successful entry %d should have a log", i)
			assert.Empty(t, result.ErrorMessage, "Successful entry %d should have no error", i)
		}
	}

	// No database calls should be made when there are validation failures
	mockRepo.AssertNotCalled(t, "CreateBatch")
}
