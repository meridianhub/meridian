package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DisputeLister retrieves paginated dispute lists.
type DisputeLister interface {
	List(ctx context.Context, filter domain.DisputeFilter) ([]*domain.Dispute, error)
}

// AssertionLister retrieves paginated balance assertion lists.
type AssertionLister interface {
	List(ctx context.Context, filter domain.AssertionFilter) ([]*domain.BalanceAssertion, error)
}

// WithDisputeListRepository sets the dispute lister for paginated queries.
func WithDisputeListRepository(repo DisputeLister) Option {
	return func(s *AccountReconciliationService) {
		s.disputeListRepo = repo
	}
}

// WithAssertionListRepository sets the assertion lister for paginated queries.
func WithAssertionListRepository(repo AssertionLister) Option {
	return func(s *AccountReconciliationService) {
		s.assertionListRepo = repo
	}
}

// ListDisputes returns paginated disputes for a settlement run.
func (s *AccountReconciliationService) ListDisputes(
	ctx context.Context,
	req *reconciliationv1.ListDisputesRequest,
) (*reconciliationv1.ListDisputesResponse, error) {
	if s.disputeListRepo == nil {
		return nil, status.Error(codes.Unimplemented, "ListDisputes not yet implemented")
	}

	runID, err := uuid.Parse(req.GetRunId())
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

	filter := domain.DisputeFilter{
		RunID:  &runID,
		Status: toDomainDisputeStatusFilter(req.GetFilterStatus()),
		Limit:  pageSize + 1,
		Offset: offset,
	}

	disputes, err := s.disputeListRepo.List(ctx, filter)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list disputes")
	}

	var nextPageToken string
	if len(disputes) > pageSize {
		disputes = disputes[:pageSize]
		nextPageToken = encodeCursor(offset + pageSize)
	}

	items := make([]*reconciliationv1.DisputeDetail, len(disputes))
	for i, d := range disputes {
		items[i] = toDisputeDetailProto(d)
	}

	return &reconciliationv1.ListDisputesResponse{
		Items:         items,
		NextPageToken: nextPageToken,
		TotalCount:    -1,
	}, nil
}

// UpdateDispute updates dispute status directly (supports PATCH from frontend).
func (s *AccountReconciliationService) UpdateDispute(
	ctx context.Context,
	req *reconciliationv1.UpdateDisputeRequest,
) (*reconciliationv1.UpdateDisputeResponse, error) {
	if s.disputeRepo == nil {
		return nil, status.Error(codes.Unimplemented, "UpdateDispute not yet implemented")
	}

	disputeID, err := uuid.Parse(req.GetDisputeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid dispute_id: %v", err)
	}

	if _, err := uuid.Parse(req.GetRunId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid run_id: %v", err)
	}

	dispute, err := s.disputeRepo.FindByID(ctx, disputeID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "dispute %s not found", disputeID)
		}
		return nil, status.Error(codes.Internal, "failed to retrieve dispute")
	}

	newStatus := req.GetStatus()
	if newStatus == reconciliationv1.DisputeStatus_DISPUTE_STATUS_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "status must not be UNSPECIFIED")
	}

	switch newStatus { //nolint:exhaustive // UNSPECIFIED handled above
	case reconciliationv1.DisputeStatus_DISPUTE_STATUS_UNDER_REVIEW:
		if err := dispute.Review(); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)
		}
	case reconciliationv1.DisputeStatus_DISPUTE_STATUS_ESCALATED:
		if err := dispute.Escalate(); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)
		}
	case reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED:
		if err := dispute.Resolve(req.GetResolutionNotes(), req.GetResolvedBy()); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)
		}
	case reconciliationv1.DisputeStatus_DISPUTE_STATUS_REJECTED:
		if err := dispute.Reject(req.GetResolutionNotes(), req.GetResolvedBy()); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)
		}
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported target status: %v", newStatus)
	}

	if err := s.disputeRepo.Update(ctx, dispute); err != nil {
		return nil, status.Error(codes.Internal, "failed to update dispute")
	}

	return &reconciliationv1.UpdateDisputeResponse{
		Dispute: toDisputeDetailProto(dispute),
	}, nil
}

// ListBalanceAssertions returns paginated balance assertions for a settlement run.
func (s *AccountReconciliationService) ListBalanceAssertions(
	ctx context.Context,
	req *reconciliationv1.ListBalanceAssertionsRequest,
) (*reconciliationv1.ListBalanceAssertionsResponse, error) {
	if s.assertionListRepo == nil {
		return nil, status.Error(codes.Unimplemented, "ListBalanceAssertions not yet implemented")
	}

	runID, err := uuid.Parse(req.GetRunId())
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

	filter := domain.AssertionFilter{
		RunID:  &runID,
		Limit:  pageSize + 1,
		Offset: offset,
	}

	assertions, err := s.assertionListRepo.List(ctx, filter)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list balance assertions")
	}

	var nextPageToken string
	if len(assertions) > pageSize {
		assertions = assertions[:pageSize]
		nextPageToken = encodeCursor(offset + pageSize)
	}

	items := make([]*reconciliationv1.BalanceAssertionDetail, len(assertions))
	for i, a := range assertions {
		items[i] = toProtoAssertionDetail(a)
	}

	return &reconciliationv1.ListBalanceAssertionsResponse{
		Items:         items,
		NextPageToken: nextPageToken,
		TotalCount:    -1,
	}, nil
}

// toDomainDisputeStatusFilter converts a proto DisputeStatus to domain filter pointer.
// UNSPECIFIED returns nil (no filter).
func toDomainDisputeStatusFilter(s reconciliationv1.DisputeStatus) *domain.DisputeStatus {
	var ds domain.DisputeStatus
	switch s {
	case reconciliationv1.DisputeStatus_DISPUTE_STATUS_OPEN:
		ds = domain.DisputeStatusOpen
	case reconciliationv1.DisputeStatus_DISPUTE_STATUS_UNDER_REVIEW:
		ds = domain.DisputeStatusUnderReview
	case reconciliationv1.DisputeStatus_DISPUTE_STATUS_ESCALATED:
		ds = domain.DisputeStatusEscalated
	case reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED:
		ds = domain.DisputeStatusResolved
	case reconciliationv1.DisputeStatus_DISPUTE_STATUS_REJECTED:
		ds = domain.DisputeStatusRejected
	case reconciliationv1.DisputeStatus_DISPUTE_STATUS_UNSPECIFIED:
		return nil
	}
	return &ds
}
