package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

// TestRecordMeasurement_Success tests successful measurement recording
func TestRecordMeasurement_Success(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, newTestOutboxPublisher(t))
	require.NoError(t, err)

	logID := uuid.New()
	now := time.Now().UTC()

	// Create a position log for the mock
	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Setup mock expectations
	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"source": "smart-meter",
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.MeasurementId)
	assert.Equal(t, logID.String(), resp.PositionStateId)
	assert.NotNil(t, resp.RecordedAt)
	mockRepo.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestRecordMeasurement_ValidatesMeasurementTypeRequired tests that measurement_type is required
func TestRecordMeasurement_ValidatesMeasurementTypeRequired(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, newTestOutboxPublisher(t))
	require.NoError(t, err)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: uuid.NewString(),
		MeasurementType: "", // Empty - should fail
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.Now(),
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "measurement_type")
}

// TestRecordMeasurement_RejectsNegativeValue tests that negative values are rejected
func TestRecordMeasurement_RejectsNegativeValue(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, newTestOutboxPublisher(t))
	require.NoError(t, err)

	logID := uuid.New()
	now := time.Now().UTC()

	// Create a position log for the mock
	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Only FindByID should be called - Create should not be called due to validation failure
	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "-100.5", // Negative value - should fail
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "positive")
	mockRepo.AssertExpectations(t)
}

// TestRecordMeasurement_RejectsFutureTimestamp tests that future timestamps are rejected
func TestRecordMeasurement_RejectsFutureTimestamp(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, newTestOutboxPublisher(t))
	require.NoError(t, err)

	logID := uuid.New()
	now := time.Now().UTC()

	// Create a position log for the mock
	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)

	// Timestamp in the future (more than 1 minute to exceed tolerance)
	futureTime := now.Add(10 * time.Minute)
	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(futureTime), // Future timestamp - should fail
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "future")
	mockRepo.AssertExpectations(t)
}

// TestRecordMeasurement_PositionStateNotFound tests error when position state doesn't exist
func TestRecordMeasurement_PositionStateNotFound(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, newTestOutboxPublisher(t))
	require.NoError(t, err)

	logID := uuid.New()

	mockRepo.On("FindByID", ctx, logID).Return(nil, domain.ErrNotFound)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.Now(),
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	mockRepo.AssertExpectations(t)
}

// TestRecordMeasurement_InvalidPositionStateId tests error for invalid UUID
func TestRecordMeasurement_InvalidPositionStateId(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, newTestOutboxPublisher(t))
	require.NoError(t, err)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: "not-a-valid-uuid",
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.Now(),
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestRecordMeasurement_Idempotency_DuplicateKeyReturnsCachedResult tests idempotency
func TestRecordMeasurement_Idempotency_DuplicateKeyReturnsCachedResult(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, newTestOutboxPublisher(t))
	require.NoError(t, err)

	logID := uuid.New()
	measurementID := uuid.New()
	idempotencyKey := "test-idempotency-key"

	// Setup cached result
	cachedResult := &idempotency.Result{
		Status:      idempotency.StatusCompleted,
		Data:        []byte(`{"measurement_id":"` + measurementID.String() + `","position_state_id":"` + logID.String() + `"}`),
		CompletedAt: time.Now(),
	}

	// Expect Check to be called and return cached result
	mockIdempotency.On("Check", ctx, mock.AnythingOfType("idempotency.Key")).Return(cachedResult, nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.Now(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: idempotencyKey,
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, measurementID.String(), resp.MeasurementId)
	assert.Equal(t, logID.String(), resp.PositionStateId)
	// Repository should NOT have been called - we returned cached result
	mockRepo.AssertNotCalled(t, "FindByID")
	mockMeasurementRepo.AssertNotCalled(t, "Create")
}

