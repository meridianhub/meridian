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

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// TestListFinancialPositionLogs_ByAccountID tests filtering by account ID
func TestListFinancialPositionLogs_ByAccountID(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	accountID := "ACC-12345"
	now := time.Now().UTC()

	expectedLogs := []*domain.FinancialPositionLog{
		{
			LogID:                 uuid.New(),
			AccountID:             accountID,
			TransactionLogEntries: []*domain.TransactionLogEntry{},
			AuditTrail:            []*domain.AuditTrailEntry{},
			StatusTracking:        domain.NewStatusTracking(),
			CreatedAt:             now,
			UpdatedAt:             now,
			Version:               1,
		},
		{
			LogID:                 uuid.New(),
			AccountID:             accountID,
			TransactionLogEntries: []*domain.TransactionLogEntry{},
			AuditTrail:            []*domain.AuditTrailEntry{},
			StatusTracking:        domain.NewStatusTracking(),
			CreatedAt:             now.Add(time.Hour),
			UpdatedAt:             now.Add(time.Hour),
			Version:               1,
		},
	}

	// Setup mock to expect correct filter
	mockRepo.On("List", ctx, domain.PositionLogFilter{
		AccountID: &accountID,
		Limit:     50,
		Offset:    0,
	}).Return(expectedLogs, nil)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		AccountId: accountID,
		Pagination: &commonv1.Pagination{
			PageSize: 50,
		},
	}

	// Act
	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Logs, 2)
	assert.Equal(t, accountID, resp.Logs[0].AccountId)
	assert.Equal(t, accountID, resp.Logs[1].AccountId)
	mockRepo.AssertExpectations(t)
}

// TestListFinancialPositionLogs_ByStatus tests filtering by transaction status
func TestListFinancialPositionLogs_ByStatus(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	status := domain.TransactionStatusPending
	now := time.Now().UTC()

	expectedLogs := []*domain.FinancialPositionLog{
		{
			LogID:                 uuid.New(),
			AccountID:             "ACC-001",
			TransactionLogEntries: []*domain.TransactionLogEntry{},
			AuditTrail:            []*domain.AuditTrailEntry{},
			StatusTracking:        domain.NewStatusTracking(),
			CreatedAt:             now,
			UpdatedAt:             now,
			Version:               1,
		},
	}

	// Setup mock to expect correct filter
	mockRepo.On("List", ctx, domain.PositionLogFilter{
		Status: &status,
		Limit:  50,
		Offset: 0,
	}).Return(expectedLogs, nil)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		Pagination: &commonv1.Pagination{
			PageSize: 50,
		},
	}

	// Act
	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Logs, 1)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, resp.Logs[0].StatusTracking.CurrentStatus)
	mockRepo.AssertExpectations(t)
}

// TestListFinancialPositionLogs_Pagination tests pagination parameters
func TestListFinancialPositionLogs_Pagination(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	now := time.Now().UTC()
	expectedLogs := []*domain.FinancialPositionLog{
		{
			LogID:                 uuid.New(),
			AccountID:             "ACC-001",
			TransactionLogEntries: []*domain.TransactionLogEntry{},
			AuditTrail:            []*domain.AuditTrailEntry{},
			StatusTracking:        domain.NewStatusTracking(),
			CreatedAt:             now,
			UpdatedAt:             now,
			Version:               1,
		},
	}

	// Setup mock to expect limit of 10, offset of 20
	mockRepo.On("List", ctx, domain.PositionLogFilter{
		Limit:  10,
		Offset: 20,
	}).Return(expectedLogs, nil)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		Pagination: &commonv1.Pagination{
			PageSize:  10,
			PageToken: "20", // Page token encodes offset
		},
	}

	// Act
	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Logs, 1)
	mockRepo.AssertExpectations(t)
}

