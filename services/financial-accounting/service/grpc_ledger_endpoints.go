package service

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
)

// ListFinancialBookingLogs lists booking logs with optional filtering and pagination.
//
// This method implements subtask 9.5 - list operation with filtering.
//
// Supports:
//   - Cursor-based pagination (page_size, page_token)
//   - Status filtering (e.g., PENDING, POSTED)
//   - Business unit filtering
//
// gRPC Error Codes:
//   - codes.InvalidArgument: Invalid pagination or filter parameters
//   - codes.Internal: Database or system errors
//
// Example:
//
//	req := &financialaccountingv1.ListFinancialBookingLogsRequest{
//	    Pagination: &commonv1.Pagination{PageSize: 20},
//	    Status: commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
//	}
//	resp, err := service.ListFinancialBookingLogs(ctx, req)
func (s *FinancialAccountingService) ListFinancialBookingLogs(
	ctx context.Context,
	req *financialaccountingv1.ListFinancialBookingLogsRequest,
) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
	// Parse pagination parameters
	pageSize := int32(50) // Default
	pageToken := ""
	if req.GetPagination() != nil {
		// If pagination is explicitly provided with page_size=0, reject it
		if req.Pagination.PageSize == 0 {
			return nil, status.Errorf(codes.InvalidArgument, "page_size must be between 1 and 1000")
		}
		if req.Pagination.PageSize > 0 {
			pageSize = req.Pagination.PageSize
		}
		pageToken = req.Pagination.PageToken
	}

	// Validate page size
	if pageSize < 1 || pageSize > 1000 {
		return nil, status.Errorf(codes.InvalidArgument, "page_size must be between 1 and 1000")
	}

	// Build repository query parameters
	params := persistence.ListBookingLogsParams{
		PageSize:  int(pageSize),
		PageToken: pageToken,
	}

	// Apply status filter if provided
	if req.Status != commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		params.StatusFilter = fromProtoTransactionStatus(req.Status).String()
	}

	// Apply business unit filter if provided
	if req.BusinessUnitReference != "" {
		params.BusinessUnitFilter = req.BusinessUnitReference
	}

	// Execute repository query
	result, err := s.repository.ListBookingLogs(ctx, params)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list booking logs")
	}

	// Convert domain models to protobuf
	protoLogs := make([]*financialaccountingv1.FinancialBookingLog, len(result.BookingLogs))
	for i, log := range result.BookingLogs {
		protoLogs[i] = toProtoFinancialBookingLog(log)
	}

	// Build response with pagination metadata
	return &financialaccountingv1.ListFinancialBookingLogsResponse{
		FinancialBookingLogs: protoLogs,
		Pagination: &commonv1.PaginationResponse{
			NextPageToken: result.NextPageToken,
			TotalCount:    result.TotalCount,
		},
	}, nil
}

