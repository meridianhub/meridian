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
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

func TestInitiateWithOpeningBalance_Success_PositiveBalance(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	effectiveDate := time.Now().Add(-24 * time.Hour) // Yesterday
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        1500,
				Nanos:        500000000, // £1500.50
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "MIGRATION-BATCH-001",
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

	// Mock repository create
	mockRepo.On("Create", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(nil)

	// Mock idempotency store result
	mockIdempotency.On("StoreResult", ctx, mock.AnythingOfType("idempotency.Result")).
		Return(nil)

	// Act
	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	// Assert
	require.NoError(t, err, "Expected no error from InitiateWithOpeningBalance")
	require.NotNil(t, resp, "Expected non-nil response")
	require.NotNil(t, resp.Log, "Expected non-nil log in response")

	assert.Equal(t, "migrated-account-001", resp.Log.AccountId)
	assert.NotEmpty(t, resp.Log.LogId, "Expected log ID to be set")
	// Opening balance creates a transaction entry
	assert.Len(t, resp.Log.TransactionLogEntries, 1, "Expected 1 transaction entry for opening balance")
	assert.NotNil(t, resp.Log.StatusTracking, "Expected status tracking to be set")
	// Opening balance is immediately posted
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, resp.Log.StatusTracking.CurrentStatus)

	// Verify event was published
	events := mockEventPublisher.GetPublishedEvents()
	assert.Len(t, events, 1, "Expected 1 event to be published")
	assert.Equal(t, "position_keeping.opening_balance_recorded.v1", events[0].EventType())

	mockRepo.AssertExpectations(t)
	mockIdempotency.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_Success_NegativeBalance(t *testing.T) {
	// Arrange - Test overdrawn account migration
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	effectiveDate := time.Now().Add(-24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "overdrawn-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        -500,
				Nanos:        -250000000, // -£500.25 (overdrawn)
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "MIGRATION-BATCH-002",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
	}

	// Mock idempotency check - no previous operation
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil, idempotency.ErrResultNotFound)

	mockIdempotency.On("MarkPending", ctx, mock.AnythingOfType("idempotency.Key"), mock.AnythingOfType("time.Duration")).
		Return(nil)

	mockRepo.On("Create", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(nil)

	mockIdempotency.On("StoreResult", ctx, mock.AnythingOfType("idempotency.Result")).
		Return(nil)

	// Act
	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	// Assert
	require.NoError(t, err, "Expected no error for negative opening balance")
	require.NotNil(t, resp, "Expected non-nil response")
	require.NotNil(t, resp.Log, "Expected non-nil log in response")

	assert.Equal(t, "overdrawn-account-001", resp.Log.AccountId)
	assert.Len(t, resp.Log.TransactionLogEntries, 1, "Expected 1 transaction entry for negative opening balance")
	// Verify the entry has DEBIT direction for negative balance
	assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, resp.Log.TransactionLogEntries[0].Direction)

	mockRepo.AssertExpectations(t)
	mockIdempotency.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_IdempotencyCheck(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	effectiveDate := time.Now().Add(-24 * time.Hour)
	idempotencyKey := uuid.NewString()
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        1500,
				Nanos:        0,
			},
		},
		EffectiveDate: timestamppb.New(effectiveDate),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: idempotencyKey,
		},
	}

	// Mock idempotency check - operation already completed
	cachedLogID := uuid.NewString()
	cachedResult := &idempotency.Result{
		Status: idempotency.StatusCompleted,
		Data:   []byte(`{"log_id":"` + cachedLogID + `"}`),
	}

	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(cachedResult, nil)

	// Create a log that looks like it was created with opening balance
	cachedLog := &domain.FinancialPositionLog{
		LogID:     uuid.MustParse(cachedLogID),
		AccountID: "migrated-account-001",
	}
	mockRepo.On("FindByID", ctx, mock.AnythingOfType("uuid.UUID")).
		Return(cachedLog, nil)

	// Act
	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	// Assert
	require.NoError(t, err, "Expected no error for idempotent request")
	require.NotNil(t, resp, "Expected non-nil response")
	assert.Equal(t, cachedLogID, resp.Log.LogId, "Expected cached log ID to be returned")

	// Verify no new events were published
	events := mockEventPublisher.GetPublishedEvents()
	assert.Len(t, events, 0, "Expected no new events for idempotent request")

	mockIdempotency.AssertExpectations(t)
	mockRepo.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_ValidationErrors(t *testing.T) {
	tests := []struct {
		name          string
		req           *positionkeepingv1.InitiateWithOpeningBalanceRequest
		expectedCode  codes.Code
		expectedError string
	}{
		{
			name: "empty account ID",
			req: &positionkeepingv1.InitiateWithOpeningBalanceRequest{
				AccountId: "",
				OpeningBalance: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        100,
					},
				},
				EffectiveDate: timestamppb.New(time.Now().Add(-24 * time.Hour)),
			},
			expectedCode:  codes.InvalidArgument,
			expectedError: "account_id is required",
		},
		{
			name: "missing opening balance",
			req: &positionkeepingv1.InitiateWithOpeningBalanceRequest{
				AccountId:     "test-account",
				EffectiveDate: timestamppb.New(time.Now().Add(-24 * time.Hour)),
			},
			expectedCode:  codes.InvalidArgument,
			expectedError: "opening_balance is required",
		},
		{
			name: "missing effective date",
			req: &positionkeepingv1.InitiateWithOpeningBalanceRequest{
				AccountId: "test-account",
				OpeningBalance: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        100,
					},
				},
			},
			expectedCode:  codes.InvalidArgument,
			expectedError: "effective_date is required",
		},
		{
			name: "future effective date",
			req: &positionkeepingv1.InitiateWithOpeningBalanceRequest{
				AccountId: "test-account",
				OpeningBalance: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        100,
					},
				},
				EffectiveDate: timestamppb.New(time.Now().Add(24 * time.Hour)), // Tomorrow
			},
			expectedCode:  codes.InvalidArgument,
			expectedError: "effective_date cannot be in the future",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			ctx := context.Background()
			mockRepo := new(MockRepository)
			mockEventPublisher := domain.NewInMemoryEventPublisher()
			mockIdempotency := new(MockIdempotencyService)

			svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

			// Act
			resp, err := svc.InitiateWithOpeningBalance(ctx, tt.req)

			// Assert
			require.Error(t, err, "Expected error for validation failure")
			assert.Nil(t, resp, "Expected nil response on error")

			st, ok := status.FromError(err)
			require.True(t, ok, "Expected gRPC status error")
			assert.Equal(t, tt.expectedCode, st.Code(), "Expected correct gRPC error code")
			assert.Contains(t, st.Message(), tt.expectedError, "Expected error message to contain validation error")
		})
	}
}