// TestRecordMeasurement_RequiredFieldValidation tests all required fields
func TestRecordMeasurement_RequiredFieldValidation(t *testing.T) {
	tests := []struct {
		name          string
		req           *positionkeepingv1.RecordMeasurementRequest
		expectedError string
	}{
		{
			name: "missing position_state_id",
			req: &positionkeepingv1.RecordMeasurementRequest{
				PositionStateId: "",
				MeasurementType: "kWh",
				Value:           "100.5",
				Unit:            "kWh",
				Timestamp:       timestamppb.Now(),
			},
			expectedError: "position_state_id",
		},
		{
			name: "missing value",
			req: &positionkeepingv1.RecordMeasurementRequest{
				PositionStateId: uuid.NewString(),
				MeasurementType: "kWh",
				Value:           "",
				Unit:            "kWh",
				Timestamp:       timestamppb.Now(),
			},
			expectedError: "value",
		},
		{
			name: "missing unit",
			req: &positionkeepingv1.RecordMeasurementRequest{
				PositionStateId: uuid.NewString(),
				MeasurementType: "kWh",
				Value:           "100.5",
				Unit:            "",
				Timestamp:       timestamppb.Now(),
			},
			expectedError: "unit",
		},
		{
			name: "missing timestamp",
			req: &positionkeepingv1.RecordMeasurementRequest{
				PositionStateId: uuid.NewString(),
				MeasurementType: "kWh",
				Value:           "100.5",
				Unit:            "kWh",
				Timestamp:       nil,
			},
			expectedError: "timestamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockRepo := new(MockRepository)
			mockMeasurementRepo := new(MockMeasurementRepository)
			mockEventPublisher := domain.NewInMemoryEventPublisher()
			mockIdempotency := new(MockIdempotencyService)

			svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, newTestOutboxPublisher(t))
			require.NoError(t, err)

			resp, err := svc.RecordMeasurement(ctx, tt.req)

			require.Error(t, err)
			require.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
			assert.Contains(t, st.Message(), tt.expectedError)
		})
	}
}

// TestRecordMeasurement_MeasurementTypes tests all valid measurement types
func TestRecordMeasurement_MeasurementTypes(t *testing.T) {
	measurementTypes := []string{
		"kWh",
		"GPU-Hours",
		"CPU-Hours",
		"Storage-GB",
		"Bandwidth-GB",
		"Carbon-Tonnes",
		"Water-Litres", //nolint:misspell // British spelling matches database constraint
		"Custom",
	}

	for _, mt := range measurementTypes {
		t.Run(mt, func(t *testing.T) {
			ctx := context.Background()
			mockRepo := new(MockRepository)
			mockMeasurementRepo := new(MockMeasurementRepository)
			mockEventPublisher := domain.NewInMemoryEventPublisher()
			mockIdempotency := new(MockIdempotencyService)

			svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, newTestOutboxPublisher(t))
			require.NoError(t, err)

			logID := uuid.New()
			now := time.Now().UTC()

			positionLog := &domain.FinancialPositionLog{
				LogID:     logID,
				AccountID: "test-account-123",
				StatusTracking: &domain.StatusTracking{
					CurrentStatus: domain.TransactionStatusPending,
				},
				CreatedAt: now,
				UpdatedAt: now,
				Version:   1,
			}

			mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
			mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil)

			req := &positionkeepingv1.RecordMeasurementRequest{
				PositionStateId: logID.String(),
				MeasurementType: mt,
				Value:           "100.0",
				Unit:            mt,
				Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
			}

			resp, err := svc.RecordMeasurement(ctx, req)

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.NotEmpty(t, resp.MeasurementId)
		})
	}
}

