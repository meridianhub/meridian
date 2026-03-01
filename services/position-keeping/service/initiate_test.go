package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/events"
)

// newTestOutboxPublisher creates an OutboxEventPublisher backed by a no-op PgxOutboxRepository
// for use in unit tests. The pool is nil so any actual DB call would fail, but since tests
// use MockRepository which never calls the outbox fn, this is safe for unit tests.
func newTestOutboxPublisher(tb testing.TB) *messaging.OutboxEventPublisher {
	tb.Helper()
	pub, err := messaging.NewOutboxEventPublisher(events.NewPgxOutboxRepository(nil))
	require.NoError(tb, err, "unexpected error creating test outbox publisher")
	return pub
}

// mustNewPositionKeepingService creates a service and fails the test if an error occurs.
// Use this for tests where the service should always be created successfully.
// Accepts testing.TB to work with both *testing.T and *testing.B.
func mustNewPositionKeepingService(tb testing.TB, repo domain.FinancialPositionLogRepository, publisher domain.EventPublisher, idempotencySvc idempotency.Service) *service.PositionKeepingService {
	tb.Helper()
	mockMeasurementRepo := new(MockMeasurementRepository)
	svc, err := service.NewPositionKeepingService(repo, mockMeasurementRepo, publisher, idempotencySvc, newTestOutboxPublisher(tb))
	require.NoError(tb, err, "unexpected error creating service")
	return svc
}

// MockRepository is a mock implementation of FinancialPositionLogRepository
type MockRepository struct {
	mock.Mock
}

func (m *MockRepository) Create(ctx context.Context, log *domain.FinancialPositionLog) error {
	args := m.Called(ctx, log)
	return args.Error(0)
}

func (m *MockRepository) CreateBatch(ctx context.Context, logs []*domain.FinancialPositionLog) error {
	args := m.Called(ctx, logs)
	return args.Error(0)
}

func (m *MockRepository) FindByID(ctx context.Context, logID uuid.UUID) (*domain.FinancialPositionLog, error) {
	args := m.Called(ctx, logID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.FinancialPositionLog), args.Error(1)
}

func (m *MockRepository) FindByAccountID(ctx context.Context, accountID string) ([]*domain.FinancialPositionLog, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.FinancialPositionLog), args.Error(1)
}

func (m *MockRepository) Update(ctx context.Context, log *domain.FinancialPositionLog) error {
	args := m.Called(ctx, log)
	return args.Error(0)
}

func (m *MockRepository) CreateWithOutbox(ctx context.Context, log *domain.FinancialPositionLog, _ func(pgx.Tx) error) error {
	args := m.Called(ctx, log)
	return args.Error(0)
}

func (m *MockRepository) UpdateWithOutbox(ctx context.Context, log *domain.FinancialPositionLog, _ func(pgx.Tx) error) error {
	args := m.Called(ctx, log)
	return args.Error(0)
}

func (m *MockRepository) List(ctx context.Context, filter domain.PositionLogFilter) ([]*domain.FinancialPositionLog, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.FinancialPositionLog), args.Error(1)
}

func (m *MockRepository) FindPendingForReconciliation(ctx context.Context, limit int) ([]*domain.FinancialPositionLog, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.FinancialPositionLog), args.Error(1)
}

// MockIdempotencyService is a mock implementation of idempotency.Service
type MockIdempotencyService struct {
	mock.Mock
}

func (m *MockIdempotencyService) Check(ctx context.Context, key idempotency.Key) (*idempotency.Result, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*idempotency.Result), args.Error(1)
}

func (m *MockIdempotencyService) MarkPending(ctx context.Context, key idempotency.Key, ttl time.Duration) error {
	args := m.Called(ctx, key, ttl)
	return args.Error(0)
}

func (m *MockIdempotencyService) StoreResult(ctx context.Context, result idempotency.Result) error {
	args := m.Called(ctx, result)
	return args.Error(0)
}

