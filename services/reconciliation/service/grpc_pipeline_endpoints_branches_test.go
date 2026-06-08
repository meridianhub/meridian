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

// errUpdate is the sentinel returned by updateFailRepo on Update.
var errUpdate = errors.New("simulated update failure")

// updateFailRepo is a thread-safe settlement run repository where FindByID
// succeeds but Update always fails. This exercises the persistence-failure
// branches in the pipeline endpoints (handleCancel/Pause/Resume, the Execute
// RUNNING-persist path, completePipeline, updateCheckpoint and failRun) that a
// plain in-memory repo cannot reach.
type updateFailRepo struct {
	mu   sync.RWMutex
	runs map[uuid.UUID]*domain.SettlementRun
}

func newUpdateFailRepo() *updateFailRepo {
	return &updateFailRepo{runs: make(map[uuid.UUID]*domain.SettlementRun)}
}

func (m *updateFailRepo) put(run *domain.SettlementRun) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[run.RunID] = run
}

func (m *updateFailRepo) Create(_ context.Context, run *domain.SettlementRun) error {
	m.put(run)
	return nil
}

func (m *updateFailRepo) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[runID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *run
	return &cp, nil
}

func (m *updateFailRepo) Update(_ context.Context, _ *domain.SettlementRun) error {
	return errUpdate
}

func (m *updateFailRepo) List(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*domain.SettlementRun, 0, len(m.runs))
	for _, r := range m.runs {
		result = append(result, r)
	}
	return result, nil
}

func noopControlSvc(repo domain.SettlementRunRepository) *service.AccountReconciliationService {
	return service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
		service.WithSnapshotCapturer(noopCapturer),
		service.WithVarianceDetector(noopDetector),
		service.WithVarianceValuator(noopValuator),
	)
}

// --- FindByID Internal-error branches (non-NotFound) ---

func TestExecuteAccountReconciliation_FindByIDInternalError(t *testing.T) {
	repo := newMockSettlementRunRepo()
	repo.err = errors.New("connection reset")

	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: uuid.New().String(),
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to retrieve settlement run")
}

func TestControlAccountReconciliation_FindByIDInternalError(t *testing.T) {
	repo := newMockSettlementRunRepo()
	repo.err = errors.New("connection reset")

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(),
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  uuid.New().String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to retrieve settlement run")
}

// --- Execute: persist RUNNING state failure ---

func TestExecuteAccountReconciliation_UpdateFailure(t *testing.T) {
	repo := newUpdateFailRepo()
	run := newPendingRun(t)
	repo.put(run)

	svc := noopControlSvc(repo)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to update settlement run")
}

// --- Control action persist-failure branches ---

func TestControlAccountReconciliation_CancelUpdateFailure(t *testing.T) {
	repo := newUpdateFailRepo()
	run := newPendingRun(t)
	repo.put(run)

	svc := noopControlSvc(repo)

	resp, err := svc.ControlAccountReconciliation(context.Background(),
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  run.RunID.String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to persist settlement run")
}

func TestControlAccountReconciliation_PauseUpdateFailure(t *testing.T) {
	repo := newUpdateFailRepo()
	run := newRunningRun(t)
	repo.put(run)

	svc := noopControlSvc(repo)

	resp, err := svc.ControlAccountReconciliation(context.Background(),
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  run.RunID.String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE,
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to persist settlement run")
}

func TestControlAccountReconciliation_ResumeUpdateFailure(t *testing.T) {
	repo := newUpdateFailRepo()
	run := newPausedRun(t)
	repo.put(run)

	svc := noopControlSvc(repo)

	resp, err := svc.ControlAccountReconciliation(context.Background(),
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  run.RunID.String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_RESUME,
		})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to persist settlement run")
}

// --- Pause with a non-nil checkpoint (exercises getCheckpointPhase != nil branch) ---

func TestControlAccountReconciliation_PauseWithCheckpoint(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newRunningRun(t)
	// Record a completed phase so the pause path logs a concrete checkpoint
	// rather than "<none>".
	run.SetCheckpoint(domain.PhaseSnapshotCapture)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(),
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  run.RunID.String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE,
		})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_PAUSED, resp.GetRun().GetStatus())

	updated := repo.getRun(run.RunID)
	assert.Equal(t, domain.RunStatusPaused, updated.Status)
	require.NotNil(t, updated.LastCompletedPhase)
	assert.Equal(t, domain.PhaseSnapshotCapture, *updated.LastCompletedPhase)
}