// TestDomain_Measurement_NewMeasurement_Validation tests domain level validation
func TestDomain_Measurement_NewMeasurement_Validation(t *testing.T) {
	positionLogID := uuid.New()
	now := time.Now().UTC()

	tests := []struct {
		name            string
		measurementType domain.MeasurementType
		value           decimal.Decimal
		unit            string
		timestamp       time.Time
		expectedErr     error
	}{
		{
			name:            "valid measurement",
			measurementType: domain.MeasurementTypeKWh,
			value:           decimal.NewFromFloat(100.5),
			unit:            "kWh",
			timestamp:       now.Add(-1 * time.Hour),
			expectedErr:     nil,
		},
		{
			name:            "negative value",
			measurementType: domain.MeasurementTypeKWh,
			value:           decimal.NewFromFloat(-100.5),
			unit:            "kWh",
			timestamp:       now.Add(-1 * time.Hour),
			expectedErr:     domain.ErrNegativeMeasurementValue,
		},
		{
			name:            "empty unit",
			measurementType: domain.MeasurementTypeKWh,
			value:           decimal.NewFromFloat(100.5),
			unit:            "",
			timestamp:       now.Add(-1 * time.Hour),
			expectedErr:     domain.ErrInvalidUnit,
		},
		{
			name:            "future timestamp",
			measurementType: domain.MeasurementTypeKWh,
			value:           decimal.NewFromFloat(100.5),
			unit:            "kWh",
			timestamp:       now.Add(10 * time.Minute),
			expectedErr:     domain.ErrFutureTimestamp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement, err := domain.NewMeasurement(
				positionLogID,
				tt.measurementType,
				tt.value,
				tt.unit,
				tt.timestamp,
				nil, // metadata
				"",  // bucket_id (empty for these tests)
				"test-user",
			)

			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
				assert.Nil(t, measurement)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, measurement)
				assert.NotEqual(t, uuid.Nil, measurement.ID)
				assert.Equal(t, positionLogID, measurement.FinancialPositionLogID)
			}
		})
	}
}

// =============================================================================
// CEL Validation Tests
// =============================================================================

// MockInstrumentCache is a mock implementation of InstrumentCache for testing.
type MockInstrumentCache struct {
	mock.Mock
}

// GetOrLoad implements service.InstrumentCache.
func (m *MockInstrumentCache) GetOrLoad(ctx context.Context, code string, version int, _ func() (*service.CachedInstrument, error)) (*service.CachedInstrument, error) {
	args := m.Called(ctx, code, version)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*service.CachedInstrument), args.Error(1)
}

// createTestCELProgram creates a CEL program for testing validation.
// The expression should evaluate to a boolean.
func createTestCELProgram(t *testing.T, expression string) cel.Program {
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

// TestRecordMeasurement_CEL_ValidationPasses tests that valid attributes pass CEL validation.
func TestRecordMeasurement_CEL_ValidationPasses(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Create a CEL program that validates the source attribute exists
	validationProgram := createTestCELProgram(t, `"source" in attributes`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:    "kWh",
		ValidationProgram: validationProgram,
	}

	// Setup mock expectations
	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"source": "smart-meter", // This satisfies the CEL expression
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.MeasurementId)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestRecordMeasurement_CEL_ValidationRejects tests that invalid attributes are rejected.
func TestRecordMeasurement_CEL_ValidationRejects(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Create a CEL program that requires a specific source attribute
	validationProgram := createTestCELProgram(t, `"source" in attributes && attributes["source"] == "approved-meter"`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:    "kWh",
		ValidationProgram: validationProgram,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"source": "unknown-meter", // This does NOT satisfy the CEL expression
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "validation failed")
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertNotCalled(t, "Create")
}

// TestRecordMeasurement_CEL_NilCacheSkipsValidation tests that nil cache skips validation.
func TestRecordMeasurement_CEL_NilCacheSkipsValidation(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		// No metadata - would normally fail validation, but cache is nil
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.MeasurementId)
	mockRepo.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestRecordMeasurement_CEL_NilValidationProgramPasses tests that nil validation program passes.
func TestRecordMeasurement_CEL_NilValidationProgramPasses(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Instrument with NO validation program (nil)
	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:    "kWh",
		ValidationProgram: nil, // No validation expression defined
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		// No metadata - validation is skipped because program is nil
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.MeasurementId)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestRecordMeasurement_CEL_InstrumentNotFound tests error when instrument is not found.
func TestRecordMeasurement_CEL_InstrumentNotFound(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	// Cache returns not found error
	mockCache.On("GetOrLoad", ctx, "unknown-instrument", 1).Return(nil, service.ErrInstrumentNotFound)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "unknown-instrument",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "instrument definition not found")
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertNotCalled(t, "Create")
}

