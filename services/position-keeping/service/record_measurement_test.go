package service_test

import (
	"context"
	"testing"
	"time"

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

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency)
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

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency)
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

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency)
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

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency)
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

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency)
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

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency)
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

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency)
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

			svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency)
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

			svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency)
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
				nil,
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
