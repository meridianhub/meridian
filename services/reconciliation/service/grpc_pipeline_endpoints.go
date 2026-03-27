package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ExecuteAccountReconciliation triggers execution of a pending settlement run.
//
// The handler validates the request, transitions the run to RUNNING, returns
// immediately, and spawns a background goroutine to execute the reconciliation
// pipeline (capture -> detect -> value). Clients poll via RetrieveAccountReconciliation.
//
//nolint:contextcheck // Intentionally uses background context for async pipeline that outlives the RPC
func (s *AccountReconciliationService) ExecuteAccountReconciliation(
	ctx context.Context,
	req *reconciliationv1.ExecuteAccountReconciliationRequest,
) (*reconciliationv1.ExecuteAccountReconciliationResponse, error) {
	if s.runRepo == nil || s.snapshotCapturer == nil || s.varianceDetector == nil || s.varianceValuator == nil {
		return nil, status.Error(codes.Unimplemented, "ExecuteAccountReconciliation not yet implemented")
	}

	runIDStr := req.GetRunId()
	if runIDStr == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id is required")
	}

	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid run_id: %v", err)
	}

	run, err := s.runRepo.FindByID(ctx, runID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "settlement run not found: %s", runID)
		}
		slog.ErrorContext(ctx, "failed to retrieve settlement run", "run_id", runID, "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve settlement run")
	}

	if run.Status != domain.RunStatusPending {
		return nil, status.Errorf(codes.FailedPrecondition,
			"settlement run %s is not in PENDING state (current: %s)", runID, run.Status)
	}

	if err := run.Start(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to start settlement run: %v", err)
	}
	if err := s.runRepo.Update(ctx, run); err != nil {
		slog.ErrorContext(ctx, "failed to persist RUNNING state", "run_id", runID, "error", err)
		return nil, status.Error(codes.Internal, "failed to update settlement run")
	}

	// Spawn background goroutine with a detached context so it continues
	// after the RPC returns. The RPC context must not be used here.
	pipelineCtx, pipelineCancel := context.WithTimeout(context.Background(), pipelineTimeout) //nolint:contextcheck // Intentionally detached: pipeline must outlive the RPC context
	go func() {
		defer pipelineCancel()
		s.executePipeline(pipelineCtx, runID)
	}()

	return &reconciliationv1.ExecuteAccountReconciliationResponse{
		Run: toProtoRunSummary(run),
	}, nil
}

// ControlAccountReconciliation controls a settlement run (cancel, pause, resume).
func (s *AccountReconciliationService) ControlAccountReconciliation(
	ctx context.Context,
	req *reconciliationv1.ControlAccountReconciliationRequest,
) (*reconciliationv1.ControlAccountReconciliationResponse, error) {
	if s.runRepo == nil {
		return nil, status.Error(codes.Unimplemented, "ControlAccountReconciliation not yet implemented")
	}

	runIDStr := req.GetRunId()
	if runIDStr == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id is required")
	}

	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid run_id: %v", err)
	}

	action := req.GetAction()
	if action == reconciliationv1.ControlAction_CONTROL_ACTION_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "action is required and must not be UNSPECIFIED")
	}

	run, err := s.runRepo.FindByID(ctx, runID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "settlement run not found: %s", runID)
		}
		slog.ErrorContext(ctx, "failed to retrieve settlement run", "run_id", runID, "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve settlement run")
	}

	switch action { //nolint:exhaustive // UNSPECIFIED is handled above
	case reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL:
		statusBefore := run.Status
		if err := run.Cancel(); err != nil {
			if errors.Is(err, domain.ErrInvalidStatusTransition) {
				return nil, status.Errorf(codes.FailedPrecondition, "cannot cancel run in %s state", run.Status)
			}
			return nil, status.Error(codes.Internal, "failed to cancel settlement run")
		}
		if err := s.runRepo.Update(ctx, run); err != nil {
			slog.ErrorContext(ctx, "failed to persist cancelled run", "run_id", runID, "error", err)
			return nil, status.Error(codes.Internal, "failed to persist settlement run")
		}
		slog.InfoContext(ctx, "settlement run cancelled",
			"run_id", runID,
			"action", action.String(),
			"status_before", statusBefore.String(),
			"status_after", run.Status.String(),
		)

	case reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE:
		checkpoint := getCheckpointPhase(run)
		statusBefore := run.Status
		if err := run.Pause(checkpoint); err != nil {
			if errors.Is(err, domain.ErrInvalidStatusTransition) {
				return nil, status.Errorf(codes.FailedPrecondition, "cannot pause run in %s state", run.Status)
			}
			return nil, status.Error(codes.Internal, "failed to pause settlement run")
		}
		if err := s.runRepo.Update(ctx, run); err != nil {
			slog.ErrorContext(ctx, "failed to persist paused run", "run_id", runID, "error", err)
			return nil, status.Error(codes.Internal, "failed to persist settlement run")
		}
		s.signalPause(runID)
		checkpointStr := "<none>"
		if checkpoint != nil {
			checkpointStr = string(*checkpoint)
		}
		slog.InfoContext(ctx, "settlement run paused",
			"run_id", runID,
			"action", action.String(),
			"status_before", statusBefore.String(),
			"status_after", run.Status.String(),
			"checkpoint", checkpointStr,
		)

	case reconciliationv1.ControlAction_CONTROL_ACTION_RESUME:
		statusBefore := run.Status
		if err := run.Resume(); err != nil {
			if errors.Is(err, domain.ErrInvalidStatusTransition) {
				return nil, status.Errorf(codes.FailedPrecondition, "cannot resume run in %s state", run.Status)
			}
			return nil, status.Error(codes.Internal, "failed to resume settlement run")
		}
		if err := s.runRepo.Update(ctx, run); err != nil {
			slog.ErrorContext(ctx, "failed to persist resumed run", "run_id", runID, "error", err)
			return nil, status.Error(codes.Internal, "failed to persist settlement run")
		}
		// Capture the response before launching the pipeline goroutine to avoid
		// a data race between the background goroutine writing to the run and the
		// response reading it.
		resp := &reconciliationv1.ControlAccountReconciliationResponse{
			Run: toProtoRunSummary(run),
		}
		// Re-launch the pipeline from the checkpoint
		s.resumePipeline(run.RunID) //nolint:contextcheck // resumePipeline intentionally creates a detached context for background pipeline execution
		slog.InfoContext(ctx, "settlement run resumed",
			"run_id", runID,
			"action", action.String(),
			"status_before", statusBefore.String(),
			"status_after", run.Status.String(),
		)
		return resp, nil

	default:
		return nil, status.Errorf(codes.InvalidArgument, "unknown control action: %v", action)
	}

	return &reconciliationv1.ControlAccountReconciliationResponse{
		Run: toProtoRunSummary(run),
	}, nil
}

