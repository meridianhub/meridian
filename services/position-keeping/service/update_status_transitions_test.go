package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

func makeExistingLog(logID uuid.UUID) *domain.FinancialPositionLog {
	return &domain.FinancialPositionLog{
		LogID:                 logID,
		AccountID:             "ACC-001",
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        domain.NewStatusTracking(),
		CreatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		UpdatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		Version:               1,
	}
}

func TestUpdateFinancialPositionLog_StatusFailed(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	logID := uuid.New()
	existingLog := makeExistingLog(logID)

	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)
	mockRepo.On("UpdateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	resp, err := svc.UpdateFinancialPositionLog(ctx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId:   logID.String(),
		Version: 1,
		StatusUpdate: &positionkeepingv1.StatusTracking{
			CurrentStatus: commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED,
			StatusReason:  "Processing error",
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action: "fail_transaction", Details: "System failure", UserId: "system",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED, resp.Log.StatusTracking.CurrentStatus)
}

func TestUpdateFinancialPositionLog_StatusCancelled(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	logID := uuid.New()
	existingLog := makeExistingLog(logID)

	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)
	mockRepo.On("UpdateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	resp, err := svc.UpdateFinancialPositionLog(ctx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId:   logID.String(),
		Version: 1,
		StatusUpdate: &positionkeepingv1.StatusTracking{
			CurrentStatus: commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
			StatusReason:  "User requested cancellation",
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action: "cancel_transaction", Details: "User cancellation", UserId: "user@example.com",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED, resp.Log.StatusTracking.CurrentStatus)
}

func TestUpdateFinancialPositionLog_StatusPendingNotAllowed(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	logID := uuid.New()
	existingLog := makeExistingLog(logID)

	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)

	resp, err := svc.UpdateFinancialPositionLog(ctx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId:   logID.String(),
		Version: 1,
		StatusUpdate: &positionkeepingv1.StatusTracking{
			CurrentStatus: commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
			StatusReason:  "Reset to pending",
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action: "reset", Details: "Reset", UserId: "user",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "cannot be directly set")
}

func TestUpdateFinancialPositionLog_StatusReversedNotAllowed(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	logID := uuid.New()
	existingLog := makeExistingLog(logID)

	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)

	resp, err := svc.UpdateFinancialPositionLog(ctx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId:   logID.String(),
		Version: 1,
		StatusUpdate: &positionkeepingv1.StatusTracking{
			CurrentStatus: commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED,
			StatusReason:  "Reverse",
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action: "reverse", Details: "Reverse", UserId: "user",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateFinancialPositionLog_RepositoryUpdateNotFound(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	logID := uuid.New()
	existingLog := makeExistingLog(logID)

	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)
	mockRepo.On("UpdateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(domain.ErrNotFound)

	resp, err := svc.UpdateFinancialPositionLog(ctx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId:   logID.String(),
		Version: 1,
		StatusUpdate: &positionkeepingv1.StatusTracking{
			CurrentStatus: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			StatusReason:  "Posted",
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action: "post", Details: "Post", UserId: "user",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUpdateFinancialPositionLog_RepositoryOptimisticLock(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	logID := uuid.New()
	existingLog := makeExistingLog(logID)

	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)
	mockRepo.On("UpdateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(domain.ErrOptimisticLock)

	resp, err := svc.UpdateFinancialPositionLog(ctx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId:   logID.String(),
		Version: 1,
		StatusUpdate: &positionkeepingv1.StatusTracking{
			CurrentStatus: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			StatusReason:  "Posted",
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action: "post", Details: "Post", UserId: "user",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
}

func TestUpdateFinancialPositionLog_RepositoryInternalError(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	logID := uuid.New()
	existingLog := makeExistingLog(logID)

	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)
	mockRepo.On("UpdateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(assert.AnError)

	resp, err := svc.UpdateFinancialPositionLog(ctx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId:   logID.String(),
		Version: 1,
		StatusUpdate: &positionkeepingv1.StatusTracking{
			CurrentStatus: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			StatusReason:  "Posted",
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action: "post", Details: "Post", UserId: "user",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestUpdateFinancialPositionLog_FindByIDInternalError(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	logID := uuid.New()
	mockRepo.On("FindByID", ctx, logID).Return(nil, assert.AnError)

	resp, err := svc.UpdateFinancialPositionLog(ctx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId:   logID.String(),
		Version: 1,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestUpdateFinancialPositionLog_NoIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	logID := uuid.New()
	existingLog := makeExistingLog(logID)

	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)
	mockRepo.On("UpdateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	resp, err := svc.UpdateFinancialPositionLog(ctx, &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId:   logID.String(),
		Version: 1,
		StatusUpdate: &positionkeepingv1.StatusTracking{
			CurrentStatus: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			StatusReason:  "Posted",
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action: "post", Details: "Post", UserId: "user",
		},
		// No IdempotencyKey
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	// No idempotency calls should be made
	idempotency.AssertNotCalled(t, "Check")
	idempotency.AssertNotCalled(t, "MarkPending")
	idempotency.AssertNotCalled(t, "StoreResult")
}
