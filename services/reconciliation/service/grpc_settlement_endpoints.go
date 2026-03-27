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
