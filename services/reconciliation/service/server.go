// Package service implements the gRPC AccountReconciliationService.
//
// Dispute RPCs are implemented in dispute_handler.go. The AssertBalance RPC
// is implemented with cross-account balance assertion logic. Other RPCs
// currently return UNIMPLEMENTED status and will be added in subsequent tasks.
//
//meridian:large-file - known oversized file; split tracked in backlog
package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SnapshotCapturerFunc captures point-in-time position snapshots for a settlement run.
type SnapshotCapturerFunc func(ctx context.Context, runID uuid.UUID) error

// VarianceDetectorFunc detects variances by comparing snapshots for a settlement run.
type VarianceDetectorFunc func(ctx context.Context, runID uuid.UUID) ([]*domain.Variance, error)

// VarianceValuatorFunc values detected variances using the valuation engine.
type VarianceValuatorFunc func(ctx context.Context, runID uuid.UUID) error

// VarianceLister retrieves paginated variance lists.
type VarianceLister interface {
	List(ctx context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error)
}

const (
	// pipelineTimeout is the maximum time allowed for the background reconciliation pipeline.
	pipelineTimeout = 15 * time.Minute

	// persistTimeout is the maximum time allowed for persisting state transitions
	// after the pipeline completes or fails. Uses a fresh context so that state
	// transitions succeed even if the pipeline context has expired.
	persistTimeout = 30 * time.Second
)

// AccountReconciliationService implements the gRPC service for reconciliation operations.
type AccountReconciliationService struct {
	reconciliationv1.UnimplementedAccountReconciliationServiceServer

	runRepo           domain.SettlementRunRepository
	disputeRepo       domain.DisputeRepository
	disputeListRepo   DisputeLister
	assertionListRepo AssertionLister
	varianceRepo      VarianceFinder
	varianceListRepo  VarianceLister
	sagaRuntime       SagaRuntime
	eventPublisher    EventPublisher
	assertor          *BalanceAssertor
	policyRuntime     valuation.PolicyRuntime
	starlarkRuntime   valuation.StarlarkRuntime
	valuationCache    valuation.Cache
	logger            *slog.Logger
	snapshotCapturer  SnapshotCapturerFunc
	varianceDetector  VarianceDetectorFunc
	varianceValuator  VarianceValuatorFunc

	// pauseMu protects pauseSignals map.
	pauseMu      sync.Mutex
	pauseSignals map[uuid.UUID]chan struct{}
}

// Option configures the AccountReconciliationService.
type Option func(*AccountReconciliationService)

// WithDisputeRepository sets the dispute repository.
func WithDisputeRepository(repo domain.DisputeRepository) Option {
	return func(s *AccountReconciliationService) {
		s.disputeRepo = repo
	}
}

// WithSettlementRunRepository sets the settlement run repository.
func WithSettlementRunRepository(repo domain.SettlementRunRepository) Option {
	return func(s *AccountReconciliationService) {
		s.runRepo = repo
	}
}

// WithVarianceRepository sets the variance finder for dispute validation.
func WithVarianceRepository(repo VarianceFinder) Option {
	return func(s *AccountReconciliationService) {
		s.varianceRepo = repo
	}
}

// WithVarianceListRepository sets the variance lister for paginated queries.
func WithVarianceListRepository(repo VarianceLister) Option {
	return func(s *AccountReconciliationService) {
		s.varianceListRepo = repo
	}
}

// WithSagaRuntime sets the saga runtime for dispute resolution.
func WithSagaRuntime(rt SagaRuntime) Option {
	return func(s *AccountReconciliationService) {
		s.sagaRuntime = rt
	}
}

// WithEventPublisher sets the event publisher for domain events.
func WithEventPublisher(pub EventPublisher) Option {
	return func(s *AccountReconciliationService) {
		s.eventPublisher = pub
	}
}

// WithBalanceAssertor sets the balance assertor for the service.
func WithBalanceAssertor(assertor *BalanceAssertor) Option {
	return func(s *AccountReconciliationService) {
		s.assertor = assertor
	}
}

// WithPolicyRuntime sets the CEL policy runtime for valuation.
func WithPolicyRuntime(rt valuation.PolicyRuntime) Option {
	return func(s *AccountReconciliationService) {
		s.policyRuntime = rt
	}
}

// WithStarlarkRuntime sets the Starlark runtime for valuation.
func WithStarlarkRuntime(rt valuation.StarlarkRuntime) Option {
	return func(s *AccountReconciliationService) {
		s.starlarkRuntime = rt
	}
}

// WithValuationCache sets the L1 cache for the valuation engine.
func WithValuationCache(c valuation.Cache) Option {
	return func(s *AccountReconciliationService) {
		s.valuationCache = c
	}
}

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(s *AccountReconciliationService) {
		s.logger = l
	}
}

