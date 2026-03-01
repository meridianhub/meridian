package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/cel-go/cel"
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
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
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

	// Verify event was written to outbox transactionally (CreateWithOutbox was called)
	mockRepo.AssertCalled(t, "CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog"))

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

	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
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

	// Verify no outbox write occurred (CreateWithOutbox not called for cached response)
	mockRepo.AssertNotCalled(t, "CreateWithOutbox")

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
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
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
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
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
	mockRepo.AssertNotCalled(t, "CreateWithOutbox")
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
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
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

func TestInitiateWithOpeningBalance_IdempotencyCheckError(t *testing.T) {
	// Arrange - Test that transient idempotency errors are not silently ignored
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

	// Mock idempotency check - transient error (e.g., Redis timeout)
	transientErr := assert.AnError // Generic error (not ErrResultNotFound)
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil, transientErr)

	// Act
	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	// Assert - Should return Internal error, not proceed to MarkPending
	require.Error(t, err, "Expected error when idempotency check fails")
	assert.Nil(t, resp, "Expected nil response when idempotency check fails")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code(), "Expected Internal error code")
	assert.Contains(t, st.Message(), "failed to check idempotency")

	// Verify that MarkPending was NOT called since we returned early
	mockIdempotency.AssertNotCalled(t, "MarkPending")
	mockRepo.AssertNotCalled(t, "CreateWithOutbox")
}

// =============================================================================
// CEL Validation Tests for InitiateWithOpeningBalance
// =============================================================================

// createMigrationTestCELProgram creates a CEL program for testing validation.
// The expression should evaluate to a boolean.
func createMigrationTestCELProgram(t *testing.T, expression string) cel.Program {
	t.Helper()
	env, err := cel.NewEnv(
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("amount", cel.StringType),
		cel.Variable("valid_from", cel.TimestampType),
		cel.Variable("valid_to", cel.TimestampType),
		cel.Variable("source", cel.StringType),
	)
	require.NoError(t, err)

	ast, issues := env.Compile(expression)
	require.NoError(t, issues.Err())

	prg, err := env.Program(ast)
	require.NoError(t, err)

	return prg
}