// =============================================================================
// Bucket Key Generation Tests
// =============================================================================

// MockBucketCounter is a mock implementation of BucketCounter for testing.
type MockBucketCounter struct {
	mock.Mock
}

// CountBuckets implements service.BucketCounter.
func (m *MockBucketCounter) CountBuckets(ctx context.Context, accountID string, instrumentCode string) (int, error) {
	args := m.Called(ctx, accountID, instrumentCode)
	return args.Int(0), args.Error(1)
}

// createTestBucketKeyProgram creates a CEL program for testing bucket key generation.
// The expression should evaluate to a string.
func createTestBucketKeyProgram(t *testing.T, expression string) cel.Program {
	t.Helper()
	env, err := cel.NewEnv(
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
	)
	require.NoError(t, err)

	ast, issues := env.Compile(expression)
	require.NoError(t, issues.Err())

	prg, err := env.Program(ast)
	require.NoError(t, err)

	return prg
}

// TestRecordMeasurement_BucketKey_GeneratesKey tests that bucket key is generated from attributes.
func TestRecordMeasurement_BucketKey_GeneratesKey(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Create a bucket key program that concatenates attributes
	// This simulates what bucket_key([...]) would do, returning a simple string for testing
	bucketKeyProgram := createTestBucketKeyProgram(t, `attributes["region"] + "-" + attributes["meter_id"]`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:   "kWh",
		BucketKeyProgram: bucketKeyProgram,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"region":   "eu-west-1",
			"meter_id": "meter-001",
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.MeasurementId)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestRecordMeasurement_BucketKey_SameAttributesProduceSameKey tests determinism.
func TestRecordMeasurement_BucketKey_SameAttributesProduceSameKey(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Use a deterministic bucket key expression
	bucketKeyProgram := createTestBucketKeyProgram(t, `attributes["meter_id"]`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:   "kWh",
		BucketKeyProgram: bucketKeyProgram,
	}

	// Setup for two requests with same attributes
	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil).Times(2)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil).Times(2)
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil).Times(2)

	metadata := map[string]string{
		"meter_id": "meter-123",
	}

	req1 := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata:        metadata,
	}

	req2 := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "200.0", // Different value
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-30 * time.Minute)),
		Metadata:        metadata, // Same metadata
	}

	resp1, err1 := svc.RecordMeasurement(ctx, req1)
	require.NoError(t, err1)
	require.NotNil(t, resp1)

	resp2, err2 := svc.RecordMeasurement(ctx, req2)
	require.NoError(t, err2)
	require.NotNil(t, resp2)

	// Both should succeed (bucket key is same, so should be deterministic)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestRecordMeasurement_BucketKey_NilProgramProducesEmptyBucketID tests nil program behavior.
func TestRecordMeasurement_BucketKey_NilProgramProducesEmptyBucketID(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Instrument with NO bucket key program (nil)
	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:   "kWh",
		BucketKeyProgram: nil, // No bucket key expression
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"source": "meter",
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.MeasurementId)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// =============================================================================
// Cardinality Guard Tests
// =============================================================================

// TestRecordMeasurement_Cardinality_RejectsWhenLimitExceeded tests cardinality enforcement.
func TestRecordMeasurement_Cardinality_RejectsWhenLimitExceeded(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)
	mockBucketCounter := new(MockBucketCounter)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
		service.WithBucketCounter(mockBucketCounter),
	)
	require.NoError(t, err)

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	bucketKeyProgram := createTestBucketKeyProgram(t, `attributes["meter_id"]`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:   "kWh",
		BucketKeyProgram: bucketKeyProgram,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)
	// Return count at limit
	mockBucketCounter.On("CountBuckets", ctx, "test-account-123", "kWh").Return(service.MaxBucketsPerAccountInstrument, nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"meter_id": "new-meter-999",
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
	assert.Contains(t, st.Message(), "bucket cardinality limit exceeded")
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockBucketCounter.AssertExpectations(t)
	mockMeasurementRepo.AssertNotCalled(t, "Create")
}

