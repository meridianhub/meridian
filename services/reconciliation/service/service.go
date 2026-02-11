// Package service implements the gRPC AccountReconciliationService.
//
// Dispute RPCs are implemented in dispute_handler.go. The AssertBalance RPC
// is implemented with cross-account balance assertion logic. Other RPCs
// currently return UNIMPLEMENTED status and will be added in subsequent tasks.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
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

	disputeRepo      domain.DisputeRepository
	runRepo          domain.SettlementRunRepository
	varianceRepo     VarianceFinder
	sagaRuntime      SagaRuntime
	eventPublisher   EventPublisher
	assertor         *BalanceAssertor
	policyRuntime    valuation.PolicyRuntime
	starlarkRuntime  valuation.StarlarkRuntime
	valuationCache   valuation.Cache
	snapshotCapturer SnapshotCapturerFunc
	varianceDetector VarianceDetectorFunc
	varianceValuator VarianceValuatorFunc
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
	svc := &AccountReconciliationService{}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// InitiateAccountReconciliation creates a new settlement run.
func (s *AccountReconciliationService) InitiateAccountReconciliation(
	_ context.Context,
	_ *reconciliationv1.InitiateAccountReconciliationRequest,
) (*reconciliationv1.InitiateAccountReconciliationResponse, error) {
	return nil, status.Error(codes.Unimplemented, "InitiateAccountReconciliation not yet implemented")
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
func (s *AccountReconciliationService) executePipeline(ctx context.Context, runID uuid.UUID) {
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

	// Step 1: Capture snapshots
	if err := s.snapshotCapturer(ctx, runID); err != nil {
		slog.Error("snapshot capture failed", "run_id", runID, "error", err)
		s.failRun(ctx, runID, err.Error())
		return
	}

	// Step 2: Detect variances
	if _, err := s.varianceDetector(ctx, runID); err != nil {
		slog.Error("variance detection failed", "run_id", runID, "error", err)
		s.failRun(ctx, runID, err.Error())
		return
	}

	// Step 3: Value variances
	if err := s.varianceValuator(ctx, runID); err != nil {
		slog.Error("variance valuation failed", "run_id", runID, "error", err)
		s.failRun(ctx, runID, err.Error())
		return
	}

	// Pipeline succeeded: transition to COMPLETED.
	// Use a fresh context so persistence succeeds even if the pipeline context has expired.
	persistCtx, persistCancel := context.WithTimeout(context.Background(), persistTimeout) //nolint:contextcheck
	defer persistCancel()
	run, err := s.runRepo.FindByID(persistCtx, runID) //nolint:contextcheck
	if err != nil {
		slog.Error("failed to retrieve run for completion", "run_id", runID, "error", err)
		return
	}
	if err := run.Complete(run.VarianceCount); err != nil {
		slog.Error("failed to transition run to COMPLETED", "run_id", runID, "error", err)
		return
	}
	if err := s.runRepo.Update(persistCtx, run); err != nil { //nolint:contextcheck
		slog.Error("failed to persist COMPLETED state", "run_id", runID, "error", err)
		return
	}

	slog.Info("reconciliation pipeline completed", "run_id", runID)
}

// failRun transitions a settlement run to FAILED with the given error message.
// It uses a fresh context so persistence succeeds even if the pipeline context has expired.
func (s *AccountReconciliationService) failRun(_ context.Context, runID uuid.UUID, errMsg string) {
	persistCtx, persistCancel := context.WithTimeout(context.Background(), persistTimeout) //nolint:contextcheck
	defer persistCancel()
	run, err := s.runRepo.FindByID(persistCtx, runID) //nolint:contextcheck
	if err != nil {
		slog.Error("failed to retrieve run for failure transition", "run_id", runID, "error", err)
		return
	}
	if err := run.Fail(errMsg); err != nil {
		slog.Error("failed to transition run to FAILED", "run_id", runID, "error", err)
		return
	}
	if err := s.runRepo.Update(persistCtx, run); err != nil { //nolint:contextcheck
		slog.Error("failed to persist FAILED state", "run_id", runID, "error", err)
	}
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

// ControlAccountReconciliation controls a settlement run (cancel, pause, resume).
func (s *AccountReconciliationService) ControlAccountReconciliation(
	_ context.Context,
	_ *reconciliationv1.ControlAccountReconciliationRequest,
) (*reconciliationv1.ControlAccountReconciliationResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ControlAccountReconciliation not yet implemented")
}

// ListReconciliationResults returns paginated variance details for a run.
func (s *AccountReconciliationService) ListReconciliationResults(
	_ context.Context,
	_ *reconciliationv1.ListReconciliationResultsRequest,
) (*reconciliationv1.ListReconciliationResultsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ListReconciliationResults not yet implemented")
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

// extractCallerRole reads the caller's role from gRPC metadata.
// TODO: Replace with validated role from auth interceptor/gateway. Currently
// trusts client-supplied metadata which is acceptable for internal service-to-service
// calls but must be secured before external exposure.
func extractCallerRole(ctx context.Context) CallerRole {
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
