package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// listRunRepo is a mock settlement run repo with full filter support for List tests.
type listRunRepo struct {
	listFn  func(ctx context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error)
	listErr error
}

func (m *listRunRepo) Create(_ context.Context, _ *domain.SettlementRun) error {
	return nil
}

func (m *listRunRepo) FindByID(_ context.Context, _ uuid.UUID) (*domain.SettlementRun, error) {
	return nil, domain.ErrNotFound
}

func (m *listRunRepo) Update(_ context.Context, _ *domain.SettlementRun) error {
	return nil
}

func (m *listRunRepo) List(ctx context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error) {
	if m.listFn != nil {
		return m.listFn(ctx, filter)
	}
	if m.listErr != nil {
		return nil, m.listErr
	}
	return nil, nil
}

func makeSettlementRuns(count int) []*domain.SettlementRun {
	now := time.Now().UTC()
	runs := make([]*domain.SettlementRun, count)
	for i := range count {
		runs[i] = &domain.SettlementRun{
			RunID:          uuid.New(),
			AccountID:      fmt.Sprintf("acct-%03d", i),
			Scope:          domain.ReconciliationScopeAccount,
			SettlementType: domain.SettlementTypeDaily,
			Status:         domain.RunStatusCompleted,
			PeriodStart:    now.Add(-24 * time.Hour),
			PeriodEnd:      now,
			InitiatedBy:    "test-user",
			CreatedAt:      now,
			UpdatedAt:      now,
			Version:        1,
		}
	}
	return runs
}

func TestListAccountReconciliations_FirstPage(t *testing.T) {
	allRuns := makeSettlementRuns(11)
	repo := &listRunRepo{
		listFn: func(_ context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error) {
			end := filter.Limit
			if end > len(allRuns) {
				end = len(allRuns)
			}
			return allRuns[filter.Offset:end], nil
		},
	}

	svc := service.NewAccountReconciliationService(service.WithSettlementRunRepository(repo))
	resp, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{
		PageSize: 10,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.GetRuns(), 10)
	assert.NotEmpty(t, resp.GetNextPageToken())
	assert.Equal(t, int64(-1), resp.GetTotalCount())
}

func TestListAccountReconciliations_LastPage(t *testing.T) {
	allRuns := makeSettlementRuns(5)
	repo := &listRunRepo{
		listFn: func(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
			return allRuns, nil
		},
	}

	svc := service.NewAccountReconciliationService(service.WithSettlementRunRepository(repo))
	resp, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{
		PageSize: 10,
	})

	require.NoError(t, err)
	assert.Len(t, resp.GetRuns(), 5)
	assert.Empty(t, resp.GetNextPageToken())
}

func TestListAccountReconciliations_FilterByStatus(t *testing.T) {
	var capturedFilter domain.RunFilter
	repo := &listRunRepo{
		listFn: func(_ context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error) {
			capturedFilter = filter
			return nil, nil
		},
	}

	svc := service.NewAccountReconciliationService(service.WithSettlementRunRepository(repo))
	_, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{
		Status: reconciliationv1.RunStatus_RUN_STATUS_COMPLETED,
	})

	require.NoError(t, err)
	require.NotNil(t, capturedFilter.Status)
	assert.Equal(t, domain.RunStatusCompleted, *capturedFilter.Status)
}

func TestListAccountReconciliations_FilterByAccountID(t *testing.T) {
	var capturedFilter domain.RunFilter
	repo := &listRunRepo{
		listFn: func(_ context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error) {
			capturedFilter = filter
			return nil, nil
		},
	}

	svc := service.NewAccountReconciliationService(service.WithSettlementRunRepository(repo))
	_, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{
		AccountId: "acct-001",
	})

	require.NoError(t, err)
	require.NotNil(t, capturedFilter.AccountID)
	assert.Equal(t, "acct-001", *capturedFilter.AccountID)
}

func TestListAccountReconciliations_EmptyResults(t *testing.T) {
	repo := &listRunRepo{
		listFn: func(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
			return []*domain.SettlementRun{}, nil
		},
	}

	svc := service.NewAccountReconciliationService(service.WithSettlementRunRepository(repo))
	resp, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{})

	require.NoError(t, err)
	assert.Empty(t, resp.GetRuns())
	assert.Empty(t, resp.GetNextPageToken())
}

func TestListAccountReconciliations_DefaultPageSize(t *testing.T) {
	var capturedFilter domain.RunFilter
	repo := &listRunRepo{
		listFn: func(_ context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error) {
			capturedFilter = filter
			return nil, nil
		},
	}

	svc := service.NewAccountReconciliationService(service.WithSettlementRunRepository(repo))
	_, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{})

	require.NoError(t, err)
	// Default page size is 50; Limit passed to repo is pageSize+1
	assert.Equal(t, 51, capturedFilter.Limit)
}

func TestListAccountReconciliations_MaxPageSize(t *testing.T) {
	var capturedFilter domain.RunFilter
	repo := &listRunRepo{
		listFn: func(_ context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error) {
			capturedFilter = filter
			return nil, nil
		},
	}

	svc := service.NewAccountReconciliationService(service.WithSettlementRunRepository(repo))
	// page_size is validated by buf to max 1000, but handler also clamps it
	_, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{
		PageSize: 1000,
	})

	require.NoError(t, err)
	assert.Equal(t, 1001, capturedFilter.Limit)
}

func TestListAccountReconciliations_InvalidPageToken(t *testing.T) {
	repo := &listRunRepo{}

	svc := service.NewAccountReconciliationService(service.WithSettlementRunRepository(repo))
	resp, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{
		PageToken: "!!!not-valid-base64!!!",
	})

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid page_token")
}

func TestListAccountReconciliations_NoRepoConfigured(t *testing.T) {
	svc := service.NewAccountReconciliationService()

	resp, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{})

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestListAccountReconciliations_InternalError(t *testing.T) {
	repo := &listRunRepo{
		listErr: errors.New("db connection lost"),
	}

	svc := service.NewAccountReconciliationService(service.WithSettlementRunRepository(repo))
	resp, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{})

	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestListAccountReconciliations_NoStatusFilter_WhenUnspecified(t *testing.T) {
	var capturedFilter domain.RunFilter
	repo := &listRunRepo{
		listFn: func(_ context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error) {
			capturedFilter = filter
			return nil, nil
		},
	}

	svc := service.NewAccountReconciliationService(service.WithSettlementRunRepository(repo))
	_, err := svc.ListAccountReconciliations(context.Background(), &reconciliationv1.ListAccountReconciliationsRequest{
		Status: reconciliationv1.RunStatus_RUN_STATUS_UNSPECIFIED,
	})

	require.NoError(t, err)
	assert.Nil(t, capturedFilter.Status, "UNSPECIFIED status should not set filter")
}
