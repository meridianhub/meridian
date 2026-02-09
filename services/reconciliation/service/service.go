// Package service implements the gRPC AccountReconciliationService.
//
// Dispute RPCs are implemented in dispute_handler.go. Other RPCs currently
// return UNIMPLEMENTED status and will be added in subsequent tasks.
package service

import (
	"context"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AccountReconciliationService implements the gRPC service for reconciliation operations.
type AccountReconciliationService struct {
	reconciliationv1.UnimplementedAccountReconciliationServiceServer

	disputeRepo    domain.DisputeRepository
	varianceRepo   VarianceFinder
	sagaRuntime    SagaRuntime
	eventPublisher EventPublisher
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

// NewAccountReconciliationService creates a new AccountReconciliationService.
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
	_ context.Context,
	_ *reconciliationv1.AssertBalanceRequest,
) (*reconciliationv1.AssertBalanceResponse, error) {
	return nil, status.Error(codes.Unimplemented, "AssertBalance not yet implemented")
}