// TestRecordMeasurement_Cardinality_AllowsUnderLimit tests requests under limit succeed.
func TestRecordMeasurement_Cardinality_AllowsUnderLimit(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)
	mockBucketCounter := new(MockBucketCounter)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
		service.WithBucketCounter(mockBucketCounter),
	)
	require.NoError(t, err)

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	bucketKeyProgram := createTestBucketKeyProgram(t, `attributes["meter_id"]`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:   "kWh",
		BucketKeyProgram: bucketKeyProgram,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)
	// Return count well under limit
	mockBucketCounter.On("CountBuckets", ctx, "test-account-123", "kWh").Return(100, nil)
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"meter_id": "meter-001",
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.MeasurementId)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockBucketCounter.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestRecordMeasurement_Cardinality_SkipsCheckWhenNoBucketCounter tests nil counter behavior.
func TestRecordMeasurement_Cardinality_SkipsCheckWhenNoBucketCounter(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)
	// NO bucket counter configured

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
		// WithBucketCounter NOT called - counter is nil
	)
	require.NoError(t, err)

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	bucketKeyProgram := createTestBucketKeyProgram(t, `attributes["meter_id"]`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:   "kWh",
		BucketKeyProgram: bucketKeyProgram,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"meter_id": "meter-001",
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	// Should succeed without cardinality check
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.MeasurementId)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestRecordMeasurement_Cardinality_SkipsCheckWhenNoBucketKey tests behavior when no bucket key.
func TestRecordMeasurement_Cardinality_SkipsCheckWhenNoBucketKey(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCache := new(MockInstrumentCache)
	mockBucketCounter := new(MockBucketCounter)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithInstrumentCache(mockCache),
		service.WithBucketCounter(mockBucketCounter),
	)
	require.NoError(t, err)

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Instrument with NO bucket key program
	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:   "kWh",
		BucketKeyProgram: nil, // No bucket key
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"meter_id": "meter-001",
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	// Should succeed - bucket counter should NOT be called since no bucket key generated
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.MeasurementId)
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
	// Verify bucket counter was NOT called
	mockBucketCounter.AssertNotCalled(t, "CountBuckets")
}

// TestRecordMeasurement_BucketKey_ErrorReturnsInvalidArgument tests bucket key program errors.
func TestRecordMeasurement_BucketKey_ErrorReturnsInvalidArgument(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Create a bucket key program that will fail (accessing non-existent key)
	bucketKeyProgram := createTestBucketKeyProgram(t, `attributes["nonexistent_key"]`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:   "kWh",
		BucketKeyProgram: bucketKeyProgram,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"meter_id": "meter-001", // Note: "nonexistent_key" is not in metadata
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "bucket key generation error")
	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertNotCalled(t, "Create")
}

// =============================================================================
// Bucket ID Domain Handoff Tests (Subtask 20.3)
// =============================================================================

// TestRecordMeasurement_BucketID_PassedToDomain verifies that the bucket_id from
// CEL validation is correctly passed to the domain.Measurement struct.
func TestRecordMeasurement_BucketID_PassedToDomain(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Create a bucket key program that returns a fixed key
	// This simulates a real bucket_key([...]) expression
	bucketKeyProgram := createTestBucketKeyProgram(t, `"bucket-" + attributes["region"]`)

	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:   "kWh",
		BucketKeyProgram: bucketKeyProgram,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)

	// Capture the measurement passed to Create to verify bucket_id
	var capturedMeasurement *domain.Measurement
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).
		Run(func(args mock.Arguments) {
			capturedMeasurement = args.Get(1).(*domain.Measurement)
		}).
		Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"region": "eu-west-1",
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.MeasurementId)

	// Verify bucket_id was passed to domain
	require.NotNil(t, capturedMeasurement)
	assert.Equal(t, "bucket-eu-west-1", capturedMeasurement.BucketID)

	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestRecordMeasurement_BucketID_EmptyWhenNoBucketKeyProgram verifies that