func (m *MockIdempotencyService) Delete(ctx context.Context, key idempotency.Key) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockIdempotencyService) Acquire(ctx context.Context, key idempotency.Key, opts idempotency.LockOptions) error {
	args := m.Called(ctx, key, opts)
	return args.Error(0)
}

func (m *MockIdempotencyService) Release(ctx context.Context, key idempotency.Key, token string) error {
	args := m.Called(ctx, key, token)
	return args.Error(0)
}

func (m *MockIdempotencyService) Refresh(ctx context.Context, key idempotency.Key, token string, ttl time.Duration) error {
	args := m.Called(ctx, key, token, ttl)
	return args.Error(0)
}

func (m *MockIdempotencyService) IsHeld(ctx context.Context, key idempotency.Key) (bool, error) {
	args := m.Called(ctx, key)
	return args.Bool(0), args.Error(1)
}

// MockMeasurementRepository is a mock implementation of MeasurementRepository
type MockMeasurementRepository struct {
	mock.Mock
}

func (m *MockMeasurementRepository) Create(ctx context.Context, measurement *domain.Measurement) error {
	args := m.Called(ctx, measurement)
	return args.Error(0)
}

func (m *MockMeasurementRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Measurement, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Measurement), args.Error(1)
}

func (m *MockMeasurementRepository) FindByPositionLogID(ctx context.Context, positionLogID uuid.UUID) ([]*domain.Measurement, error) {
	args := m.Called(ctx, positionLogID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Measurement), args.Error(1)
}

func TestInitiateFinancialPositionLog_Success(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "test-account-123",
		InitialEntry: &positionkeepingv1.TransactionLogEntry{
			EntryId:       uuid.NewString(),
			TransactionId: uuid.NewString(),
			AccountId:     "test-account-123",
			Amount: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
				},
			},
			Direction:   commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			Timestamp:   nil, // Will be set by service
			Description: "Test transaction",
			Reference:   "REF-001",
		},
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
	resp, err := svc.InitiateFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err, "Expected no error from InitiateFinancialPositionLog")
	require.NotNil(t, resp, "Expected non-nil response")
	require.NotNil(t, resp.Log, "Expected non-nil log in response")

	assert.Equal(t, "test-account-123", resp.Log.AccountId)
	assert.NotEmpty(t, resp.Log.LogId, "Expected log ID to be set")
	assert.Len(t, resp.Log.TransactionLogEntries, 1, "Expected 1 transaction entry")
	assert.NotNil(t, resp.Log.StatusTracking, "Expected status tracking to be set")
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, resp.Log.StatusTracking.CurrentStatus)

	// Verify event was written to outbox transactionally (CreateWithOutbox was called)
	mockRepo.AssertCalled(t, "CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog"))

	mockRepo.AssertExpectations(t)
	mockIdempotency.AssertExpectations(t)
}

