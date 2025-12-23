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

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

func TestControlFinancialPositionLog_Suspend(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()

	// Create existing log in PENDING status
	existingLog := &domain.FinancialPositionLog{
		LogID:                 logID,
		AccountID:             testAccountID,
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        domain.NewStatusTracking(),
		CreatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		UpdatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		Version:               1,
	}

	req := &positionkeepingv1.ControlFinancialPositionLogRequest{
		LogId:         logID.String(),
		ControlAction: positionkeepingv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "System maintenance",
		OperatorId:    "admin@example.com",
	}

	// Mock repository FindByID
	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)

	// Mock repository Update
	mockRepo.On("Update", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	// Act
	resp, err := svc.ControlFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, logID.String(), resp.LogId)
	assert.Equal(t, positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_SUSPENDED, resp.Status)
	assert.Equal(t, positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_ACTIVE, resp.PreviousStatus)
	mockRepo.AssertExpectations(t)
}

func TestControlFinancialPositionLog_Resume(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()

	// Create existing log in SUSPENDED status
	suspendedTracking := domain.NewStatusTracking()
	_ = suspendedTracking.UpdateStatus(domain.TransactionStatusSuspended, "Test suspended")
	existingLog := &domain.FinancialPositionLog{
		LogID:                 logID,
		AccountID:             testAccountID,
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        suspendedTracking,
		CreatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		UpdatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		Version:               1,
	}

	req := &positionkeepingv1.ControlFinancialPositionLogRequest{
		LogId:         logID.String(),
		ControlAction: positionkeepingv1.ControlAction_CONTROL_ACTION_RESUME,
		Reason:        "Maintenance completed",
		OperatorId:    "admin@example.com",
	}

	// Mock repository FindByID
	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)

	// Mock repository Update
	mockRepo.On("Update", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	// Act
	resp, err := svc.ControlFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, logID.String(), resp.LogId)
	assert.Equal(t, positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_ACTIVE, resp.Status)
	assert.Equal(t, positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_SUSPENDED, resp.PreviousStatus)
	mockRepo.AssertExpectations(t)
}

func TestControlFinancialPositionLog_Terminate(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()

	// Create existing log in SUSPENDED status (terminate only works from SUSPENDED)
	suspendedTracking := domain.NewStatusTracking()
	_ = suspendedTracking.UpdateStatus(domain.TransactionStatusSuspended, "Test suspended")
	existingLog := &domain.FinancialPositionLog{
		LogID:                 logID,
		AccountID:             testAccountID,
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        suspendedTracking,
		CreatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		UpdatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		Version:               1,
	}

	req := &positionkeepingv1.ControlFinancialPositionLogRequest{
		LogId:         logID.String(),
		ControlAction: positionkeepingv1.ControlAction_CONTROL_ACTION_TERMINATE,
		Reason:        "Account closure",
		OperatorId:    "admin@example.com",
	}

	// Mock repository FindByID
	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)

	// Mock repository Update
	mockRepo.On("Update", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	// Act
	resp, err := svc.ControlFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, logID.String(), resp.LogId)
	assert.Equal(t, positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_TERMINATED, resp.Status)
	assert.Equal(t, positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_SUSPENDED, resp.PreviousStatus)
	mockRepo.AssertExpectations(t)
}

func TestControlFinancialPositionLog_NotFound(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()

	req := &positionkeepingv1.ControlFinancialPositionLogRequest{
		LogId:         logID.String(),
		ControlAction: positionkeepingv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Test",
		OperatorId:    "admin@example.com",
	}

	// Mock repository FindByID to return not found
	mockRepo.On("FindByID", ctx, logID).Return(nil, domain.ErrNotFound)

	// Act
	resp, err := svc.ControlFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	mockRepo.AssertExpectations(t)
}

func TestControlFinancialPositionLog_InvalidLogID(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.ControlFinancialPositionLogRequest{
		LogId:         "not-a-valid-uuid",
		ControlAction: positionkeepingv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Test",
		OperatorId:    "admin@example.com",
	}

	// Act
	resp, err := svc.ControlFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestControlFinancialPositionLog_UnspecifiedAction(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.ControlFinancialPositionLogRequest{
		LogId:         uuid.New().String(),
		ControlAction: positionkeepingv1.ControlAction_CONTROL_ACTION_UNSPECIFIED,
		Reason:        "Test",
		OperatorId:    "admin@example.com",
	}

	// Act
	resp, err := svc.ControlFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestControlFinancialPositionLog_CannotResumeNonSuspendedLog(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()

	// Create existing log in PENDING status (not SUSPENDED)
	existingLog := &domain.FinancialPositionLog{
		LogID:                 logID,
		AccountID:             testAccountID,
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        domain.NewStatusTracking(), // PENDING
		CreatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		UpdatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		Version:               1,
	}

	req := &positionkeepingv1.ControlFinancialPositionLogRequest{
		LogId:         logID.String(),
		ControlAction: positionkeepingv1.ControlAction_CONTROL_ACTION_RESUME,
		Reason:        "Try to resume non-suspended log",
		OperatorId:    "admin@example.com",
	}

	// Mock repository FindByID
	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)

	// Act
	resp, err := svc.ControlFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	mockRepo.AssertExpectations(t)
}

func TestControlFinancialPositionLog_CannotSuspendTerminatedLog(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()

	// Create existing log in TERMINATED status
	// Since TERMINATED is only reachable from SUSPENDED, we need to set it directly
	terminatedTracking := &domain.StatusTracking{
		CurrentStatus: domain.TransactionStatusTerminated,
	}
	existingLog := &domain.FinancialPositionLog{
		LogID:                 logID,
		AccountID:             testAccountID,
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        terminatedTracking,
		CreatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		UpdatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		Version:               1,
	}

	req := &positionkeepingv1.ControlFinancialPositionLogRequest{
		LogId:         logID.String(),
		ControlAction: positionkeepingv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Try to suspend terminated log",
		OperatorId:    "admin@example.com",
	}

	// Mock repository FindByID
	mockRepo.On("FindByID", ctx, logID).Return(existingLog, nil)

	// Act (no Update mock needed - domain model should reject before reaching Update)
	resp, err := svc.ControlFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	mockRepo.AssertExpectations(t)
}

// mockIdempotencyService is defined in service_test.go and used in multiple test files
// MockRepository is defined in service_test.go