// WithSnapshotCapturer sets the snapshot capture function for the reconciliation pipeline.
func WithSnapshotCapturer(fn SnapshotCapturerFunc) Option {
	return func(s *AccountReconciliationService) {
		s.snapshotCapturer = fn
	}
}

// WithVarianceDetector sets the variance detection function for the reconciliation pipeline.
func WithVarianceDetector(fn VarianceDetectorFunc) Option {
	return func(s *AccountReconciliationService) {
		s.varianceDetector = fn
	}
}

// WithVarianceValuator sets the variance valuation function for the reconciliation pipeline.
func WithVarianceValuator(fn VarianceValuatorFunc) Option {
	return func(s *AccountReconciliationService) {
		s.varianceValuator = fn
	}
}

// NewAccountReconciliationService creates a new AccountReconciliationService.
// The assertor is optional; if nil, AssertBalance returns UNIMPLEMENTED.
func NewAccountReconciliationService(opts ...Option) *AccountReconciliationService {
	svc := &AccountReconciliationService{
		pauseSignals: make(map[uuid.UUID]chan struct{}),
	}
	for _, opt := range opts {
		opt(svc)
	}
	if svc.logger == nil {
		svc.logger = slog.Default()
	}
	return svc
}

// InitiateAccountReconciliation creates a new settlement run.
func (s *AccountReconciliationService) InitiateAccountReconciliation(
	ctx context.Context,
	req *reconciliationv1.InitiateAccountReconciliationRequest,
) (*reconciliationv1.InitiateAccountReconciliationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.runRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "settlement run repository not configured")
	}

	accountID := req.GetAccountId()
	if accountID == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	scope := req.GetScope()
	if scope == reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "scope must not be UNSPECIFIED")
	}

	settlementType := req.GetSettlementType()
	if settlementType == reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "settlement_type must not be UNSPECIFIED")
	}

	periodStartPb := req.GetPeriodStart()
	if periodStartPb == nil {
		return nil, status.Error(codes.InvalidArgument, "period_start is required")
	}
	if err := periodStartPb.CheckValid(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "period_start is invalid")
	}
	periodStart := periodStartPb.AsTime()

	periodEndPb := req.GetPeriodEnd()
	if periodEndPb == nil {
		return nil, status.Error(codes.InvalidArgument, "period_end is required")
	}
	if err := periodEndPb.CheckValid(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "period_end is invalid")
	}
	periodEnd := periodEndPb.AsTime()

	if !periodStart.Before(periodEnd) {
		return nil, status.Error(codes.InvalidArgument, "period_end must be after period_start")
	}

	initiatedBy := req.GetInitiatedBy()
	if initiatedBy == "" {
		return nil, status.Error(codes.InvalidArgument, "initiated_by is required")
	}

	domainScope := toDomainReconciliationScope(scope)
	domainType := toDomainSettlementType(settlementType)

	run, err := domain.NewSettlementRun(accountID, domainScope, domainType, periodStart, periodEnd, initiatedBy)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid settlement run: %v", err)
	}

	if err := s.runRepo.Create(ctx, run); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil, status.Error(codes.AlreadyExists, "settlement run already exists")
		}
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, "request canceled")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, "deadline exceeded")
		}
		s.logger.Error("failed to create settlement run",
			slog.String("account_id", accountID),
			slog.String("error", err.Error()),
		)
		return nil, status.Error(codes.Internal, "failed to create settlement run")
	}

	s.logger.Info("settlement run created",
		slog.String("run_id", run.RunID.String()),
		slog.String("account_id", accountID),
		slog.String("scope", string(domainScope)),
	)

	return &reconciliationv1.InitiateAccountReconciliationResponse{
		Run: toProtoRunSummary(run),
	}, nil
}

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

