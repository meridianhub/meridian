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
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testRunRepo extends mockSettlementRunRepo with thread-safe access for async tests.
type testRunRepo struct {
	mu   sync.RWMutex
	runs map[uuid.UUID]*domain.SettlementRun
	err  error
}

func newTestRunRepo() *testRunRepo {
	return &testRunRepo{runs: make(map[uuid.UUID]*domain.SettlementRun)}
}

func (m *testRunRepo) Create(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[run.RunID] = run
	return nil
}

func (m *testRunRepo) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return nil, m.err
	}
	run, ok := m.runs[runID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	// Return a copy to avoid data races on the shared struct.
	cp := *run
	return &cp, nil
}

func (m *testRunRepo) Update(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.RunID]; !ok {
		return domain.ErrNotFound
	}
	m.runs[run.RunID] = run
	return nil
}

func (m *testRunRepo) List(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*domain.SettlementRun, 0, len(m.runs))
	for _, r := range m.runs {
		result = append(result, r)
	}
	return result, nil
}

// getStatus returns the current status of a run in a thread-safe way.
func (m *testRunRepo) getStatus(runID uuid.UUID) domain.RunStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[runID]
	if !ok {
		return ""
	}
	return run.Status
}

// getFailureReason returns the failure reason in a thread-safe way.
func (m *testRunRepo) getFailureReason(runID uuid.UUID) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[runID]
	if !ok {
		return ""
	}
	return run.FailureReason
}

func noopCapturer(_ context.Context, _ uuid.UUID) error { return nil }
func noopDetector(_ context.Context, _ uuid.UUID) ([]*domain.Variance, error) {
	return nil, nil
}
func noopValuator(_ context.Context, _ uuid.UUID) error { return nil }

func newServiceWithPipeline(repo domain.SettlementRunRepository, capturer service.SnapshotCapturerFunc, detector service.VarianceDetectorFunc, valuator service.VarianceValuatorFunc) *service.AccountReconciliationService {
	return service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
		service.WithSnapshotCapturer(capturer),
		service.WithVarianceDetector(detector),
		service.WithVarianceValuator(valuator),
	)
}

func TestExecuteAccountReconciliation_Success(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.GetRun())
	assert.Equal(t, run.RunID.String(), resp.GetRun().GetRunId())
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_RUNNING, resp.GetRun().GetStatus())

	// Wait for async pipeline to complete
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusCompleted
		})
	require.NoError(t, err, "run should transition to COMPLETED")
}

func TestExecuteAccountReconciliation_NotFound(t *testing.T) {
	repo := newTestRunRepo()
	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: uuid.New().String(),
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "settlement run not found")
}

func TestExecuteAccountReconciliation_InvalidStatus(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	// Transition to RUNNING so it's no longer PENDING
	require.NoError(t, run.Start())
	repo.runs[run.RunID] = run

	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "not in PENDING state")
}

func TestExecuteAccountReconciliation_AlreadyCompleted(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	require.NoError(t, run.Start())
	require.NoError(t, run.Complete(0))
	repo.runs[run.RunID] = run

	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestExecuteAccountReconciliation_PipelineFailure(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	failingCapturer := func(_ context.Context, _ uuid.UUID) error {
		return errors.New("snapshot capture: connection timeout")
	}

	svc := newServiceWithPipeline(repo, failingCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})

	// RPC should succeed immediately with RUNNING status
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_RUNNING, resp.GetRun().GetStatus())

	// Wait for async pipeline failure
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusFailed
		})
	require.NoError(t, err, "run should transition to FAILED")
}

func TestExecuteAccountReconciliation_ContextDetachment(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	// Use a channel to control when the capturer completes
	capturerStarted := make(chan struct{})
	capturer := func(_ context.Context, _ uuid.UUID) error {
		close(capturerStarted)
		return nil
	}

	// Create a context that we'll cancel immediately after the RPC
	ctx, cancel := context.WithCancel(context.Background())

	svc := newServiceWithPipeline(repo, capturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(ctx,
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Cancel the RPC context immediately
	cancel()

	// The background goroutine should still run despite the cancelled RPC context
	select {
	case <-capturerStarted:
		// Capturer was invoked - context detachment works
	case <-time.After(5 * time.Second):
		t.Fatal("capturer was not invoked - background goroutine may not have been spawned")
	}

	// Pipeline should complete successfully
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusCompleted
		})
	require.NoError(t, err, "run should complete despite cancelled RPC context")
}

func TestExecuteAccountReconciliation_ErrorPersistence(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	expectedErrMsg := "variance detection: instrument not found"
	failingDetector := func(_ context.Context, _ uuid.UUID) ([]*domain.Variance, error) {
		return nil, errors.New(expectedErrMsg)
	}

	svc := newServiceWithPipeline(repo, noopCapturer, failingDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})

	require.NoError(t, err)
	require.NotNil(t, resp)

	// Wait for async pipeline to fail
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusFailed
		})
	require.NoError(t, err, "run should transition to FAILED")

	// Verify the error message was persisted
	reason := repo.getFailureReason(run.RunID)
	assert.Contains(t, reason, expectedErrMsg)
}

func TestExecuteAccountReconciliation_EmptyRunID(t *testing.T) {
	repo := newTestRunRepo()
	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: "",
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "run_id is required")
}

func TestExecuteAccountReconciliation_InvalidUUID(t *testing.T) {
	repo := newTestRunRepo()
	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: "not-a-uuid",
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid run_id")
}

func TestExecuteAccountReconciliation_NoDependencies(t *testing.T) {
	// When pipeline dependencies are not configured, handler should return Unimplemented
	svc := service.NewAccountReconciliationService()

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: uuid.New().String(),
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestExecuteAccountReconciliation_ValuatorFailure(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	failingValuator := func(_ context.Context, _ uuid.UUID) error {
		return errors.New("valuation engine unavailable")
	}

	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, failingValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})

	require.NoError(t, err)
	require.NotNil(t, resp)

	// Wait for async pipeline to fail at valuation step
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusFailed
		})
	require.NoError(t, err, "run should transition to FAILED on valuator failure")

	reason := repo.getFailureReason(run.RunID)
	assert.Contains(t, reason, "valuation engine unavailable")
}
