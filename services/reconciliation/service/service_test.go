package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	"google.golang.org/protobuf/types/known/timestamppb"
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
			name: "ExecuteAccountReconciliation",
			call: func() error {
				_, err := svc.ExecuteAccountReconciliation(ctx, &reconciliationv1.ExecuteAccountReconciliationRequest{})
				return err
			},
		},
		{
			name: "ListAccountReconciliations",
			call: func() error {
				_, err := svc.ListAccountReconciliations(ctx, &reconciliationv1.ListAccountReconciliationsRequest{})
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

func TestListReconciliationResults_EmptyRunID(t *testing.T) {
	mock := &mockVarianceLister{
		listFn: func(_ context.Context, _ domain.VarianceFilter) ([]*domain.Variance, error) {
			t.Fatal("should not be called with empty run_id")
			return nil, nil
		},
	}

	svc := NewAccountReconciliationService(WithVarianceListRepository(mock))
	_, err := svc.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId: "",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "run_id is required")
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

// --- Mock for InitiateAccountReconciliation tests ---

type initiateRunRepoMock struct {
	mu        sync.Mutex
	runs      map[uuid.UUID]*domain.SettlementRun
	createErr error
}

func newInitiateRunRepoMock() *initiateRunRepoMock {
	return &initiateRunRepoMock{runs: make(map[uuid.UUID]*domain.SettlementRun)}
}

func (m *initiateRunRepoMock) Create(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	if _, exists := m.runs[run.RunID]; exists {
		return domain.ErrConflict
	}
	m.runs[run.RunID] = run
	return nil
}

func (m *initiateRunRepoMock) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return run, nil
}

func (m *initiateRunRepoMock) Update(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[run.RunID] = run
	return nil
}

func (m *initiateRunRepoMock) List(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
	return nil, nil
}

// --- Helper for building a valid request ---

func validInitiateRequest() *reconciliationv1.InitiateAccountReconciliationRequest {
	now := time.Now().UTC()
	return &reconciliationv1.InitiateAccountReconciliationRequest{
		AccountId:      "acct-001",
		Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
		SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
		PeriodStart:    timestamppb.New(now.Add(-24 * time.Hour)),
		PeriodEnd:      timestamppb.New(now),
		InitiatedBy:    "system-admin",
	}
}

// --- InitiateAccountReconciliation Tests ---

func TestInitiateAccountReconciliation_Success(t *testing.T) {
	repo := newInitiateRunRepoMock()
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	req := validInitiateRequest()
	resp, err := svc.InitiateAccountReconciliation(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Run)

	assert.NotEmpty(t, resp.Run.RunId)
	assert.Equal(t, "acct-001", resp.Run.AccountId)
	assert.Equal(t, reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT, resp.Run.Scope)
	assert.Equal(t, reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY, resp.Run.SettlementType)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_PENDING, resp.Run.Status)
	assert.Equal(t, "system-admin", resp.Run.InitiatedBy)
	assert.NotNil(t, resp.Run.PeriodStart)
	assert.NotNil(t, resp.Run.PeriodEnd)
	assert.NotNil(t, resp.Run.CreatedAt)

	// Verify run was persisted
	runID, err := uuid.Parse(resp.Run.RunId)
	require.NoError(t, err)
	stored, err := repo.FindByID(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, "acct-001", stored.AccountID)
}

func TestInitiateAccountReconciliation_MissingAccountID(t *testing.T) {
	repo := newInitiateRunRepoMock()
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	req := validInitiateRequest()
	req.AccountId = ""

	_, err := svc.InitiateAccountReconciliation(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "account_id")
}

func TestInitiateAccountReconciliation_InvalidScope(t *testing.T) {
	repo := newInitiateRunRepoMock()
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	req := validInitiateRequest()
	req.Scope = reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_UNSPECIFIED

	_, err := svc.InitiateAccountReconciliation(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "scope")
}

func TestInitiateAccountReconciliation_InvalidSettlementType(t *testing.T) {
	repo := newInitiateRunRepoMock()
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	req := validInitiateRequest()
	req.SettlementType = reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED

	_, err := svc.InitiateAccountReconciliation(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "settlement_type")
}