// executePipeline runs the reconciliation pipeline in the background.
// The caller is responsible for providing a detached context with timeout.
// The pipeline supports checkpointing: on resume from a PAUSED state, phases
// that completed before the pause are skipped based on LastCompletedPhase.
func (s *AccountReconciliationService) executePipeline(ctx context.Context, runID uuid.UUID) {
	pauseCh := s.registerPauseSignal(runID)
	defer s.removePauseSignal(runID)

	defer func() {
		if r := recover(); r != nil {
			slog.Error("reconciliation pipeline panicked",
				"run_id", runID,
				"panic", r,
			)
			s.failRun(ctx, runID, "pipeline panicked")
		}
	}()

	slog.Info("reconciliation pipeline started", "run_id", runID)

	// Determine the starting phase by checking the run's checkpoint.
	startIndex := 0
	persistCtx, persistCancel := context.WithTimeout(context.Background(), persistTimeout)
	run, err := s.runRepo.FindByID(persistCtx, runID) //nolint:contextcheck // uses persist context created above
	persistCancel()
	if err != nil {
		slog.Error("failed to retrieve run for pipeline start", "run_id", runID, "error", err)
		s.failRun(ctx, runID, "failed to retrieve run for pipeline start: "+err.Error())
		return
	}
	if run.LastCompletedPhase != nil {
		startIndex = phaseIndex(*run.LastCompletedPhase) + 1
	}

	// Step 1: Capture snapshots
	if startIndex <= phaseIndex(domain.PhaseSnapshotCapture) {
		if err := s.snapshotCapturer(ctx, runID); err != nil {
			slog.Error("snapshot capture failed", "run_id", runID, "error", err)
			s.failRun(ctx, runID, err.Error())
			return
		}
		s.updateCheckpoint(ctx, runID, domain.PhaseSnapshotCapture)
		if s.checkPause(pauseCh) {
			slog.Info("pipeline paused after snapshot capture", "run_id", runID)
			return
		}
	}

	// Step 2: Detect variances
	if startIndex <= phaseIndex(domain.PhaseVarianceDetection) {
		if _, err := s.varianceDetector(ctx, runID); err != nil {
			slog.Error("variance detection failed", "run_id", runID, "error", err)
			s.failRun(ctx, runID, err.Error())
			return
		}
		s.updateCheckpoint(ctx, runID, domain.PhaseVarianceDetection)
		if s.checkPause(pauseCh) {
			slog.Info("pipeline paused after variance detection", "run_id", runID)
			return
		}
	}

	// Step 3: Value variances
	if startIndex <= phaseIndex(domain.PhaseVarianceValuation) {
		if err := s.varianceValuator(ctx, runID); err != nil {
			slog.Error("variance valuation failed", "run_id", runID, "error", err)
			s.failRun(ctx, runID, err.Error())
			return
		}
		s.updateCheckpoint(ctx, runID, domain.PhaseVarianceValuation)
		if s.checkPause(pauseCh) {
			slog.Info("pipeline paused after variance valuation", "run_id", runID)
			return
		}
	}

	// NOTE: PhaseBalanceAssertion is defined in the domain model but the pipeline step
	// is not yet implemented. When the balance assertion step is added, insert it here
	// before the completion block, following the same checkpoint/pause pattern above.

	// Pipeline succeeded: transition to COMPLETED.
	// Use a fresh context so persistence succeeds even if the pipeline context has expired.
	completeCtx, completeCancel := context.WithTimeout(context.Background(), persistTimeout)
	defer completeCancel()
	run, err = s.runRepo.FindByID(completeCtx, runID) //nolint:contextcheck // uses completion context created above
	if err != nil {
		slog.Error("failed to retrieve run for completion", "run_id", runID, "error", err)
		return
	}
	if err := run.Complete(run.VarianceCount); err != nil {
		slog.Error("failed to transition run to COMPLETED", "run_id", runID, "error", err)
		return
	}
	if err := s.runRepo.Update(completeCtx, run); err != nil { //nolint:contextcheck // uses completion context created above
		slog.Error("failed to persist COMPLETED state", "run_id", runID, "error", err)
		return
	}

	slog.Info("reconciliation pipeline completed", "run_id", runID)
}