// TestListFinancialPositionLogs_DateRange tests date range filtering
func TestListFinancialPositionLogs_DateRange(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	fromDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	// ToDate is start of next day (exclusive upper bound)
	// This ensures records on end_date (2025-01-31) are included (< 2025-02-01)
	toDate := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)

	expectedLogs := []*domain.FinancialPositionLog{
		{
			LogID:                 uuid.New(),
			AccountID:             "ACC-001",
			TransactionLogEntries: []*domain.TransactionLogEntry{},
			AuditTrail:            []*domain.AuditTrailEntry{},
			StatusTracking:        domain.NewStatusTracking(),
			CreatedAt:             time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
			UpdatedAt:             time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
			Version:               1,
		},
	}

	// Setup mock to expect date range filter
	mockRepo.On("List", ctx, domain.PositionLogFilter{
		FromDate: &fromDate,
		ToDate:   &toDate,
		Limit:    50,
		Offset:   0,
	}).Return(expectedLogs, nil)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		DateRange: &commonv1.DateRange{
			StartDate: "2025-01-01",
			EndDate:   "2025-01-31",
		},
		Pagination: &commonv1.Pagination{
			PageSize: 50,
		},
	}

	// Act
	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Logs, 1)
	mockRepo.AssertExpectations(t)
}

// TestListFinancialPositionLogs_EmptyResults tests empty result set
func TestListFinancialPositionLogs_EmptyResults(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	// Setup mock to return empty results
	mockRepo.On("List", ctx, domain.PositionLogFilter{
		Limit:  50,
		Offset: 0,
	}).Return([]*domain.FinancialPositionLog{}, nil)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		Pagination: &commonv1.Pagination{
			PageSize: 50,
		},
	}

	// Act
	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Empty(t, resp.Logs)
	mockRepo.AssertExpectations(t)
}

// TestListFinancialPositionLogs_ZeroPageSize_UsesDefault tests that zero page_size
// uses the default (50) instead of rejecting. Proto3 int32 defaults to 0, and
// connect-es clients may send Pagination with zero page_size.
func TestListFinancialPositionLogs_ZeroPageSize_UsesDefault(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	// Setup mock to expect default limit of 50
	mockRepo.On("List", ctx, domain.PositionLogFilter{
		Limit:  50,
		Offset: 0,
	}).Return([]*domain.FinancialPositionLog{}, nil)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		Pagination: &commonv1.Pagination{
			PageSize: 0, // Proto3 default
		},
	}

	// Act
	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Empty(t, resp.Logs)
	mockRepo.AssertExpectations(t)
}

// TestListFinancialPositionLogs_NilPagination_UsesDefault tests that nil pagination
// uses the default page size (50).
func TestListFinancialPositionLogs_NilPagination_UsesDefault(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	// Setup mock to expect default limit of 50
	mockRepo.On("List", ctx, domain.PositionLogFilter{
		Limit:  50,
		Offset: 0,
	}).Return([]*domain.FinancialPositionLog{}, nil)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		// No pagination at all
	}

	// Act
	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Empty(t, resp.Logs)
	mockRepo.AssertExpectations(t)
}

// TestListFinancialPositionLogs_InvalidPagination tests invalid pagination parameters
func TestListFinancialPositionLogs_InvalidPagination(t *testing.T) {
	tests := []struct {
		name          string
		pageSize      int32
		expectedCode  codes.Code
		expectedError string
	}{
		{
			name:          "negative page size",
			pageSize:      -1,
			expectedCode:  codes.InvalidArgument,
			expectedError: "page_size must be positive",
		},
		{
			name:          "page size too large",
			pageSize:      10000,
			expectedCode:  codes.InvalidArgument,
			expectedError: "page_size exceeds maximum",
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

			req := &positionkeepingv1.ListFinancialPositionLogsRequest{
				Pagination: &commonv1.Pagination{
					PageSize: tt.pageSize,
				},
			}

			// Act
			resp, err := svc.ListFinancialPositionLogs(ctx, req)

			// Assert
			require.Error(t, err)
			assert.Nil(t, resp)

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, st.Code())
			assert.Contains(t, st.Message(), tt.expectedError)
			mockRepo.AssertNotCalled(t, "List")
		})
	}
}

