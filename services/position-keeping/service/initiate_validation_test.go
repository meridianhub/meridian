package service_test

import (
	"context"
	"testing"

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
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

// MockAccountValidator is a mock implementation of AccountValidator
type MockAccountValidator struct {
	mock.Mock
}

func (m *MockAccountValidator) ValidateExists(ctx context.Context, accountID string) error {
	args := m.Called(ctx, accountID)
	return args.Error(0)
}

func TestInitiateFinancialPositionLog_AccountValidation_Enabled_ValidAccount(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockValidator := new(MockAccountValidator)
	mockMeasurementRepo := new(MockMeasurementRepository)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithAccountValidator(mockValidator),
		service.WithAccountValidationEnabled(true),
	)
	require.NoError(t, err)

	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "valid-account-123",
		InitialEntry: &positionkeepingv1.TransactionLogEntry{
			EntryId:       uuid.NewString(),
			TransactionId: uuid.NewString(),
			AccountId:     "valid-account-123",
			Amount: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
				},
			},
			Direction:   commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			Description: "Test transaction",
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
	}

	// Mock idempotency check - no previous operation
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil, idempotency.ErrResultNotFound)
	mockIdempotency.On("MarkPending", ctx, mock.AnythingOfType("idempotency.Key"), mock.AnythingOfType("time.Duration")).
		Return(nil)

	// Mock account validation - account exists
	mockValidator.On("ValidateExists", ctx, "valid-account-123").Return(nil)

	// Mock repository create
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)

	// Mock idempotency store result
	mockIdempotency.On("StoreResult", ctx, mock.AnythingOfType("idempotency.Result")).Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "valid-account-123", resp.Log.AccountId)

	// Verify validator was called
	mockValidator.AssertCalled(t, "ValidateExists", ctx, "valid-account-123")
	mockRepo.AssertExpectations(t)
}

func TestInitiateFinancialPositionLog_AccountValidation_Enabled_InvalidAccount(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockValidator := new(MockAccountValidator)
	mockMeasurementRepo := new(MockMeasurementRepository)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithAccountValidator(mockValidator),
		service.WithAccountValidationEnabled(true),
	)
	require.NoError(t, err)

	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "invalid-account-456",
		InitialEntry: &positionkeepingv1.TransactionLogEntry{
			EntryId:       uuid.NewString(),
			TransactionId: uuid.NewString(),
			AccountId:     "invalid-account-456",
			Amount: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
				},
			},
			Direction:   commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			Description: "Test transaction",
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
	}

	// Mock idempotency check - no previous operation
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil, idempotency.ErrResultNotFound)
	mockIdempotency.On("MarkPending", ctx, mock.AnythingOfType("idempotency.Key"), mock.AnythingOfType("time.Duration")).
		Return(nil)
	// When validation fails, the idempotency key is deleted to allow retry
	mockIdempotency.On("Delete", ctx, mock.AnythingOfType("idempotency.Key")).Return(nil)

	// Mock account validation - account NOT found
	mockValidator.On("ValidateExists", ctx, "invalid-account-456").
		Return(status.Error(codes.InvalidArgument, "account not found: invalid-account-456"))

	// Act
	resp, err := svc.InitiateFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "account not found")
	assert.Contains(t, err.Error(), "invalid-account-456")

	// Verify validator was called but repository was NOT called
	mockValidator.AssertCalled(t, "ValidateExists", ctx, "invalid-account-456")
	mockRepo.AssertNotCalled(t, "CreateWithOutbox")
}

func TestInitiateFinancialPositionLog_AccountValidation_Disabled_SkipsValidation(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockValidator := new(MockAccountValidator)
	mockMeasurementRepo := new(MockMeasurementRepository)

	// Validation disabled (default) - even with validator set
	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithAccountValidator(mockValidator),
		// WithAccountValidationEnabled not called - defaults to false
	)
	require.NoError(t, err)

	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "any-account-789",
		InitialEntry: &positionkeepingv1.TransactionLogEntry{
			EntryId:       uuid.NewString(),
			TransactionId: uuid.NewString(),
			AccountId:     "any-account-789",
			Amount: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
				},
			},
			Direction:   commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			Description: "Test transaction",
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
	}

	// Mock idempotency check - no previous operation
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil, idempotency.ErrResultNotFound)
	mockIdempotency.On("MarkPending", ctx, mock.AnythingOfType("idempotency.Key"), mock.AnythingOfType("time.Duration")).
		Return(nil)

	// Mock repository create - should be called since validation is disabled
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)
	mockIdempotency.On("StoreResult", ctx, mock.AnythingOfType("idempotency.Result")).Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify validator was NOT called (validation disabled)
	mockValidator.AssertNotCalled(t, "ValidateExists")
	// Verify repository WAS called (position log created)
	mockRepo.AssertCalled(t, "CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog"))
}

