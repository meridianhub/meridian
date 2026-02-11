package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
	svc := NewAccountReconciliationService(WithRunRepository(repo))
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
	svc := NewAccountReconciliationService(WithRunRepository(repo))
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
	svc := NewAccountReconciliationService(WithRunRepository(repo))
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
	svc := NewAccountReconciliationService(WithRunRepository(repo))
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
	svc := NewAccountReconciliationService(WithRunRepository(repo))
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

func TestInitiateAccountReconciliation_EmptyInitiatedBy(t *testing.T) {
	repo := newInitiateRunRepoMock()
	svc := NewAccountReconciliationService(WithRunRepository(repo))
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
			svc := NewAccountReconciliationService(WithRunRepository(repo))

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
	svc := NewAccountReconciliationService(WithRunRepository(repo))
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
	svc := NewAccountReconciliationService(WithRunRepository(repo))
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
	svc := NewAccountReconciliationService(WithRunRepository(repo))
	ctx := context.Background()

	req := validInitiateRequest()

	_, err := svc.InitiateAccountReconciliation(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}