func TestInitiateAccountReconciliation_InvalidDates(t *testing.T) {
	repo := newInitiateRunRepoMock()
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	now := time.Now().UTC()

	t.Run("period_end before period_start", func(t *testing.T) {
		req := validInitiateRequest()
		req.PeriodStart = timestamppb.New(now)
		req.PeriodEnd = timestamppb.New(now.Add(-24 * time.Hour))

		_, err := svc.InitiateAccountReconciliation(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "period_end must be after period_start")
	})

	t.Run("period_end equals period_start", func(t *testing.T) {
		req := validInitiateRequest()
		req.PeriodStart = timestamppb.New(now)
		req.PeriodEnd = timestamppb.New(now)

		_, err := svc.InitiateAccountReconciliation(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("missing period_start", func(t *testing.T) {
		req := validInitiateRequest()
		req.PeriodStart = nil

		_, err := svc.InitiateAccountReconciliation(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "period_start")
	})

	t.Run("missing period_end", func(t *testing.T) {
		req := validInitiateRequest()
		req.PeriodEnd = nil

		_, err := svc.InitiateAccountReconciliation(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "period_end")
	})
}

func TestInitiateAccountReconciliation_NilRequest(t *testing.T) {
	repo := newInitiateRunRepoMock()
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	_, err := svc.InitiateAccountReconciliation(ctx, nil)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "request is required")
}

func TestInitiateAccountReconciliation_MissingRunRepo(t *testing.T) {
	svc := NewAccountReconciliationService() // no WithSettlementRunRepository
	ctx := context.Background()

	req := validInitiateRequest()

	_, err := svc.InitiateAccountReconciliation(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "repository not configured")
}

func TestInitiateAccountReconciliation_EmptyInitiatedBy(t *testing.T) {
	repo := newInitiateRunRepoMock()
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	req := validInitiateRequest()
	req.InitiatedBy = ""

	_, err := svc.InitiateAccountReconciliation(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "initiated_by")
}

func TestInitiateAccountReconciliation_EnumMapping(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		scope         reconciliationv1.ReconciliationScope
		expectedScope reconciliationv1.ReconciliationScope
		stType        reconciliationv1.SettlementType
		expectedType  reconciliationv1.SettlementType
	}{
		{
			name:          "INSTRUMENT scope and WEEKLY type",
			scope:         reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT,
			expectedScope: reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT,
			stType:        reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY,
			expectedType:  reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY,
		},
		{
			name:          "PORTFOLIO scope and MONTHLY type",
			scope:         reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO,
			expectedScope: reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO,
			stType:        reconciliationv1.SettlementType_SETTLEMENT_TYPE_MONTHLY,
			expectedType:  reconciliationv1.SettlementType_SETTLEMENT_TYPE_MONTHLY,
		},
		{
			name:          "FULL scope and ON_DEMAND type",
			scope:         reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_FULL,
			expectedScope: reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_FULL,
			stType:        reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND,
			expectedType:  reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND,
		},
		{
			name:          "ACCOUNT scope and END_OF_DAY type",
			scope:         reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			expectedScope: reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			stType:        reconciliationv1.SettlementType_SETTLEMENT_TYPE_END_OF_DAY,
			expectedType:  reconciliationv1.SettlementType_SETTLEMENT_TYPE_END_OF_DAY,
		},
		{
			name:          "REAL_TIME type",
			scope:         reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			expectedScope: reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			stType:        reconciliationv1.SettlementType_SETTLEMENT_TYPE_REAL_TIME,
			expectedType:  reconciliationv1.SettlementType_SETTLEMENT_TYPE_REAL_TIME,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newInitiateRunRepoMock()
			svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))

			req := validInitiateRequest()
			req.Scope = tt.scope
			req.SettlementType = tt.stType

			resp, err := svc.InitiateAccountReconciliation(ctx, req)
			require.NoError(t, err)
			require.NotNil(t, resp.Run)

			assert.Equal(t, tt.expectedScope, resp.Run.Scope)
			assert.Equal(t, tt.expectedType, resp.Run.SettlementType)
		})
	}
}

func TestInitiateAccountReconciliation_TimestampConversion(t *testing.T) {
	repo := newInitiateRunRepoMock()
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	periodStart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	req := validInitiateRequest()
	req.PeriodStart = timestamppb.New(periodStart)
	req.PeriodEnd = timestamppb.New(periodEnd)

	resp, err := svc.InitiateAccountReconciliation(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Run)

	assert.Equal(t, periodStart, resp.Run.PeriodStart.AsTime())
	assert.Equal(t, periodEnd, resp.Run.PeriodEnd.AsTime())
}

func TestInitiateAccountReconciliation_Conflict(t *testing.T) {
	repo := newInitiateRunRepoMock()
	repo.createErr = domain.ErrConflict
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	req := validInitiateRequest()

	_, err := svc.InitiateAccountReconciliation(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.AlreadyExists, st.Code())
}

func TestInitiateAccountReconciliation_InternalError(t *testing.T) {
	repo := newInitiateRunRepoMock()
	repo.createErr = errors.New("database connection failed")
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	req := validInitiateRequest()

	_, err := svc.InitiateAccountReconciliation(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestInitiateAccountReconciliation_ContextCanceled(t *testing.T) {
	repo := newInitiateRunRepoMock()
	repo.createErr = context.Canceled
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	req := validInitiateRequest()

	_, err := svc.InitiateAccountReconciliation(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Canceled, st.Code())
}

func TestInitiateAccountReconciliation_DeadlineExceeded(t *testing.T) {
	repo := newInitiateRunRepoMock()
	repo.createErr = context.DeadlineExceeded
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	req := validInitiateRequest()

	_, err := svc.InitiateAccountReconciliation(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.DeadlineExceeded, st.Code())
}

func TestInitiateAccountReconciliation_InvalidTimestamp(t *testing.T) {
	repo := newInitiateRunRepoMock()
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	ctx := context.Background()

	t.Run("invalid period_start", func(t *testing.T) {
		req := validInitiateRequest()
		req.PeriodStart = &timestamppb.Timestamp{Seconds: -62135596801} // before 0001-01-01

		_, err := svc.InitiateAccountReconciliation(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "period_start")
	})

	t.Run("invalid period_end", func(t *testing.T) {
		req := validInitiateRequest()
		req.PeriodEnd = &timestamppb.Timestamp{Seconds: 253402300800} // after 9999-12-31

		_, err := svc.InitiateAccountReconciliation(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "period_end")
	})
}
