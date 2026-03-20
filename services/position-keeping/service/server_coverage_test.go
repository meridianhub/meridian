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
	"github.com/meridianhub/meridian/services/position-keeping/service"
)

func TestFromProtoTransactionStatus(t *testing.T) {
	tests := []struct {
		name     string
		proto    commonv1.TransactionStatus
		expected domain.TransactionStatus
	}{
		{"pending", commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, domain.TransactionStatusPending},
		{"posted", commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, domain.TransactionStatusPosted},
		{"failed", commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED, domain.TransactionStatusFailed},
		{"cancelled", commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED, domain.TransactionStatusCancelled},
		{"reversed", commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED, domain.TransactionStatusReversed},
		{"unspecified defaults to pending", commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED, domain.TransactionStatusPending},
		{"unknown defaults to pending", commonv1.TransactionStatus(999), domain.TransactionStatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.FromProtoTransactionStatusForTesting(tt.proto)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestListFinancialPositionLogs_InvalidPageToken(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	resp, err := svc.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{
		Pagination: &commonv1.Pagination{
			PageSize:  10,
			PageToken: "not-a-number",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid page_token")
}

func TestListFinancialPositionLogs_NegativePageToken(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	resp, err := svc.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{
		Pagination: &commonv1.Pagination{
			PageSize:  10,
			PageToken: "-5",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "page_token cannot be negative")
}

func TestListFinancialPositionLogs_InvalidEndDate(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	resp, err := svc.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{
		DateRange: &commonv1.DateRange{
			StartDate: "2025-01-01",
			EndDate:   "invalid-date",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid end_date")
}

func TestListFinancialPositionLogs_NextPageToken(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	now := time.Now().UTC()

	// Return exactly page_size logs so NextPageToken is set
	logs := make([]*domain.FinancialPositionLog, 10)
	for i := range logs {
		logs[i] = &domain.FinancialPositionLog{
			LogID:                 uuid.New(),
			AccountID:             "ACC-001",
			TransactionLogEntries: []*domain.TransactionLogEntry{},
			AuditTrail:            []*domain.AuditTrailEntry{},
			StatusTracking:        domain.NewStatusTracking(),
			CreatedAt:             now,
			UpdatedAt:             now,
			Version:               1,
		}
	}

	mockRepo.On("List", ctx, domain.PositionLogFilter{
		Limit:  10,
		Offset: 0,
	}).Return(logs, nil)

	resp, err := svc.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{
		Pagination: &commonv1.Pagination{PageSize: 10},
	})

	require.NoError(t, err)
	assert.Len(t, resp.Logs, 10)
	assert.Equal(t, "10", resp.Pagination.NextPageToken)
	mockRepo.AssertExpectations(t)
}

func TestListFinancialPositionLogs_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	resp, err := svc.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Canceled, st.Code())
}

func TestListFinancialPositionLogs_ContextCancelledDuringQuery(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	mockRepo.On("List", ctx, domain.PositionLogFilter{
		Limit:  50,
		Offset: 0,
	}).Return(nil, context.Canceled)

	resp, err := svc.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Canceled, st.Code())
	mockRepo.AssertExpectations(t)
}

func TestListFinancialPositionLogs_DeadlineExceeded(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotency := new(MockIdempotencyService)
	svc := mustNewPositionKeepingService(t, mockRepo, publisher, idempotency)

	mockRepo.On("List", ctx, domain.PositionLogFilter{
		Limit:  50,
		Offset: 0,
	}).Return(nil, context.DeadlineExceeded)

	resp, err := svc.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.DeadlineExceeded, st.Code())
	mockRepo.AssertExpectations(t)
}