func TestInitiateWithOpeningBalance_CEL_ValidationPasses(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)

	// Create service with instrument cache
	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
	)
	require.NoError(t, err)

	effectiveDate := time.Now().Add(-24 * time.Hour)

	// Create a CEL program that validates the batch_id attribute exists
	validationProgram := createMigrationTestCELProgram(t, `"batch_id" in attributes`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:    "USD",
		ValidationProgram: validationProgram,
	}

	// Setup mock expectations
	mockCache.On("GetOrLoad", ctx, "USD", 1).Return(cachedInstrument, nil)
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        1500,
				Nanos:        500000000,
			},
		},
		EffectiveDate:  timestamppb.New(effectiveDate),
		InstrumentCode: "USD",
		Attributes: map[string]string{
			"batch_id": "2024-Q1", // This satisfies the CEL expression
		},
	}

	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Log)
	assert.NotEmpty(t, resp.Log.LogId)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_CEL_ValidationRejects(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
	)
	require.NoError(t, err)

	effectiveDate := time.Now().Add(-24 * time.Hour)

	// Create a CEL program that requires a specific batch_id format
	validationProgram := createMigrationTestCELProgram(t, `"batch_id" in attributes && attributes["batch_id"] != ""`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:    "USD",
		ValidationProgram: validationProgram,
	}

	mockCache.On("GetOrLoad", ctx, "USD", 1).Return(cachedInstrument, nil)

	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        1500,
				Nanos:        0,
			},
		},
		EffectiveDate:  timestamppb.New(effectiveDate),
		InstrumentCode: "USD",
		Attributes:     map[string]string{}, // Missing batch_id - should fail
	}

	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "validation failed")
	mockRepo.AssertNotCalled(t, "CreateWithOutbox")
	mockCache.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_CEL_NilCacheSkipsValidation(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	// Create service WITHOUT instrument cache (nil)
	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		// No WithInstrumentCache - cache is nil
	)
	require.NoError(t, err)

	effectiveDate := time.Now().Add(-24 * time.Hour)

	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        1500,
				Nanos:        0,
			},
		},
		EffectiveDate:  timestamppb.New(effectiveDate),
		InstrumentCode: "USD", // Instrument code provided but no cache
		Attributes: map[string]string{
			"batch_id": "2024-Q1",
		},
	}

	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	// Should succeed - validation skipped because cache is nil
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Log.LogId)
	mockRepo.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_CEL_EmptyInstrumentCodeSkipsValidation(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
	)
	require.NoError(t, err)

	effectiveDate := time.Now().Add(-24 * time.Hour)

	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        1500,
				Nanos:        0,
			},
		},
		EffectiveDate:  timestamppb.New(effectiveDate),
		InstrumentCode: "", // Empty - legacy mode, skip validation
		// No attributes
	}

	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	// Should succeed - validation skipped because instrument_code is empty
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Log.LogId)
	// Verify cache was NOT called since instrument_code is empty
	mockCache.AssertNotCalled(t, "GetOrLoad")
	mockRepo.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_CEL_NilValidationProgramPasses(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
	)
	require.NoError(t, err)

	effectiveDate := time.Now().Add(-24 * time.Hour)

	// Instrument with NO validation program (nil) - fully fungible
	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:    "USD",
		ValidationProgram: nil, // No validation expression defined
	}

	mockCache.On("GetOrLoad", ctx, "USD", 1).Return(cachedInstrument, nil)
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        1500,
				Nanos:        0,
			},
		},
		EffectiveDate:  timestamppb.New(effectiveDate),
		InstrumentCode: "USD",
		// No attributes - validation is skipped because program is nil
	}

	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Log.LogId)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_CEL_InstrumentNotFound(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
	)
	require.NoError(t, err)

	effectiveDate := time.Now().Add(-24 * time.Hour)

	// Cache returns not found error
	mockCache.On("GetOrLoad", ctx, "UNKNOWN_INSTRUMENT", 1).Return(nil, service.ErrInstrumentNotFound)

	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        1500,
				Nanos:        0,
			},
		},
		EffectiveDate:  timestamppb.New(effectiveDate),
		InstrumentCode: "UNKNOWN_INSTRUMENT",
	}

	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "instrument definition not found")
	mockRepo.AssertNotCalled(t, "CreateWithOutbox")
	mockCache.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_CEL_CacheFailure(t *testing.T) {
	// Test that cache/backend failures return codes.Internal (not NotFound)
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
	)
	require.NoError(t, err)

	effectiveDate := time.Now().Add(-24 * time.Hour)

	// Cache returns a generic error (e.g., Redis timeout, backend failure)
	mockCache.On("GetOrLoad", ctx, "USD", 1).Return(nil, assert.AnError)

	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        1500,
				Nanos:        0,
			},
		},
		EffectiveDate:  timestamppb.New(effectiveDate),
		InstrumentCode: "USD",
	}

	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to load instrument definition")
	mockRepo.AssertNotCalled(t, "CreateWithOutbox")
	mockCache.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_CEL_ComplexValidationExpression(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
	)
	require.NoError(t, err)

	effectiveDate := time.Now().Add(-24 * time.Hour)

	// Create a CEL program with complex validation:
	// batch_id must exist AND grade must be "1" or "2"
	validationProgram := createMigrationTestCELProgram(t, `"batch_id" in attributes && "grade" in attributes && attributes["grade"] in ["1", "2"]`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:    "ELECTRICITY_KWH",
		ValidationProgram: validationProgram,
	}

	mockCache.On("GetOrLoad", ctx, "ELECTRICITY_KWH", 1).Return(cachedInstrument, nil)
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        1000,
				Nanos:        0,
			},
		},
		EffectiveDate:  timestamppb.New(effectiveDate),
		InstrumentCode: "ELECTRICITY_KWH",
		Attributes: map[string]string{
			"batch_id": "2024-Q1",
			"grade":    "1", // Valid grade
		},
	}

	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Log.LogId)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_CEL_ComplexValidationFails(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
	)
	require.NoError(t, err)

	effectiveDate := time.Now().Add(-24 * time.Hour)

	// Create a CEL program with complex validation
	validationProgram := createMigrationTestCELProgram(t, `"batch_id" in attributes && "grade" in attributes && attributes["grade"] in ["1", "2"]`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:    "ELECTRICITY_KWH",
		ValidationProgram: validationProgram,
	}

	mockCache.On("GetOrLoad", ctx, "ELECTRICITY_KWH", 1).Return(cachedInstrument, nil)

	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "migrated-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        1000,
				Nanos:        0,
			},
		},
		EffectiveDate:  timestamppb.New(effectiveDate),
		InstrumentCode: "ELECTRICITY_KWH",
		Attributes: map[string]string{
			"batch_id": "2024-Q1",
			"grade":    "3", // Invalid grade - not in ["1", "2"]
		},
	}

	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "validation failed")
	mockRepo.AssertNotCalled(t, "CreateWithOutbox")
	mockCache.AssertExpectations(t)
}

func TestInitiateWithOpeningBalance_CEL_NegativeAmountPassesValidation(t *testing.T) {
	// Test that negative opening balances (e.g., overdraft positions) are correctly
	// formatted and passed to CEL validation. This covers the edge case for
	// migrating accounts with negative balances.
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
	)
	require.NoError(t, err)

	effectiveDate := time.Now().Add(-24 * time.Hour)

	// Create a CEL program that validates the batch_id attribute exists
	// (doesn't check the amount value, just ensures CEL receives it correctly)
	validationProgram := createMigrationTestCELProgram(t, `"batch_id" in attributes`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:    "USD",
		ValidationProgram: validationProgram,
	}

	mockCache.On("GetOrLoad", ctx, "USD", 1).Return(cachedInstrument, nil)
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	// Negative opening balance: -$500.50 (overdraft position)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: "overdraft-account-001",
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        -500,
				Nanos:        -500000000, // -$500.50 (money.Money requires same sign)
			},
		},
		EffectiveDate:  timestamppb.New(effectiveDate),
		InstrumentCode: "USD",
		Attributes: map[string]string{
			"batch_id": "OVERDRAFT-MIGRATION-2024",
		},
	}

	resp, err := svc.InitiateWithOpeningBalance(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Log)
	assert.NotEmpty(t, resp.Log.LogId)
	// Verify the transaction entry shows DEBIT direction for negative balance
	assert.Len(t, resp.Log.TransactionLogEntries, 1)
	assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, resp.Log.TransactionLogEntries[0].Direction)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
}