// --- Pipeline panic recovery -> run transitions to FAILED ---

func TestExecuteAccountReconciliation_PipelinePanicRecovery(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	panicCapturer := func(_ context.Context, _ uuid.UUID) error {
		panic("boom in snapshot capture")
	}

	svc := newServiceWithPipeline(repo, panicCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})

	require.NoError(t, err)
	require.NotNil(t, resp)

	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusFailed
		})
	require.NoError(t, err, "panicking pipeline should recover and mark run FAILED")

	reason := repo.getFailureReason(run.RunID)
	assert.Contains(t, reason, "pipeline panicked")
}

// --- Resume from a mid-pipeline checkpoint: skips completed phases ---

func TestControlAccountReconciliation_ResumeSkipsCompletedPhases(t *testing.T) {
	repo := newTestRunRepo()
	run := newRunningRun(t)
	// Mark snapshot + detection complete, then pause at the valuation boundary.
	phase := domain.PhaseVarianceDetection
	require.NoError(t, run.Pause(&phase))
	repo.runs[run.RunID] = run

	var capturerCalls, detectorCalls, valuatorCalls int
	var mu sync.Mutex
	capturer := func(_ context.Context, _ uuid.UUID) error {
		mu.Lock()
		capturerCalls++
		mu.Unlock()
		return nil
	}
	detector := func(_ context.Context, _ uuid.UUID) ([]*domain.Variance, error) {
		mu.Lock()
		detectorCalls++
		mu.Unlock()
		return nil, nil
	}
	valuator := func(_ context.Context, _ uuid.UUID) error {
		mu.Lock()
		valuatorCalls++
		mu.Unlock()
		return nil
	}

	svc := newServiceWithPipeline(repo, capturer, detector, valuator)

	resp, err := svc.ControlAccountReconciliation(context.Background(),
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  run.RunID.String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_RESUME,
		})
	require.NoError(t, err)
	require.NotNil(t, resp)

	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusCompleted
		})
	require.NoError(t, err, "resumed run should complete")

	mu.Lock()
	defer mu.Unlock()
	// Phases before the checkpoint (capture, detection) must be skipped;
	// only valuation should run.
	assert.Equal(t, 0, capturerCalls, "snapshot capture should be skipped after checkpoint")
	assert.Equal(t, 0, detectorCalls, "variance detection should be skipped after checkpoint")
	assert.Equal(t, 1, valuatorCalls, "variance valuation should run")
}

// --- resolveStartPhase FindByID error inside the background goroutine ---
// findCountRepo fails FindByID only after the first N successful calls. The
// Execute RPC consumes one FindByID; the background goroutine's
// resolveStartPhase then issues the second FindByID, which fails, driving the
// pipeline into failRun. failRun's own FindByID also fails, exercising that
// error branch too.
type findCountRepo struct {
	mu        sync.Mutex
	runs      map[uuid.UUID]*domain.SettlementRun
	calls     int
	failAfter int
}

func newFindCountRepo(failAfter int) *findCountRepo {
	return &findCountRepo{runs: make(map[uuid.UUID]*domain.SettlementRun), failAfter: failAfter}
}

func (m *findCountRepo) Create(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[run.RunID] = run
	return nil
}

func (m *findCountRepo) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls > m.failAfter {
		return nil, errors.New("db unavailable")
	}
	run, ok := m.runs[runID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *run
	return &cp, nil
}

func (m *findCountRepo) Update(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.RunID]; !ok {
		return domain.ErrNotFound
	}
	m.runs[run.RunID] = run
	return nil
}

func (m *findCountRepo) List(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*domain.SettlementRun, 0, len(m.runs))
	for _, r := range m.runs {
		result = append(result, r)
	}
	return result, nil
}

func (m *findCountRepo) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// --- updateCheckpoint + completePipeline persistence-failure branches ---
// updateAfterRunningRepo allows exactly one successful Update (the RUNNING
// transition performed by the Execute RPC) and fails every Update thereafter.
// This lets the background pipeline run while every checkpoint/completion
// persistence fails, exercising updateCheckpoint's and completePipeline's
// error-logging branches.
type updateAfterRunningRepo struct {
	mu      sync.Mutex
	runs    map[uuid.UUID]*domain.SettlementRun
	updates int
}

func newUpdateAfterRunningRepo() *updateAfterRunningRepo {
	return &updateAfterRunningRepo{runs: make(map[uuid.UUID]*domain.SettlementRun)}
}