// updateCheckpoint persists the last completed phase on the settlement run.
// It uses a fresh context so persistence succeeds even if the pipeline context has expired.
func (s *AccountReconciliationService) updateCheckpoint(_ context.Context, runID uuid.UUID, phase domain.ReconciliationPhase) {
	persistCtx, persistCancel := context.WithTimeout(context.Background(), persistTimeout)
	defer persistCancel()
	run, err := s.runRepo.FindByID(persistCtx, runID) //nolint:contextcheck // uses persist context created above
	if err != nil {
		slog.Error("failed to retrieve run for checkpoint", "run_id", runID, "error", err)
		return
	}
	run.SetCheckpoint(phase)
	if err := s.runRepo.Update(persistCtx, run); err != nil { //nolint:contextcheck // uses persist context created above
		slog.Error("failed to persist checkpoint", "run_id", runID, "phase", string(phase), "error", err)
	}
}

// failRun transitions a settlement run to FAILED with the given error message.
// It uses a fresh context so persistence succeeds even if the pipeline context has expired.
func (s *AccountReconciliationService) failRun(_ context.Context, runID uuid.UUID, errMsg string) {
	persistCtx, persistCancel := context.WithTimeout(context.Background(), persistTimeout)
	defer persistCancel()
	run, err := s.runRepo.FindByID(persistCtx, runID) //nolint:contextcheck // uses persist context created above
	if err != nil {
		slog.Error("failed to retrieve run for failure transition", "run_id", runID, "error", err)
		return
	}
	if err := run.Fail(errMsg); err != nil {
		slog.Error("failed to transition run to FAILED", "run_id", runID, "error", err)
		return
	}
	if err := s.runRepo.Update(persistCtx, run); err != nil { //nolint:contextcheck // uses persist context created above
		slog.Error("failed to persist FAILED state", "run_id", runID, "error", err)
	}
}

// registerPauseSignal creates a pause signal channel for a run and returns it.
func (s *AccountReconciliationService) registerPauseSignal(runID uuid.UUID) chan struct{} {
	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()
	ch := make(chan struct{}, 1)
	s.pauseSignals[runID] = ch
	return ch
}

// signalPause sends a pause signal to the pipeline goroutine for a run.
func (s *AccountReconciliationService) signalPause(runID uuid.UUID) {
	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()
	if ch, ok := s.pauseSignals[runID]; ok {
		select {
		case ch <- struct{}{}:
		default:
			// Already signaled
		}
	}
}

// removePauseSignal cleans up the pause signal channel for a run.
func (s *AccountReconciliationService) removePauseSignal(runID uuid.UUID) {
	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()
	delete(s.pauseSignals, runID)
}

// checkPause returns true if a pause signal has been received for this run.
func (s *AccountReconciliationService) checkPause(pauseCh chan struct{}) bool {
	select {
	case <-pauseCh:
		return true
	default:
		return false
	}
}

// getCheckpointPhase returns the last completed pipeline phase for the run.
// Returns nil if no phase has been completed yet.
func getCheckpointPhase(run *domain.SettlementRun) *domain.ReconciliationPhase {
	return run.LastCompletedPhase
}

// phaseIndex returns the ordinal position of a phase in the pipeline.
// PhaseBalanceAssertion (index 3) is reserved for future use; the pipeline
// currently completes after PhaseVarianceValuation (index 2).
func phaseIndex(phase domain.ReconciliationPhase) int {
	switch phase {
	case domain.PhaseSnapshotCapture:
		return 0
	case domain.PhaseVarianceDetection:
		return 1
	case domain.PhaseVarianceValuation:
		return 2
	case domain.PhaseBalanceAssertion:
		return 3
	default:
		return -1
	}
}

// resumePipeline re-launches the pipeline from the run's checkpoint.
func (s *AccountReconciliationService) resumePipeline(runID uuid.UUID) {
	pipelineCtx, pipelineCancel := context.WithTimeout(context.Background(), pipelineTimeout)
	go func() {
		defer pipelineCancel()
		s.executePipeline(pipelineCtx, runID)
	}()
}
