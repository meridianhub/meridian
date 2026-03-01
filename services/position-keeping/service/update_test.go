package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

const (
	testAccountID = "test-account-123"
)

func TestUpdateFinancialPositionLog_AddNewEntry(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()
	accountID := testAccountID

	// Create existing log with one entry
	existingLog := &domain.FinancialPositionLog{
		LogID:                 logID,
		AccountID:             accountID,
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        domain.NewStatusTracking(),
		CreatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		UpdatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		Version:               1,
	}

	req := &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId: logID.String(),
		NewEntry: &positionkeepingv1.TransactionLogEntry{
			EntryId:       uuid.NewString(),
			TransactionId: uuid.NewString(),
			AccountId:     accountID,
			Amount: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        50,
					Nanos:        0,
				},
			},
			Direction:   commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
			Description: "Additional transaction",
			Reference:   "REF-002",
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action:  "add_transaction",
			Details: "Manual adjustment",
			UserId:  "user@example.com",
		},
		Version: 1,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
	}

	// Mock idempotency check - no previous operation
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil, idempotency.ErrResultNotFound)

	// Mock idempotency mark pending
	mockIdempotency.On("MarkPending", ctx, mock.AnythingOfType("idempotency.Key"), mock.AnythingOfType("time.Duration")).
		Return(nil)

	// Mock repository FindByID to return existing log
	mockRepo.On("FindByID", ctx, logID).
		Return(existingLog, nil)

	// Mock repository Update to succeed
	mockRepo.On("UpdateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(nil)

	// Mock idempotency store result
	mockIdempotency.On("StoreResult", ctx, mock.AnythingOfType("idempotency.Result")).
		Return(nil)

	// Act
	resp, err := svc.UpdateFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err, "Expected no error from UpdateFinancialPositionLog")
	require.NotNil(t, resp, "Expected non-nil response")
	require.NotNil(t, resp.Log, "Expected non-nil log in response")

	assert.Equal(t, logID.String(), resp.Log.LogId)
	assert.Equal(t, int64(1), resp.Log.Version, "Expected version to remain unchanged (AddEntry doesn't increment version)")
	assert.Len(t, resp.Log.TransactionLogEntries, 1, "Expected 1 transaction entry")
	assert.Len(t, resp.Log.AuditTrail, 1, "Expected 1 audit entry")

	mockRepo.AssertExpectations(t)
	mockIdempotency.AssertExpectations(t)
}

func TestUpdateFinancialPositionLog_UpdateStatus(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()
	accountID := testAccountID

	existingLog := &domain.FinancialPositionLog{
		LogID:                 logID,
		AccountID:             accountID,
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        domain.NewStatusTracking(),
		CreatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		UpdatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		Version:               1,
	}

	req := &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId: logID.String(),
		StatusUpdate: &positionkeepingv1.StatusTracking{
			CurrentStatus: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			StatusReason:  "Successfully posted to ledger",
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action:  "post_transaction",
			Details: "Posted to ledger",
			UserId:  "system",
		},
		Version: 1,
	}

	// Mock repository FindByID to return existing log
	mockRepo.On("FindByID", ctx, logID).
		Return(existingLog, nil)

	// Mock repository Update to succeed
	mockRepo.On("UpdateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(nil)

	// Act
	resp, err := svc.UpdateFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err, "Expected no error from UpdateFinancialPositionLog")
	require.NotNil(t, resp, "Expected non-nil response")
	require.NotNil(t, resp.Log, "Expected non-nil log in response")

	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, resp.Log.StatusTracking.CurrentStatus)
	assert.Len(t, resp.Log.AuditTrail, 1, "Expected 1 audit entry")

	mockRepo.AssertExpectations(t)
}

func TestUpdateFinancialPositionLog_VersionConflict(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()
	accountID := testAccountID

	existingLog := &domain.FinancialPositionLog{
		LogID:                 logID,
		AccountID:             accountID,
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        domain.NewStatusTracking(),
		CreatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		UpdatedAt:             time.Now().UTC().Add(-1 * time.Hour),
		Version:               2, // Current version is 2
	}

	req := &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId: logID.String(),
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action:  "test",
			Details: "test",
			UserId:  "test",
		},
		Version: 1, // Client thinks version is 1
	}

	// Mock repository FindByID to return existing log
	mockRepo.On("FindByID", ctx, logID).
		Return(existingLog, nil)

	// Act
	resp, err := svc.UpdateFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err, "Expected error for version conflict")
	assert.Nil(t, resp, "Expected nil response on error")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.Aborted, st.Code(), "Expected Aborted error code for version conflict")
	assert.Contains(t, st.Message(), "version conflict", "Expected version conflict error message")

	mockRepo.AssertExpectations(t)
}

func TestUpdateFinancialPositionLog_LogNotFound(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()

	req := &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId: logID.String(),
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action:  "test",
			Details: "test",
			UserId:  "test",
		},
		Version: 1,
	}

	// Mock repository FindByID to return not found error
	mockRepo.On("FindByID", ctx, logID).
		Return(nil, domain.ErrNotFound)

	// Act
	resp, err := svc.UpdateFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err, "Expected error when log not found")
	assert.Nil(t, resp, "Expected nil response on error")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.NotFound, st.Code(), "Expected NotFound error code")

	mockRepo.AssertExpectations(t)
}

func TestUpdateFinancialPositionLog_InvalidLogID(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId: "invalid-uuid",
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			Action:  "test",
			Details: "test",
			UserId:  "test",
		},
		Version: 1,
	}

	// Act
	resp, err := svc.UpdateFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err, "Expected error for invalid UUID")
	assert.Nil(t, resp, "Expected nil response on error")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code(), "Expected InvalidArgument error code")
}

func TestUpdateFinancialPositionLog_MissingAuditEntry(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	logID := uuid.New()
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

	req := &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId: logID.String(),
		NewEntry: &positionkeepingv1.TransactionLogEntry{
			EntryId:       uuid.NewString(),
			TransactionId: uuid.NewString(),
			AccountId:     testAccountID,
			Amount: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        50,
					Nanos:        0,
				},
			},
			Direction:   commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
			Description: "Missing audit entry test",
			Reference:   "REF-TEST",
		},
		// AuditEntry intentionally omitted
		Version: 1,
	}

	// Mock repository FindByID to return existing log
	mockRepo.On("FindByID", ctx, logID).
		Return(existingLog, nil)

	// Act
	resp, err := svc.UpdateFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err, "Expected error when audit entry is missing")
	assert.Nil(t, resp, "Expected nil response on error")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code(), "Expected InvalidArgument error code")
	assert.Contains(t, st.Message(), "audit_entry is required", "Expected error message about missing audit entry")

	mockRepo.AssertExpectations(t)
}