func (m *updateAfterRunningRepo) Create(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[run.RunID] = run
	return nil
}

func (m *updateAfterRunningRepo) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *run
	return &cp, nil
}

func (m *updateAfterRunningRepo) Update(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates++
	if m.updates > 1 {
		return errUpdate
	}
	m.runs[run.RunID] = run
	return nil
}

func (m *updateAfterRunningRepo) List(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*domain.SettlementRun, 0, len(m.runs))
	for _, r := range m.runs {
		result = append(result, r)
	}
	return result, nil
}

func (m *updateAfterRunningRepo) updateCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updates
}

func TestPipelinePersistFailures_AfterRunning(t *testing.T) {
	repo := newUpdateAfterRunningRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Pipeline runs all 3 phases (3 checkpoint Updates) + completePipeline
	// Update = 4 failing Updates after the initial RUNNING Update. Wait for the
	// completion attempt by counting Update calls (>=5 total).
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return repo.updateCount() >= 5
		})
	require.NoError(t, err, "pipeline should attempt checkpoint and completion persistence")
}

// --- completePipeline FindByID error + failRun Fail/Update error branches ---
// failCountRepo fails FindByID after the Nth call and (optionally) fails Fail's
// Update path. We reuse findCountRepo for the FindByID counting; here we target
// completePipeline's FindByID-error branch by failing FindByID only on the
// completion read.
func TestPipelineCompletion_FindByIDError(t *testing.T) {
	// Execute RPC FindByID (1) succeeds. resolveStartPhase FindByID (2),
	// updateCheckpoint x3 FindByID (3,4,5) succeed. completePipeline FindByID
	// (6) fails. failAfter=5 => the 6th FindByID errors, hitting
	// completePipeline's retrieve-error branch.
	repo := newFindCountRepo(5)
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})
	require.NoError(t, err)
	require.NotNil(t, resp)

	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return repo.callCount() >= 6
		})
	require.NoError(t, err, "completePipeline should attempt FindByID")
}

// --- Pause observed after the variance-detection phase (runPipelinePhases) ---
func TestControlAccountReconciliation_PauseAfterDetection(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	detector := func(_ context.Context, _ uuid.UUID) ([]*domain.Variance, error) {
		once.Do(func() { close(started) })
		<-release
		return nil, nil
	}

	svc := newServiceWithPipeline(repo, noopCapturer, detector, noopValuator)

	_, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})
	require.NoError(t, err)

	<-started
	_, err = svc.ControlAccountReconciliation(context.Background(),
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  run.RunID.String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE,
		})
	require.NoError(t, err)
	close(release)

	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusPaused
		})
	require.NoError(t, err, "pipeline should pause after variance detection")
	assert.NotEqual(t, domain.RunStatusCompleted, repo.getStatus(run.RunID))
}

// --- Pause observed after the variance-valuation phase (runPipelinePhases) ---
func TestControlAccountReconciliation_PauseAfterValuation(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	valuator := func(_ context.Context, _ uuid.UUID) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}

	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, valuator)

	_, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})
	require.NoError(t, err)

	<-started
	_, err = svc.ControlAccountReconciliation(context.Background(),
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  run.RunID.String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE,
		})
	require.NoError(t, err)
	close(release)

	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusPaused
		})
	require.NoError(t, err, "pipeline should pause after variance valuation")
	assert.NotEqual(t, domain.RunStatusCompleted, repo.getStatus(run.RunID))
}

// --- resolveStartPhase indexes a BalanceAssertion checkpoint (phaseIndex) ---
// A run paused at the BalanceAssertion phase resumes with startIndex past every
// implemented phase, so all phases are skipped and the run completes directly.
func TestControlAccountReconciliation_ResumeFromBalanceAssertionCheckpoint(t *testing.T) {
	repo := newTestRunRepo()
	run := newRunningRun(t)
	phase := domain.PhaseBalanceAssertion
	require.NoError(t, run.Pause(&phase))
	repo.runs[run.RunID] = run

	var capturerCalls, detectorCalls, valuatorCalls int
	var mu sync.Mutex
	capturer := func(_ context.Context, _ uuid.UUID) error {
		mu.Lock()
		capturerCalls++
		mu.Unlock()
		return nil
	}
	detector := func(_ context.Context, _ uuid.UUID) ([]*domain.Variance, error) {
		mu.Lock()
		detectorCalls++
		mu.Unlock()
		return nil, nil
	}
	valuator := func(_ context.Context, _ uuid.UUID) error {
		mu.Lock()
		valuatorCalls++
		mu.Unlock()
		return nil
	}

	svc := newServiceWithPipeline(repo, capturer, detector, valuator)

	_, err := svc.ControlAccountReconciliation(context.Background(),
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  run.RunID.String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_RESUME,
		})
	require.NoError(t, err)

	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusCompleted
		})
	require.NoError(t, err, "run resumed past the last phase should complete")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 0, capturerCalls)
	assert.Equal(t, 0, detectorCalls)
	assert.Equal(t, 0, valuatorCalls)
}