// ListLedgerPostings lists ledger postings with optional filtering and pagination.
//
// This method implements subtask 9.5 - list operation with filtering for ledger postings.
//
// Supports:
//   - Cursor-based pagination (page_size, page_token)
//   - BookingLogID filtering (filter by parent booking log)
//   - AccountID filtering (filter by account identifier)
//   - PostingDirection filtering (DEBIT or CREDIT)
//   - Date range filtering (value_date_from, value_date_to)
//   - Currency filtering (filter by currency code)
//   - Status filtering (filter by transaction status)
//
// gRPC Error Codes:
//   - codes.InvalidArgument: Invalid pagination or filter parameters
//   - codes.Internal: Database or system errors
//
// Example:
//
//	req := &financialaccountingv1.ListLedgerPostingsRequest{
//	    Pagination: &commonv1.Pagination{PageSize: 20},
//	    FinancialBookingLogId: "550e8400-e29b-41d4-a716-446655440000",
//	    PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
//	}
//	resp, err := service.ListLedgerPostings(ctx, req)
func (s *FinancialAccountingService) ListLedgerPostings(
	ctx context.Context,
	req *financialaccountingv1.ListLedgerPostingsRequest,
) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
	// Parse pagination parameters
	pageSize := int32(50) // Default
	pageToken := ""
	if req.GetPagination() != nil {
		// If pagination is explicitly provided with page_size=0, reject it
		if req.Pagination.PageSize == 0 {
			return nil, status.Errorf(codes.InvalidArgument, "page_size must be between 1 and 1000")
		}
		if req.Pagination.PageSize > 0 {
			pageSize = req.Pagination.PageSize
		}
		pageToken = req.Pagination.PageToken
	}

	// Validate page size
	if pageSize < 1 || pageSize > 1000 {
		return nil, status.Errorf(codes.InvalidArgument, "page_size must be between 1 and 1000")
	}

	// Build repository query parameters
	params := persistence.ListPostingsParams{
		PageSize:  int(pageSize),
		PageToken: pageToken,
	}

	// Apply booking log ID filter if provided
	if req.FinancialBookingLogId != "" {
		bookingLogID, err := parseUUID(req.FinancialBookingLogId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid financial_booking_log_id: %v", err)
		}
		params.BookingLogID = &bookingLogID
	}

	// Apply account ID filter - account_ids takes precedence over account_id
	if len(req.AccountIds) > 0 {
		if len(req.AccountIds) > 100 {
			return nil, status.Error(codes.InvalidArgument, "account_ids must not exceed 100 items")
		}
		params.AccountIDs = req.AccountIds
	} else if req.AccountId != "" {
		params.AccountID = req.AccountId
	}

	// Apply posting direction filter if provided
	if req.PostingDirection != commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED {
		params.PostingDirection = fromProtoPostingDirection(req.PostingDirection).String()
	}

	// Apply value date range filters if provided
	if req.ValueDateFrom != nil {
		valueDateFrom := req.ValueDateFrom.AsTime()
		params.ValueDateFrom = &valueDateFrom
	}
	if req.ValueDateTo != nil {
		valueDateTo := req.ValueDateTo.AsTime()
		params.ValueDateTo = &valueDateTo
	}

	// Validate date range if both dates provided
	if req.ValueDateFrom != nil && req.ValueDateTo != nil {
		from := req.ValueDateFrom.AsTime()
		to := req.ValueDateTo.AsTime()
		if from.After(to) {
			return nil, status.Error(codes.InvalidArgument, "value_date_from must be before or equal to value_date_to")
		}
	}

	// Apply instrument code filter if provided (field is named "currency" for backwards compatibility)
	if req.Currency != "" {
		if s.instrumentResolver != nil {
			if _, err := s.instrumentResolver.Resolve(ctx, req.Currency); err != nil {
				if errors.Is(err, refdata.ErrUnknownInstrument) {
					return nil, status.Errorf(codes.InvalidArgument, "unknown instrument code: %s", req.Currency)
				}
				return nil, status.Errorf(codes.Unavailable, "instrument lookup failed for %s, please retry", req.Currency)
			}
		}
		params.Currency = req.Currency
	}

	// Apply status filter if provided
	if req.Status != commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		params.Status = fromProtoTransactionStatus(req.Status).String()
	}

	// Execute repository query
	result, err := s.repository.ListPostings(ctx, params)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list ledger postings")
	}

	// Convert domain models to protobuf
	protoPostings := make([]*financialaccountingv1.LedgerPosting, len(result.Postings))
	for i, posting := range result.Postings {
		protoPostings[i] = toProtoLedgerPosting(posting)
	}

	// Build response with pagination metadata
	return &financialaccountingv1.ListLedgerPostingsResponse{
		LedgerPostings: protoPostings,
		Pagination: &commonv1.PaginationResponse{
			NextPageToken: result.NextPageToken,
			TotalCount:    result.TotalCount,
		},
	}, nil
}
