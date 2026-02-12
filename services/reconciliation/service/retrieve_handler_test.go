package service_test

import (
	"context"
	"errors"
	"sync"
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

// mockSettlementRunRepo implements domain.SettlementRunRepository for testing.
// It is thread-safe to support tests that exercise async pipeline goroutines.
type mockSettlementRunRepo struct {
	mu   sync.RWMutex
	runs map[uuid.UUID]*domain.SettlementRun
	err  error // injected error for testing
}

func newMockSettlementRunRepo() *mockSettlementRunRepo {
	return &mockSettlementRunRepo{runs: make(map[uuid.UUID]*domain.SettlementRun)}
}

func (m *mockSettlementRunRepo) Create(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[run.RunID] = run
	return nil
}

func (m *mockSettlementRunRepo) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return nil, m.err
	}
	run, ok := m.runs[runID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	// Return a copy to match real DB behavior and avoid data races
	// when background goroutines (e.g. resumePipeline) modify the returned object.
	cp := *run
	return &cp, nil
}

func (m *mockSettlementRunRepo) Update(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.RunID]; !ok {
		return domain.ErrNotFound
	}
	m.runs[run.RunID] = run
	return nil
}

func (m *mockSettlementRunRepo) List(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*domain.SettlementRun, 0, len(m.runs))
	for _, r := range m.runs {
		result = append(result, r)
	}
	return result, nil
}

// getRun safely retrieves a run from the mock for test assertions.
func (m *mockSettlementRunRepo) getRun(runID uuid.UUID) *domain.SettlementRun {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runs[runID]
}

func TestRetrieveAccountReconciliation_Success(t *testing.T) {
	repo := newMockSettlementRunRepo()
	now := time.Now().UTC().Truncate(time.Second)
	completedAt := now.Add(-time.Minute)
	runID := uuid.New()

	run := &domain.SettlementRun{
		RunID:          runID,
		AccountID:      "ACC-001",
		Scope:          domain.ReconciliationScopeAccount,
		SettlementType: domain.SettlementTypeDaily,
		Status:         domain.RunStatusCompleted,
		PeriodStart:    now.Add(-24 * time.Hour),
		PeriodEnd:      now,
		InitiatedBy:    "system",
		CompletedAt:    &completedAt,
		VarianceCount:  3,
		Attributes:     map[string]string{"source": "test"},
		CreatedAt:      now.Add(-24 * time.Hour),
		UpdatedAt:      now,
		Version:        2,
	}
	repo.runs[runID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.RetrieveAccountReconciliation(context.Background(), &reconciliationv1.RetrieveAccountReconciliationRequest{
		RunId: runID.String(),
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.GetRun())

	summary := resp.GetRun()
	assert.Equal(t, runID.String(), summary.GetRunId())
	assert.Equal(t, "ACC-001", summary.GetAccountId())
	assert.Equal(t, reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT, summary.GetScope())
	assert.Equal(t, reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY, summary.GetSettlementType())
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_COMPLETED, summary.GetStatus())
	assert.Equal(t, "system", summary.GetInitiatedBy())
	assert.Equal(t, int32(3), summary.GetVarianceCount())
	assert.Equal(t, int64(2), summary.GetVersion())
	assert.NotNil(t, summary.GetCompletedAt())
	assert.NotNil(t, summary.GetPeriodStart())
	assert.NotNil(t, summary.GetPeriodEnd())
	assert.NotNil(t, summary.GetCreatedAt())
	assert.NotNil(t, summary.GetUpdatedAt())
}

func TestRetrieveAccountReconciliation_NotFound(t *testing.T) {
	repo := newMockSettlementRunRepo()

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.RetrieveAccountReconciliation(context.Background(), &reconciliationv1.RetrieveAccountReconciliationRequest{
		RunId: uuid.New().String(),
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "settlement run not found")
}

func TestRetrieveAccountReconciliation_InvalidUUID(t *testing.T) {
	repo := newMockSettlementRunRepo()

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.RetrieveAccountReconciliation(context.Background(), &reconciliationv1.RetrieveAccountReconciliationRequest{
		RunId: "not-a-uuid",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid run_id")
}

func TestRetrieveAccountReconciliation_EmptyRunID(t *testing.T) {
	repo := newMockSettlementRunRepo()

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.RetrieveAccountReconciliation(context.Background(), &reconciliationv1.RetrieveAccountReconciliationRequest{
		RunId: "",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "run_id is required")
}

func TestRetrieveAccountReconciliation_TimestampConversion(t *testing.T) {
	repo := newMockSettlementRunRepo()
	runID := uuid.New()
	now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)

	// Run without CompletedAt (still in progress)
	run := &domain.SettlementRun{
		RunID:          runID,
		AccountID:      "ACC-002",
		Scope:          domain.ReconciliationScopeInstrument,
		SettlementType: domain.SettlementTypeOnDemand,
		Status:         domain.RunStatusRunning,
		PeriodStart:    now.Add(-24 * time.Hour),
		PeriodEnd:      now,
		InitiatedBy:    "operator",
		CompletedAt:    nil,
		CreatedAt:      now.Add(-time.Hour),
		UpdatedAt:      now,
		Version:        1,
	}
	repo.runs[runID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.RetrieveAccountReconciliation(context.Background(), &reconciliationv1.RetrieveAccountReconciliationRequest{
		RunId: runID.String(),
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	summary := resp.GetRun()

	// CompletedAt should be nil when run is still in progress
	assert.Nil(t, summary.GetCompletedAt())

	// Verify PeriodStart timestamp conversion
	assert.Equal(t, now.Add(-24*time.Hour).Unix(), summary.GetPeriodStart().GetSeconds())

	// Verify PeriodEnd timestamp conversion
	assert.Equal(t, now.Unix(), summary.GetPeriodEnd().GetSeconds())

	// Verify CreatedAt timestamp conversion
	assert.Equal(t, now.Add(-time.Hour).Unix(), summary.GetCreatedAt().GetSeconds())

	// Verify UpdatedAt timestamp conversion
	assert.Equal(t, now.Unix(), summary.GetUpdatedAt().GetSeconds())
}

func TestRetrieveAccountReconciliation_InternalError(t *testing.T) {
	repo := newMockSettlementRunRepo()
	repo.err = errors.New("database connection failed")

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.RetrieveAccountReconciliation(context.Background(), &reconciliationv1.RetrieveAccountReconciliationRequest{
		RunId: uuid.New().String(),
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestRetrieveAccountReconciliation_NoRunRepo(t *testing.T) {
	// When no run repo is configured, handler should return Unimplemented
	svc := service.NewAccountReconciliationService()

	resp, err := svc.RetrieveAccountReconciliation(context.Background(), &reconciliationv1.RetrieveAccountReconciliationRequest{
		RunId: uuid.New().String(),
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}