func TestInitiateWithOpeningBalance_RepositoryError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	effectiveDate := time.Now().Add(-24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        1500,
				Nanos:        0,
			},
		},
		EffectiveDate: timestamppb.New(effectiveDate),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
	}

	// Mock idempotency check - no previous operation
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil, idempotency.ErrResultNotFound)

	mockIdempotency.On("MarkPending", ctx, mock.AnythingOfType("idempotency.Key"), mock.AnythingOfType("time.Duration")).
		Return(nil)

	// Mock repository create to fail
	mockRepo.On("Create", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(assert.AnError)

	// Mock idempotency delete for cleanup on error
	mockIdempotency.On("Delete", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil)

	// Act
	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	// Assert
	require.Error(t, err, "Expected error from repository failure")
	assert.Nil(t, resp, "Expected nil response on error")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code(), "Expected Internal error code for repository failure")

	mockRepo.AssertExpectations(t)
	mockIdempotency.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_WithoutIdempotencyKey(t *testing.T) {
	// Arrange - Test that operation works without idempotency key
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	effectiveDate := time.Now().Add(-24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        1500,
				Nanos:        0,
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "MIGRATION-BATCH-001",
		// No IdempotencyKey
	}

	// Mock repository create
	mockRepo.On("Create", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(nil)

	// Act
	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	// Assert
	require.NoError(t, err, "Expected no error without idempotency key")
	require.NotNil(t, resp, "Expected non-nil response")
	assert.NotEmpty(t, resp.Log.LogId, "Expected log ID to be set")

	// Verify idempotency service was not called
	mockIdempotency.AssertNotCalled(t, "Check")
	mockIdempotency.AssertNotCalled(t, "MarkPending")
	mockIdempotency.AssertNotCalled(t, "StoreResult")

	mockRepo.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_StatusPending(t *testing.T) {
	// Arrange - Test that concurrent request returns Aborted when another is in progress
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	effectiveDate := time.Now().Add(-24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        1500,
				Nanos:        0,
			},
		},
		EffectiveDate: timestamppb.New(effectiveDate),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
	}

	// Mock idempotency check - operation is pending (another request in progress)
	pendingResult := &idempotency.Result{
		Status: idempotency.StatusPending,
	}

	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(pendingResult, nil)

	// Act
	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	// Assert - Should return Aborted error
	require.Error(t, err, "Expected error when operation is pending")
	assert.Nil(t, resp, "Expected nil response when operation is pending")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.Aborted, st.Code(), "Expected Aborted error code")
	assert.Contains(t, st.Message(), "operation already in progress")

	// Verify that MarkPending was NOT called since we returned early
	mockIdempotency.AssertNotCalled(t, "MarkPending")
	mockRepo.AssertNotCalled(t, "Create")
}

func TestInitiateWithOpeningBalance_ZeroBalance(t *testing.T) {
	// Arrange - Test zero opening balance (edge case)
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	effectiveDate := time.Now().Add(-24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "new-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        0,
				Nanos:        0,
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "MIGRATION-BATCH-003",
	}

	// Mock repository create
	mockRepo.On("Create", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(nil)

	// Act
	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	// Assert
	require.NoError(t, err, "Expected no error for zero opening balance")
	require.NotNil(t, resp, "Expected non-nil response")
	// Zero balance creates no transaction entry
	assert.Len(t, resp.Log.TransactionLogEntries, 0, "Expected no transaction entry for zero opening balance")

	mockRepo.AssertExpectations(t)
}