// TestListFinancialPositionLogs_InvalidDateRange tests invalid date range
func TestListFinancialPositionLogs_InvalidDateRange(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		DateRange: &commonv1.DateRange{
			StartDate: "invalid-date",
			EndDate:   "2025-01-31",
		},
		Pagination: &commonv1.Pagination{
			PageSize: 50,
		},
	}

	// Act
	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid start_date")
	mockRepo.AssertNotCalled(t, "List")
}

// TestListFinancialPositionLogs_RepositoryError tests repository errors
func TestListFinancialPositionLogs_RepositoryError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	// Setup mock to return error
	mockRepo.On("List", ctx, domain.PositionLogFilter{
		Limit:  50,
		Offset: 0,
	}).Return(nil, assert.AnError)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		Pagination: &commonv1.Pagination{
			PageSize: 50,
		},
	}

	// Act
	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	mockRepo.AssertExpectations(t)
}

// TestListFinancialPositionLogs_ByAccountIDs tests filtering by multiple account IDs
func TestListFinancialPositionLogs_ByAccountIDs(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	accountIDs := []string{"ACC-001", "ACC-002"}
	now := time.Now().UTC()

	expectedLogs := []*domain.FinancialPositionLog{
		{
			LogID:                 uuid.New(),
			AccountID:             "ACC-001",
			TransactionLogEntries: []*domain.TransactionLogEntry{},
			AuditTrail:            []*domain.AuditTrailEntry{},
			StatusTracking:        domain.NewStatusTracking(),
			CreatedAt:             now,
			UpdatedAt:             now,
			Version:               1,
		},
		{
			LogID:                 uuid.New(),
			AccountID:             "ACC-002",
			TransactionLogEntries: []*domain.TransactionLogEntry{},
			AuditTrail:            []*domain.AuditTrailEntry{},
			StatusTracking:        domain.NewStatusTracking(),
			CreatedAt:             now,
			UpdatedAt:             now,
			Version:               1,
		},
	}

	mockRepo.On("List", ctx, domain.PositionLogFilter{
		AccountIDs: accountIDs,
		Limit:      50,
		Offset:     0,
	}).Return(expectedLogs, nil)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		AccountIds: accountIDs,
		Pagination: &commonv1.Pagination{
			PageSize: 50,
		},
	}

	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Logs, 2)
	mockRepo.AssertExpectations(t)
}

// TestListFinancialPositionLogs_AccountIDs_TakesPrecedenceOverAccountID verifies account_ids
// takes precedence when both account_id and account_ids are set.
func TestListFinancialPositionLogs_AccountIDs_TakesPrecedenceOverAccountID(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	accountIDs := []string{"ACC-001", "ACC-002"}
	now := time.Now().UTC()

	expectedLogs := []*domain.FinancialPositionLog{
		{
			LogID:                 uuid.New(),
			AccountID:             "ACC-001",
			TransactionLogEntries: []*domain.TransactionLogEntry{},
			AuditTrail:            []*domain.AuditTrailEntry{},
			StatusTracking:        domain.NewStatusTracking(),
			CreatedAt:             now,
			UpdatedAt:             now,
			Version:               1,
		},
	}

	// Mock expects AccountIDs set, not AccountID
	mockRepo.On("List", ctx, domain.PositionLogFilter{
		AccountIDs: accountIDs,
		Limit:      50,
		Offset:     0,
	}).Return(expectedLogs, nil)

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		AccountId:  "IGNORED",
		AccountIds: accountIDs,
		Pagination: &commonv1.Pagination{
			PageSize: 50,
		},
	}

	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	mockRepo.AssertExpectations(t)
}

// TestListFinancialPositionLogs_AccountIDs_MaxLimitValidation verifies the 100-item limit is enforced.
func TestListFinancialPositionLogs_AccountIDs_MaxLimitValidation(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc := mustNewPositionKeepingService(t, mockRepo, mockEventPublisher, mockIdempotency)

	// Build 101 account IDs
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "ACC-001"
	}

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{
		AccountIds: tooMany,
		Pagination: &commonv1.Pagination{
			PageSize: 50,
		},
	}

	resp, err := svc.ListFinancialPositionLogs(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	mockRepo.AssertNotCalled(t, "List")
}
