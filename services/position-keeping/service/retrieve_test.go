package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// TestRetrieveFinancialPositionLog_Success tests successful retrieval by ID
func TestRetrieveFinancialPositionLog_Success(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()
	now := time.Now().UTC()
	expectedLog := &domain.FinancialPositionLog{
		LogID:                 logID,
		AccountID:             "ACC-12345",
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        domain.NewStatusTracking(),
		CreatedAt:             now,
		UpdatedAt:             now,
		Version:               1,
	}

	mockRepo.On("FindByID", ctx, logID).Return(expectedLog, nil)

	req := &positionkeepingv1.RetrieveFinancialPositionLogRequest{
		LogId: logID.String(),
	}

	// Act
	resp, err := svc.RetrieveFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Log)
	assert.Equal(t, logID.String(), resp.Log.LogId)
	assert.Equal(t, "ACC-12345", resp.Log.AccountId)
	mockRepo.AssertExpectations(t)
}

// TestRetrieveFinancialPositionLog_NotFound tests retrieval of non-existent log
func TestRetrieveFinancialPositionLog_NotFound(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()
	mockRepo.On("FindByID", ctx, logID).Return(nil, domain.ErrNotFound)

	req := &positionkeepingv1.RetrieveFinancialPositionLogRequest{
		LogId: logID.String(),
	}

	// Act
	resp, err := svc.RetrieveFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	// Verify it's a gRPC NotFound error
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "not found")
	mockRepo.AssertExpectations(t)
}

// TestRetrieveFinancialPositionLog_InvalidID tests retrieval with invalid UUID
func TestRetrieveFinancialPositionLog_InvalidID(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.RetrieveFinancialPositionLogRequest{
		LogId: "not-a-valid-uuid",
	}

	// Act
	resp, err := svc.RetrieveFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	// Verify it's a gRPC InvalidArgument error
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid")
	mockRepo.AssertNotCalled(t, "FindByID")
}

// TestRetrieveFinancialPositionLog_EmptyID tests retrieval with empty UUID
func TestRetrieveFinancialPositionLog_EmptyID(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.RetrieveFinancialPositionLogRequest{
		LogId: "",
	}

	// Act
	resp, err := svc.RetrieveFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	// Verify it's a gRPC InvalidArgument error
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	mockRepo.AssertNotCalled(t, "FindByID")
}

// TestRetrieveFinancialPositionLog_RepositoryError tests internal repository errors
func TestRetrieveFinancialPositionLog_RepositoryError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()
	mockRepo.On("FindByID", ctx, logID).Return(nil, assert.AnError)

	req := &positionkeepingv1.RetrieveFinancialPositionLogRequest{
		LogId: logID.String(),
	}

	// Act
	resp, err := svc.RetrieveFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	// Verify it's a gRPC Internal error
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	mockRepo.AssertExpectations(t)
}