// RetrieveAccountReconciliation retrieves a settlement run summary.
func (s *AccountReconciliationService) RetrieveAccountReconciliation(
	ctx context.Context,
	req *reconciliationv1.RetrieveAccountReconciliationRequest,
) (*reconciliationv1.RetrieveAccountReconciliationResponse, error) {
	if s.runRepo == nil {
		return nil, status.Error(codes.Unimplemented, "RetrieveAccountReconciliation not yet implemented")
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

	return &reconciliationv1.RetrieveAccountReconciliationResponse{
		Run: toProtoRunSummary(run),
	}, nil
}

// ListAccountReconciliations returns paginated settlement runs.
func (s *AccountReconciliationService) ListAccountReconciliations(
	ctx context.Context,
	req *reconciliationv1.ListAccountReconciliationsRequest,
) (*reconciliationv1.ListAccountReconciliationsResponse, error) {
	if s.runRepo == nil {
		return nil, status.Error(codes.Unimplemented, "ListAccountReconciliations not yet implemented")
	}

	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 1000 {
		pageSize = 1000
	}

	offset, err := decodeCursor(req.GetPageToken())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
	}

	filter := domain.RunFilter{
		Limit:  pageSize + 1,
		Offset: offset,
	}

	if req.GetAccountId() != "" {
		accountID := req.GetAccountId()
		filter.AccountID = &accountID
	}

	if req.GetStatus() != reconciliationv1.RunStatus_RUN_STATUS_UNSPECIFIED {
		domainStatus := toDomainRunStatus(req.GetStatus())
		filter.Status = &domainStatus
	}

	runs, err := s.runRepo.List(ctx, filter)
	if err != nil {
		s.logger.Error("failed to list settlement runs",
			slog.String("error", err.Error()),
		)
		return nil, status.Error(codes.Internal, "failed to list settlement runs")
	}

	var nextPageToken string
	if len(runs) > pageSize {
		runs = runs[:pageSize]
		nextPageToken = encodeCursor(offset + pageSize)
	}

	summaries := make([]*reconciliationv1.SettlementRunSummary, len(runs))
	for i, run := range runs {
		summaries[i] = toProtoRunSummary(run)
	}

	return &reconciliationv1.ListAccountReconciliationsResponse{
		Runs:          summaries,
		NextPageToken: nextPageToken,
		TotalCount:    -1,
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

// ListReconciliationResults returns paginated variance details for a run.
func (s *AccountReconciliationService) ListReconciliationResults(
	ctx context.Context,
	req *reconciliationv1.ListReconciliationResultsRequest,
) (*reconciliationv1.ListReconciliationResultsResponse, error) {
	if s.varianceListRepo == nil {
		return nil, status.Error(codes.Unimplemented, "ListReconciliationResults not yet implemented")
	}

	runIDStr := req.GetRunId()
	if runIDStr == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id is required")
	}

	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid run_id: %v", err)
	}

	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 1000 {
		pageSize = 1000
	}

	offset, err := decodeCursor(req.GetPageToken())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
	}

	filter := domain.VarianceFilter{
		RunID:  &runID,
		Status: toDomainVarianceStatus(req.GetFilterStatus()),
		Reason: toDomainVarianceReason(req.GetFilterReason()),
		Limit:  pageSize + 1,
		Offset: offset,
	}

	variances, err := s.varianceListRepo.List(ctx, filter)
	if err != nil {
		s.logger.Error("failed to list variances",
			slog.String("run_id", runID.String()),
			slog.String("error", err.Error()),
		)
		return nil, status.Error(codes.Internal, "failed to list variances")
	}

	var nextPageToken string
	if len(variances) > pageSize {
		variances = variances[:pageSize]
		nextPageToken = encodeCursor(offset + pageSize)
	}

	details := make([]*reconciliationv1.VarianceDetail, len(variances))
	for i, v := range variances {
		details[i] = toProtoVarianceDetail(v)
	}

	return &reconciliationv1.ListReconciliationResultsResponse{
		Variances:     details,
		NextPageToken: nextPageToken,
		TotalCount:    -1,
	}, nil
}

// AssertBalance evaluates a balance assertion against current positions.
func (s *AccountReconciliationService) AssertBalance(
	ctx context.Context,
	req *reconciliationv1.AssertBalanceRequest,
) (*reconciliationv1.AssertBalanceResponse, error) {
	if s.assertor == nil {
		return nil, status.Error(codes.Unimplemented, "AssertBalance not yet implemented")
	}

	expectedBalance, err := decimal.NewFromString(req.GetExpectedBalance())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid expected_balance: %v", err)
	}

	var runID *uuid.UUID
	if req.GetRunId() != "" {
		parsed, err := uuid.Parse(req.GetRunId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid run_id: %v", err)
		}
		runID = &parsed
	}

	// Extract caller role from gRPC metadata
	callerRole := extractCallerRole(ctx)

	// Determine scope from expression or default
	scope := inferScope(req.GetExpression(), req.GetAccountId())

	result, err := s.assertor.ExecuteBalanceAssertion(ctx, AssertBalanceRequest{
		AccountID:       req.GetAccountId(),
		InstrumentCode:  req.GetInstrumentCode(),
		Expression:      req.GetExpression(),
		ExpectedBalance: expectedBalance,
		RunID:           runID,
		Scope:           scope,
		CallerRole:      callerRole,
	})
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		if errors.Is(err, domain.ErrUnimplemented) {
			return nil, status.Error(codes.Unimplemented, "NOSTRO_VOSTRO scope not yet implemented")
		}
		if errors.Is(err, domain.ErrEmptyAccountID) ||
			errors.Is(err, domain.ErrEmptyInstrumentCode) ||
			errors.Is(err, domain.ErrEmptyAssertionExpression) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "balance assertion failed")
	}

	return &reconciliationv1.AssertBalanceResponse{
		Assertion: toProtoAssertionDetail(result.Assertion),
	}, nil
}

