// Package service implements the gRPC AccountReconciliationService.
//
// RPC handlers are split across domain-specific endpoint files:
//   - grpc_settlement_endpoints.go: Initiate, Retrieve, List settlement runs and results
//   - grpc_pipeline_endpoints.go: Execute, Control, and pipeline internals
//   - grpc_assertion_endpoints.go: AssertBalance and caller role extraction
//   - grpc_dispute_endpoints.go: Dispute operations
//   - grpc_list_endpoints.go: Dispute and assertion listing
package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
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