func TestInitiateFinancialPositionLog_AccountValidation_ValidatorNil_SkipsValidation(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockMeasurementRepo := new(MockMeasurementRepository)

	// Validation enabled but no validator provided
	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithAccountValidationEnabled(true), // Enabled but...
		// WithAccountValidator not called - validator is nil
	)
	require.NoError(t, err)

	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "any-account-000",
		InitialEntry: &positionkeepingv1.TransactionLogEntry{
			EntryId:       uuid.NewString(),
			TransactionId: uuid.NewString(),
			AccountId:     "any-account-000",
			Amount: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
				},
			},
			Direction:   commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			Description: "Test transaction",
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
	}

	// Mock idempotency check - no previous operation
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil, idempotency.ErrResultNotFound)
	mockIdempotency.On("MarkPending", ctx, mock.AnythingOfType("idempotency.Key"), mock.AnythingOfType("time.Duration")).
		Return(nil)

	// Mock repository create - should be called since validator is nil
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)
	mockIdempotency.On("StoreResult", ctx, mock.AnythingOfType("idempotency.Result")).Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify repository WAS called (position log created despite enabled flag)
	mockRepo.AssertCalled(t, "CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog"))
}

func TestInitiateFinancialPositionLog_AccountValidation_GracefulDegradation(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockValidator := new(MockAccountValidator)
	mockMeasurementRepo := new(MockMeasurementRepository)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithAccountValidator(mockValidator),
		service.WithAccountValidationEnabled(true),
	)
	require.NoError(t, err)

	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "graceful-account-123",
		InitialEntry: &positionkeepingv1.TransactionLogEntry{
			EntryId:       uuid.NewString(),
			TransactionId: uuid.NewString(),
			AccountId:     "graceful-account-123",
			Amount: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
				},
			},
			Direction:   commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			Description: "Test transaction",
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
	}

	// Mock idempotency check - no previous operation
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil, idempotency.ErrResultNotFound)
	mockIdempotency.On("MarkPending", ctx, mock.AnythingOfType("idempotency.Key"), mock.AnythingOfType("time.Duration")).
		Return(nil)

	// Mock account validation - service unavailable (graceful degradation returns nil)
	// Note: The CurrentAccountValidator returns nil on service unavailability
	mockValidator.On("ValidateExists", ctx, "graceful-account-123").Return(nil)

	// Mock repository create - should be called due to graceful degradation
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).Return(nil)
	mockIdempotency.On("StoreResult", ctx, mock.AnythingOfType("idempotency.Result")).Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify position log was created despite potential service unavailability
	mockRepo.AssertCalled(t, "CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog"))
}

func TestWithAccountValidator_Option(t *testing.T) {
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockValidator := new(MockAccountValidator)

	t.Run("sets account validator", func(t *testing.T) {
		svc, err := service.NewPositionKeepingService(
			mockRepo,
			mockMeasurementRepo,
			mockEventPublisher,
			mockIdempotency,
			newTestOutboxPublisher(t),
			service.WithAccountValidator(mockValidator),
		)
		require.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("allows nil validator", func(t *testing.T) {
		svc, err := service.NewPositionKeepingService(
			mockRepo,
			mockMeasurementRepo,
			mockEventPublisher,
			mockIdempotency,
			newTestOutboxPublisher(t),
			service.WithAccountValidator(nil),
		)
		require.NoError(t, err)
		assert.NotNil(t, svc)
	})
}

func TestWithAccountValidationEnabled_Option(t *testing.T) {
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockMeasurementRepo := new(MockMeasurementRepository)

	t.Run("enables validation", func(t *testing.T) {
		svc, err := service.NewPositionKeepingService(
			mockRepo,
			mockMeasurementRepo,
			mockEventPublisher,
			mockIdempotency,
			newTestOutboxPublisher(t),
			service.WithAccountValidationEnabled(true),
		)
		require.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("disables validation", func(t *testing.T) {
		svc, err := service.NewPositionKeepingService(
			mockRepo,
			mockMeasurementRepo,
			mockEventPublisher,
			mockIdempotency,
			newTestOutboxPublisher(t),
			service.WithAccountValidationEnabled(false),
		)
		require.NoError(t, err)
		assert.NotNil(t, svc)
	})
}