// extractCallerRole determines the caller's role from validated JWT claims.
// When auth is enabled, the auth interceptor validates the JWT and stores claims
// in context. This function extracts the role from those validated claims.
// When auth is disabled (development mode), no claims are present in context,
// so it falls back to reading the role from gRPC metadata headers.
func extractCallerRole(ctx context.Context) CallerRole {
	// Primary: extract role from validated JWT claims (production path)
	if claims, ok := auth.GetClaimsFromContext(ctx); ok {
		return mapClaimsToCallerRole(claims)
	}

	// Fallback: no JWT claims in context means auth is disabled (development mode).
	// Trust metadata-based role extraction for local development and testing.
	return extractCallerRoleFromMetadata(ctx)
}

// mapClaimsToCallerRole maps validated JWT claims to a CallerRole.
// The role hierarchy uses auth.Role constants from the RBAC system:
//   - auth.RoleService ("service") -> CallerRoleSystem (service-to-service calls)
//   - auth.RoleAdmin ("admin") -> CallerRoleSystem (admin has full access)
//   - auth.RoleAuditor ("auditor") -> CallerRoleAuditor (read-only audit access)
//   - All others -> CallerRoleTenantAdmin (default tenant-scoped access)
func mapClaimsToCallerRole(claims *auth.Claims) CallerRole {
	if claims.HasRole(auth.RoleService.String()) || claims.HasRole(auth.RoleAdmin.String()) {
		return CallerRoleSystem
	}
	if claims.HasRole(auth.RoleAuditor.String()) {
		return CallerRoleAuditor
	}
	return CallerRoleTenantAdmin
}

// extractCallerRoleFromMetadata reads the caller's role from gRPC metadata.
// This is only used when auth is disabled (AUTH_ENABLED=false) for development.
func extractCallerRoleFromMetadata(ctx context.Context) CallerRole {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return CallerRoleTenantAdmin
	}

	roles := md.Get("x-meridian-role")
	if len(roles) == 0 {
		return CallerRoleTenantAdmin
	}

	switch roles[0] {
	case "SYSTEM":
		return CallerRoleSystem
	case "AUDITOR":
		return CallerRoleAuditor
	default:
		return CallerRoleTenantAdmin
	}
}

// inferScope determines the assertion scope from the expression and account ID.
func inferScope(expression, accountID string) domain.AssertionScope {
	// If the expression mentions cross-account or the account is a system marker
	if accountID == "SYSTEM" || accountID == "*" {
		return domain.AssertionScopeCrossAccount
	}
	_ = expression // Expression-based scope inference can be added later
	return domain.AssertionScopePositionLedger
}

// toProtoAssertionDetail converts a domain BalanceAssertion to proto.
func toProtoAssertionDetail(a *domain.BalanceAssertion) *reconciliationv1.BalanceAssertionDetail {
	if a == nil {
		return nil
	}

	detail := &reconciliationv1.BalanceAssertionDetail{
		AssertionId:     a.AssertionID.String(),
		AccountId:       a.AccountID,
		InstrumentCode:  a.InstrumentCode,
		Expression:      a.Expression,
		ExpectedBalance: a.ExpectedBalance.String(),
		ActualBalance:   a.ActualBalance.String(),
		Status:          toProtoAssertionStatus(a.Status),
		FailureReason:   a.FailureReason,
		OverrideReason:  a.OverrideReason,
		CreatedAt:       timestamppb.New(a.CreatedAt),
	}

	if a.RunID != nil {
		detail.RunId = a.RunID.String()
	}

	if !a.AssertedAt.IsZero() {
		detail.AssertedAt = timestamppb.New(a.AssertedAt)
	}

	return detail
}

// toProtoAssertionStatus converts domain AssertionStatus to proto enum.
func toProtoAssertionStatus(s domain.AssertionStatus) reconciliationv1.AssertionStatus {
	switch s {
	case domain.AssertionStatusPending:
		return reconciliationv1.AssertionStatus_ASSERTION_STATUS_PENDING
	case domain.AssertionStatusPassed:
		return reconciliationv1.AssertionStatus_ASSERTION_STATUS_PASSED
	case domain.AssertionStatusFailed:
		return reconciliationv1.AssertionStatus_ASSERTION_STATUS_FAILED
	case domain.AssertionStatusOverride:
		return reconciliationv1.AssertionStatus_ASSERTION_STATUS_OVERRIDE
	default:
		return reconciliationv1.AssertionStatus_ASSERTION_STATUS_UNSPECIFIED
	}
}
