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

// AccountReconciliationService implements the gRPC service for reconciliation operations.
type AccountReconciliationService struct {
	reconciliationv1.UnimplementedAccountReconciliationServiceServer

	runRepo         domain.SettlementRunRepository
	disputeRepo     domain.DisputeRepository
	varianceRepo    VarianceFinder
	sagaRuntime     SagaRuntime
	eventPublisher  EventPublisher
	assertor        *BalanceAssertor
	policyRuntime   valuation.PolicyRuntime
	starlarkRuntime valuation.StarlarkRuntime
	valuationCache  valuation.Cache
	logger          *slog.Logger
}

// Option configures the AccountReconciliationService.
type Option func(*AccountReconciliationService)

// WithDisputeRepository sets the dispute repository.
func WithDisputeRepository(repo domain.DisputeRepository) Option {
	return func(s *AccountReconciliationService) {
		s.disputeRepo = repo
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

// WithRunRepository sets the settlement run repository.
func WithRunRepository(repo domain.SettlementRunRepository) Option {
	return func(s *AccountReconciliationService) {
		s.runRepo = repo
	}
}

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(s *AccountReconciliationService) {
		s.logger = l
	}
}

// NewAccountReconciliationService creates a new AccountReconciliationService.
// The assertor is optional; if nil, AssertBalance returns UNIMPLEMENTED.
func NewAccountReconciliationService(opts ...Option) *AccountReconciliationService {
	svc := &AccountReconciliationService{}
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
	periodStart := periodStartPb.AsTime()

	periodEndPb := req.GetPeriodEnd()
	if periodEndPb == nil {
		return nil, status.Error(codes.InvalidArgument, "period_end is required")
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
func (s *AccountReconciliationService) ExecuteAccountReconciliation(
	_ context.Context,
	_ *reconciliationv1.ExecuteAccountReconciliationRequest,
) (*reconciliationv1.ExecuteAccountReconciliationResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ExecuteAccountReconciliation not yet implemented")
}

// RetrieveAccountReconciliation retrieves a settlement run summary.
func (s *AccountReconciliationService) RetrieveAccountReconciliation(
	_ context.Context,
	_ *reconciliationv1.RetrieveAccountReconciliationRequest,
) (*reconciliationv1.RetrieveAccountReconciliationResponse, error) {
	return nil, status.Error(codes.Unimplemented, "RetrieveAccountReconciliation not yet implemented")
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