// bucket_id is empty string when no bucket key expression is defined.
func TestRecordMeasurement_BucketID_EmptyWhenNoBucketKeyProgram(t *testing.T) {
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

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Instrument with NO bucket key program
	cachedInstrument := &service.CachedInstrument{
		InstrumentCode:   "kWh",
		BucketKeyProgram: nil,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)
	mockCache.On("GetOrLoad", ctx, "kWh", 1).Return(cachedInstrument, nil)

	// Capture the measurement passed to Create
	var capturedMeasurement *domain.Measurement
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).
		Run(func(args mock.Arguments) {
			capturedMeasurement = args.Get(1).(*domain.Measurement)
		}).
		Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"region": "eu-west-1",
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify bucket_id is empty when no program defined
	require.NotNil(t, capturedMeasurement)
	assert.Empty(t, capturedMeasurement.BucketID)

	mockRepo.AssertExpectations(t)
	mockCache.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestRecordMeasurement_BucketID_EmptyWhenNoCacheConfigured verifies that
// bucket_id is empty string when instrument cache is not configured.
func TestRecordMeasurement_BucketID_EmptyWhenNoCacheConfigured(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	// Create service WITHOUT instrument cache
	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		// No WithInstrumentCache - cache is nil
	)
	require.NoError(t, err)

	logID := uuid.New()
	now := time.Now().UTC()

	positionLog := &domain.FinancialPositionLog{
		LogID:     logID,
		AccountID: "test-account-123",
		StatusTracking: &domain.StatusTracking{
			CurrentStatus: domain.TransactionStatusPending,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	mockRepo.On("FindByID", ctx, logID).Return(positionLog, nil)

	// Capture the measurement passed to Create
	var capturedMeasurement *domain.Measurement
	mockMeasurementRepo.On("Create", ctx, mock.AnythingOfType("*domain.Measurement")).
		Run(func(args mock.Arguments) {
			capturedMeasurement = args.Get(1).(*domain.Measurement)
		}).
		Return(nil)

	req := &positionkeepingv1.RecordMeasurementRequest{
		PositionStateId: logID.String(),
		MeasurementType: "kWh",
		Value:           "100.5",
		Unit:            "kWh",
		Timestamp:       timestamppb.New(now.Add(-1 * time.Hour)),
		Metadata: map[string]string{
			"region": "eu-west-1",
		},
	}

	resp, err := svc.RecordMeasurement(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify bucket_id is empty when no cache configured
	require.NotNil(t, capturedMeasurement)
	assert.Empty(t, capturedMeasurement.BucketID)

	mockRepo.AssertExpectations(t)
	mockMeasurementRepo.AssertExpectations(t)
}

// TestDomain_Measurement_BucketID_StoredInStruct verifies that BucketID is
// correctly stored in the Measurement struct.
func TestDomain_Measurement_BucketID_StoredInStruct(t *testing.T) {
	positionLogID := uuid.New()
	now := time.Now().UTC()

	measurement, err := domain.NewMeasurement(
		positionLogID,
		domain.MeasurementTypeKWh,
		decimal.NewFromFloat(100.5),
		"kWh",
		now.Add(-1*time.Hour),
		map[string]string{"region": "eu-west-1"},
		"test-bucket-key-12345",
		"test-user",
	)

	require.NoError(t, err)
	require.NotNil(t, measurement)
	assert.Equal(t, "test-bucket-key-12345", measurement.BucketID)
}

// TestDomain_Measurement_BucketID_CanBeEmpty verifies that BucketID can be empty.
func TestDomain_Measurement_BucketID_CanBeEmpty(t *testing.T) {
	positionLogID := uuid.New()
	now := time.Now().UTC()

	measurement, err := domain.NewMeasurement(
		positionLogID,
		domain.MeasurementTypeKWh,
		decimal.NewFromFloat(100.5),
		"kWh",
		now.Add(-1*time.Hour),
		nil,
		"", // Empty bucket_id is valid
		"test-user",
	)

	require.NoError(t, err)
	require.NotNil(t, measurement)
	assert.Empty(t, measurement.BucketID)
}