func TestInitiateFinancialPositionLog_IdempotencyCheck(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	idempotencyKey := uuid.NewString()
	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "test-account-123",
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

	// Mock repository FindByID to return the cached log
	mockRepo.On("FindByID", ctx, mock.AnythingOfType("uuid.UUID")).
		Return(&domain.FinancialPositionLog{
			LogID:     uuid.MustParse(cachedLogID),
			AccountID: "test-account-123",
		}, nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLog(ctx, req)

	// Assert
	require.NoError(t, err, "Expected no error for idempotent request")
	require.NotNil(t, resp, "Expected non-nil response")
	assert.Equal(t, cachedLogID, resp.Log.LogId, "Expected cached log ID to be returned")

	// Verify no outbox write occurred (CreateWithOutbox not called for cached response)
	mockRepo.AssertNotCalled(t, "CreateWithOutbox")

	mockIdempotency.AssertExpectations(t)
	mockRepo.AssertExpectations(t)
}

func TestInitiateFinancialPositionLog_ValidationErrors(t *testing.T) {
	tests := []struct {
		name          string
		req           *positionkeepingv1.InitiateFinancialPositionLogRequest
		expectedCode  codes.Code
		expectedError string
	}{
		{
			name: "empty account ID",
			req: &positionkeepingv1.InitiateFinancialPositionLogRequest{
				AccountId: "",
			},
			expectedCode:  codes.InvalidArgument,
			expectedError: "account_id is required",
		},
		{
			name: "invalid initial entry - missing amount",
			req: &positionkeepingv1.InitiateFinancialPositionLogRequest{
				AccountId: "test-account-123",
				InitialEntry: &positionkeepingv1.TransactionLogEntry{
					EntryId:       uuid.NewString(),
					TransactionId: uuid.NewString(),
					AccountId:     "test-account-123",
					Direction:     commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				},
			},
			expectedCode:  codes.InvalidArgument,
			expectedError: "amount is required",
		},
		{
			name: "invalid direction - unspecified",
			req: &positionkeepingv1.InitiateFinancialPositionLogRequest{
				AccountId: "test-account-123",
				InitialEntry: &positionkeepingv1.TransactionLogEntry{
					EntryId:       uuid.NewString(),
					TransactionId: uuid.NewString(),
					AccountId:     "test-account-123",
					Amount: &commonv1.MoneyAmount{
						Amount: &money.Money{
							CurrencyCode: "GBP",
							Units:        100,
							Nanos:        0,
						},
					},
					Direction: commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED,
				},
			},
			expectedCode:  codes.InvalidArgument,
			expectedError: "direction cannot be unspecified",
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
			resp, err := svc.InitiateFinancialPositionLog(ctx, tt.req)

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

func TestInitiateFinancialPositionLog_RepositoryError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "test-account-123",
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

	// Mock repository create to fail
	mockRepo.On("CreateWithOutbox", ctx, mock.AnythingOfType("*domain.FinancialPositionLog")).
		Return(assert.AnError)

	// Mock idempotency delete for cleanup on error
	mockIdempotency.On("Delete", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err, "Expected error from repository failure")
	assert.Nil(t, resp, "Expected nil response on error")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code(), "Expected Internal error code for repository failure")

	mockRepo.AssertExpectations(t)
	mockIdempotency.AssertExpectations(t)
}

func TestInitiateFinancialPositionLog_MarkPendingError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "test-account-123",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.NewString(),
		},
	}

	// Mock idempotency check - no previous operation
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil, idempotency.ErrResultNotFound)

	// Mock idempotency mark pending to fail
	mockIdempotency.On("MarkPending", ctx, mock.AnythingOfType("idempotency.Key"), mock.AnythingOfType("time.Duration")).
		Return(assert.AnError)

	// Act
	resp, err := svc.InitiateFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err, "Expected error from MarkPending failure")
	assert.Nil(t, resp, "Expected nil response on error")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code(), "Expected Internal error code for MarkPending failure")
	assert.Contains(t, st.Message(), "failed to mark operation as pending", "Expected error message about MarkPending")

	mockIdempotency.AssertExpectations(t)
}

func TestInitiateFinancialPositionLog_StoreResultError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "test-account-123",
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

	// Mock idempotency store result to fail
	mockIdempotency.On("StoreResult", ctx, mock.AnythingOfType("idempotency.Result")).
		Return(assert.AnError)

	// Mock idempotency delete for cleanup on error
	mockIdempotency.On("Delete", ctx, mock.AnythingOfType("idempotency.Key")).
		Return(nil)

	// Act
	resp, err := svc.InitiateFinancialPositionLog(ctx, req)

	// Assert
	require.Error(t, err, "Expected error from StoreResult failure")
	assert.Nil(t, resp, "Expected nil response on error")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code(), "Expected Internal error code for StoreResult failure")
	assert.Contains(t, st.Message(), "failed to store idempotency result", "Expected error message about StoreResult")

	mockRepo.AssertExpectations(t)
	mockIdempotency.AssertExpectations(t)
}