// --- updateCheckpoint FindByID-error branch ---
// failAfter=2: Execute RPC FindByID (1) and resolveStartPhase FindByID (2)
// succeed; the first updateCheckpoint FindByID (3, after snapshot capture)
// fails, exercising updateCheckpoint's retrieve-error branch.
func TestPipeline_UpdateCheckpointFindByIDError(t *testing.T) {
	repo := newFindCountRepo(2)
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})
	require.NoError(t, err)
	require.NotNil(t, resp)

	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return repo.callCount() >= 3
		})
	require.NoError(t, err, "updateCheckpoint should attempt FindByID after the first phase")
}

// --- failRun Update-error branch ---
// A failing detector drives the pipeline into failRun. updateAfterRunningRepo
// permits the RUNNING transition Update then fails every later Update, so
// failRun reads the run successfully, transitions it to FAILED, but cannot
// persist - exercising failRun's persist-error branch.
func TestPipeline_FailRunUpdateError(t *testing.T) {
	repo := newUpdateAfterRunningRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	failingDetector := func(_ context.Context, _ uuid.UUID) ([]*domain.Variance, error) {
		return nil, errors.New("detection exploded")
	}

	svc := newServiceWithPipeline(repo, noopCapturer, failingDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Updates: RUNNING (1, ok), updateCheckpoint after capture (2, fails),
	// failRun (3, fails). Wait for the failRun persist attempt.
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return repo.updateCount() >= 3
		})
	require.NoError(t, err, "failRun should attempt to persist the FAILED transition")
}

func TestExecuteAccountReconciliation_ResolveStartPhaseError(t *testing.T) {
	// failAfter=1: the Execute RPC's FindByID succeeds, every subsequent
	// FindByID (resolveStartPhase, then failRun) fails.
	repo := newFindCountRepo(1)
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	svc := newServiceWithPipeline(repo, noopCapturer, noopDetector, noopValuator)

	resp, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// resolveStartPhase fails -> failRun is called -> failRun's FindByID also
	// fails. The goroutine returns without panicking. Confirm the extra
	// FindByID calls happened (resolveStartPhase + failRun) so the error
	// branches were exercised.
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return repo.callCount() >= 3
		})
	require.NoError(t, err, "resolveStartPhase and failRun should both attempt FindByID")
}

// --- Pause mid-pipeline: signalPause delivers to the running goroutine,
// checkPause observes it, and the pipeline stops after a phase. ---

func TestControlAccountReconciliation_PauseStopsRunningPipeline(t *testing.T) {
	repo := newTestRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	// Gate the capturer so the pipeline is reliably in-flight when we pause.
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	capturer := func(_ context.Context, _ uuid.UUID) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}

	svc := newServiceWithPipeline(repo, capturer, noopDetector, noopValuator)

	// Start the pipeline.
	_, err := svc.ExecuteAccountReconciliation(context.Background(),
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: run.RunID.String(),
		})
	require.NoError(t, err)

	// Wait until the capturer is executing, then transition to RUNNING-paused.
	<-started

	// Manually pause via Control. The run is RUNNING, so Pause succeeds and
	// signalPause delivers to the live goroutine's pause channel.
	_, err = svc.ControlAccountReconciliation(context.Background(),
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  run.RunID.String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE,
		})
	require.NoError(t, err)

	// Release the capturer; the pipeline should observe the pause signal after
	// the snapshot phase and stop without completing.
	close(release)

	// The run stays PAUSED (the goroutine returns without transitioning to
	// COMPLETED). Confirm it does not progress to COMPLETED.
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return repo.getStatus(run.RunID) == domain.RunStatusPaused
		})
	require.NoError(t, err, "paused pipeline should remain PAUSED, not COMPLETED")
	assert.NotEqual(t, domain.RunStatusCompleted, repo.getStatus(run.RunID))
}
