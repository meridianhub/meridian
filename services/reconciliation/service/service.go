// Package service implements the gRPC AccountReconciliationService.
//
// All RPCs currently return UNIMPLEMENTED status. Business logic will be
// added in subsequent tasks after the persistence layer is integrated.
package service

import (
	"context"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AccountReconciliationService implements the gRPC service for reconciliation operations.
type AccountReconciliationService struct {
	reconciliationv1.UnimplementedAccountReconciliationServiceServer
}

// NewAccountReconciliationService creates a new AccountReconciliationService.
func NewAccountReconciliationService() *AccountReconciliationService {
	return &AccountReconciliationService{}
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

// InitiateDispute raises a formal dispute against a variance.
func (s *AccountReconciliationService) InitiateDispute(
	_ context.Context,
	_ *reconciliationv1.InitiateDisputeRequest,
) (*reconciliationv1.InitiateDisputeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "InitiateDispute not yet implemented")
}

// ControlDispute controls a dispute lifecycle (escalate, resolve, reject).
func (s *AccountReconciliationService) ControlDispute(
	_ context.Context,
	_ *reconciliationv1.ControlDisputeRequest,
) (*reconciliationv1.ControlDisputeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ControlDispute not yet implemented")
}

// RetrieveDispute retrieves a dispute by ID.
func (s *AccountReconciliationService) RetrieveDispute(
	_ context.Context,
	_ *reconciliationv1.RetrieveDisputeRequest,
) (*reconciliationv1.RetrieveDisputeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "RetrieveDispute not yet implemented")
}
