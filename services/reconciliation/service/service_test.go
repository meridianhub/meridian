package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockVarianceLister implements VarianceLister for testing.
type mockVarianceLister struct {
	listFn func(ctx context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error)
}

func (m *mockVarianceLister) List(ctx context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error) {
	return m.listFn(ctx, filter)
}

func makeVariances(runID uuid.UUID, count int) []*domain.Variance {
	now := time.Now().UTC()
	variances := make([]*domain.Variance, count)
	for i := range count {
		variances[i] = &domain.Variance{
			VarianceID:     uuid.New(),
			RunID:          runID,
			SnapshotID:     uuid.New(),
			AccountID:      fmt.Sprintf("acc-%03d", i),
			InstrumentCode: "GBP",
			ExpectedAmount: decimal.NewFromInt(1000),
			ActualAmount:   decimal.NewFromInt(999),
			VarianceAmount: decimal.NewFromInt(-1),
			Reason:         domain.VarianceReasonAmountMismatch,
			Status:         domain.VarianceStatusOpen,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}
	return variances
}

func TestNewAccountReconciliationService(t *testing.T) {
	svc := NewAccountReconciliationService()
	require.NotNil(t, svc)
}

func TestAllRPCsReturnUnimplemented(t *testing.T) {
	svc := NewAccountReconciliationService()
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "InitiateAccountReconciliation",
			call: func() error {
				_, err := svc.InitiateAccountReconciliation(ctx, &reconciliationv1.InitiateAccountReconciliationRequest{})
				return err
			},
		},
		{
			name: "ExecuteAccountReconciliation",
			call: func() error {
				_, err := svc.ExecuteAccountReconciliation(ctx, &reconciliationv1.ExecuteAccountReconciliationRequest{})
				return err
			},
		},
		{
			name: "RetrieveAccountReconciliation",
			call: func() error {
				_, err := svc.RetrieveAccountReconciliation(ctx, &reconciliationv1.RetrieveAccountReconciliationRequest{})
				return err
			},
		},
		{
			name: "ControlAccountReconciliation",
			call: func() error {
				_, err := svc.ControlAccountReconciliation(ctx, &reconciliationv1.ControlAccountReconciliationRequest{})
				return err
			},
		},
		{
			name: "ListReconciliationResults",
			call: func() error {
				_, err := svc.ListReconciliationResults(ctx, &reconciliationv1.ListReconciliationResultsRequest{})
				return err
			},
		},
		{
			name: "AssertBalance",
			call: func() error {
				_, err := svc.AssertBalance(ctx, &reconciliationv1.AssertBalanceRequest{})
				return err
			},
		},
		{
			name: "InitiateDispute",
			call: func() error {
				_, err := svc.InitiateDispute(ctx, &reconciliationv1.InitiateDisputeRequest{})
				return err
			},
		},
		{
			name: "ControlDispute",
			call: func() error {
				_, err := svc.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{})
				return err
			},
		},
		{
			name: "RetrieveDispute",
			call: func() error {
				_, err := svc.RetrieveDispute(ctx, &reconciliationv1.RetrieveDisputeRequest{})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok, "error should be a gRPC status")
			assert.Equal(t, codes.Unimplemented, st.Code())
		})
	}
}

func TestListReconciliationResults_FirstPage(t *testing.T) {
	runID := uuid.New()
	// Return 11 results (page_size+1) to indicate more pages exist
	allVariances := makeVariances(runID, 11)

	mock := &mockVarianceLister{
		listFn: func(_ context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error) {
			assert.Equal(t, runID, *filter.RunID)
			assert.Equal(t, 11, filter.Limit) // page_size + 1
			assert.Equal(t, 0, filter.Offset)
			return allVariances, nil
		},
	}

	svc := NewAccountReconciliationService(WithVarianceListRepository(mock))
	resp, err := svc.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId:    runID.String(),
		PageSize: 10,
	})

	require.NoError(t, err)
	assert.Len(t, resp.Variances, 10)
	assert.NotEmpty(t, resp.NextPageToken, "next_page_token should be present when more results exist")
	assert.Equal(t, int64(-1), resp.TotalCount)
}

