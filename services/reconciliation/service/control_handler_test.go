package service_test

import (
	"context"
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

func newPendingRun(t *testing.T) *domain.SettlementRun {
	t.Helper()
	now := time.Now().UTC()
	run, err := domain.NewSettlementRun(
		"ACC-001",
		domain.ReconciliationScopeAccount,
		domain.SettlementTypeDaily,
		now.Add(-24*time.Hour),
		now,
		"system",
	)
	require.NoError(t, err)
	return run
}

func newRunningRun(t *testing.T) *domain.SettlementRun {
	t.Helper()
	run := newPendingRun(t)
	err := run.Start()
	require.NoError(t, err)
	return run
}

func TestControlAccountReconciliation_CancelPending(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  run.RunID.String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.GetRun())
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_CANCELLED, resp.GetRun().GetStatus())
	assert.Equal(t, run.RunID.String(), resp.GetRun().GetRunId())

	// Verify persisted
	updated := repo.runs[run.RunID]
	assert.Equal(t, domain.RunStatusCancelled, updated.Status)
	assert.NotNil(t, updated.CompletedAt)
}

func TestControlAccountReconciliation_CancelRunning(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newRunningRun(t)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  run.RunID.String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.GetRun())
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_CANCELLED, resp.GetRun().GetStatus())

	updated := repo.runs[run.RunID]
	assert.Equal(t, domain.RunStatusCancelled, updated.Status)
}

func TestControlAccountReconciliation_CancelCompleted(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newRunningRun(t)
	err := run.Complete(0)
	require.NoError(t, err)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  run.RunID.String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "cannot cancel run in COMPLETED state")
}

func TestControlAccountReconciliation_PauseRunning(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newRunningRun(t)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  run.RunID.String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.GetRun())
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_PAUSED, resp.GetRun().GetStatus())
	assert.Equal(t, run.RunID.String(), resp.GetRun().GetRunId())

	// Verify persisted
	updated := repo.runs[run.RunID]
	assert.Equal(t, domain.RunStatusPaused, updated.Status)
	require.NotNil(t, updated.LastCompletedPhase)
}

func TestControlAccountReconciliation_PauseCompleted(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newRunningRun(t)
	err := run.Complete(0)
	require.NoError(t, err)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  run.RunID.String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE,
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "cannot pause run in COMPLETED state")
}

func TestControlAccountReconciliation_PausePending(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  run.RunID.String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE,
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "cannot pause run in PENDING state")
}

func newPausedRun(t *testing.T) *domain.SettlementRun {
	t.Helper()
	run := newRunningRun(t)
	phase := domain.PhaseSnapshotCapture
	err := run.Pause(phase)
	require.NoError(t, err)
	return run
}

func TestControlAccountReconciliation_ResumePaused(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newPausedRun(t)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
		service.WithSnapshotCapturer(func(_ context.Context, _ uuid.UUID) error { return nil }),
		service.WithVarianceDetector(func(_ context.Context, _ uuid.UUID) ([]*domain.Variance, error) { return nil, nil }),
		service.WithVarianceValuator(func(_ context.Context, _ uuid.UUID) error { return nil }),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  run.RunID.String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_RESUME,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.GetRun())
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_RUNNING, resp.GetRun().GetStatus())

	// Verify persisted (use thread-safe accessor since resume spawns a background goroutine)
	updated := repo.getRun(run.RunID)
	assert.Equal(t, domain.RunStatusRunning, updated.Status)
}

func TestControlAccountReconciliation_ResumeRunning(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newRunningRun(t)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  run.RunID.String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_RESUME,
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "cannot resume run in RUNNING state")
}

func TestControlAccountReconciliation_ResumeCompleted(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newRunningRun(t)
	err := run.Complete(0)
	require.NoError(t, err)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  run.RunID.String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_RESUME,
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "cannot resume run in COMPLETED state")
}

func TestControlAccountReconciliation_NotFound(t *testing.T) {
	repo := newMockSettlementRunRepo()

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  uuid.New().String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "settlement run not found")
}

func TestControlAccountReconciliation_InvalidUUID(t *testing.T) {
	repo := newMockSettlementRunRepo()

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  "not-a-uuid",
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid run_id")
}

func TestControlAccountReconciliation_EmptyRunID(t *testing.T) {
	repo := newMockSettlementRunRepo()

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  "",
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "run_id is required")
}

func TestControlAccountReconciliation_UnspecifiedAction(t *testing.T) {
	repo := newMockSettlementRunRepo()
	run := newPendingRun(t)
	repo.runs[run.RunID] = run

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(repo),
	)

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  run.RunID.String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_UNSPECIFIED,
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "action is required")
}

func TestControlAccountReconciliation_NoRunRepo(t *testing.T) {
	svc := service.NewAccountReconciliationService()

	resp, err := svc.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  uuid.New().String(),
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}