func TestListReconciliationResults_LastPage(t *testing.T) {
	runID := uuid.New()
	// Return exactly page_size results (no extra), indicating last page
	allVariances := makeVariances(runID, 5)

	mock := &mockVarianceLister{
		listFn: func(_ context.Context, _ domain.VarianceFilter) ([]*domain.Variance, error) {
			return allVariances, nil
		},
	}

	svc := NewAccountReconciliationService(WithVarianceListRepository(mock))
	resp, err := svc.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId:    runID.String(),
		PageSize: 10,
	})

	require.NoError(t, err)
	assert.Len(t, resp.Variances, 5)
	assert.Empty(t, resp.NextPageToken, "next_page_token should be empty on last page")
}

func TestListReconciliationResults_FilterByStatus(t *testing.T) {
	runID := uuid.New()

	mock := &mockVarianceLister{
		listFn: func(_ context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error) {
			require.NotNil(t, filter.Status)
			assert.Equal(t, domain.VarianceStatusInvestigating, *filter.Status)
			return nil, nil
		},
	}

	svc := NewAccountReconciliationService(WithVarianceListRepository(mock))
	resp, err := svc.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId:        runID.String(),
		FilterStatus: reconciliationv1.VarianceStatus_VARIANCE_STATUS_INVESTIGATING,
	})

	require.NoError(t, err)
	assert.Empty(t, resp.Variances)
}

func TestListReconciliationResults_FilterByReason(t *testing.T) {
	runID := uuid.New()

	mock := &mockVarianceLister{
		listFn: func(_ context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error) {
			require.NotNil(t, filter.Reason)
			assert.Equal(t, domain.VarianceReasonMissingEntry, *filter.Reason)
			return nil, nil
		},
	}

	svc := NewAccountReconciliationService(WithVarianceListRepository(mock))
	resp, err := svc.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId:        runID.String(),
		FilterReason: reconciliationv1.VarianceReason_VARIANCE_REASON_MISSING_ENTRY,
	})

	require.NoError(t, err)
	assert.Empty(t, resp.Variances)
}

func TestListReconciliationResults_EmptyResults(t *testing.T) {
	runID := uuid.New()

	mock := &mockVarianceLister{
		listFn: func(_ context.Context, _ domain.VarianceFilter) ([]*domain.Variance, error) {
			return nil, nil
		},
	}

	svc := NewAccountReconciliationService(WithVarianceListRepository(mock))
	resp, err := svc.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId: runID.String(),
	})

	require.NoError(t, err)
	assert.Empty(t, resp.Variances)
	assert.Empty(t, resp.NextPageToken)
}

func TestListReconciliationResults_InvalidRunID(t *testing.T) {
	mock := &mockVarianceLister{
		listFn: func(_ context.Context, _ domain.VarianceFilter) ([]*domain.Variance, error) {
			t.Fatal("should not be called with invalid run_id")
			return nil, nil
		},
	}

	svc := NewAccountReconciliationService(WithVarianceListRepository(mock))
	_, err := svc.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId: "not-a-uuid",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListReconciliationResults_DefaultPageSize(t *testing.T) {
	runID := uuid.New()

	mock := &mockVarianceLister{
		listFn: func(_ context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error) {
			assert.Equal(t, 51, filter.Limit, "default page_size=50 means limit=51")
			return nil, nil
		},
	}

	svc := NewAccountReconciliationService(WithVarianceListRepository(mock))
	_, err := svc.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId: runID.String(),
		// No PageSize set - should default to 50
	})

	require.NoError(t, err)
}

func TestListReconciliationResults_MaxPageSize(t *testing.T) {
	runID := uuid.New()

	mock := &mockVarianceLister{
		listFn: func(_ context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error) {
			assert.Equal(t, 1001, filter.Limit, "page_size capped at 1000, limit=1001")
			return nil, nil
		},
	}

	svc := NewAccountReconciliationService(WithVarianceListRepository(mock))
	_, err := svc.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId:    runID.String(),
		PageSize: 5000,
	})

	require.NoError(t, err)
}
