package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// RetrieveFinancialPositionLog retrieves a financial position log by ID.
func (s *PositionKeepingService) RetrieveFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.RetrieveFinancialPositionLogRequest,
) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
	// Parse and validate log ID
	logID, err := parseUUID(req.GetLogId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid log_id: %v", err)
	}

	// Retrieve from repository
	log, err := s.repository.FindByID(ctx, logID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial position log not found: %s", logID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve financial position log: %v", err)
	}

	// Convert to protobuf and return
	return &positionkeepingv1.RetrieveFinancialPositionLogResponse{
		Log: toProtoFinancialPositionLog(log),
	}, nil
}

// ListFinancialPositionLogs lists financial position logs with filtering and pagination.
func (s *PositionKeepingService) ListFinancialPositionLogs(
	ctx context.Context,
	req *positionkeepingv1.ListFinancialPositionLogsRequest,
) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	// Validate and extract pagination parameters
	pageSize := int32(50) // Default page size
	offset := 0

	if req.Pagination != nil {
		if req.Pagination.PageSize < 0 {
			return nil, status.Error(codes.InvalidArgument, "page_size must be positive")
		} else if req.Pagination.PageSize > 1000 {
			return nil, status.Error(codes.InvalidArgument, "page_size exceeds maximum of 1000")
		} else if req.Pagination.PageSize > 0 {
			pageSize = req.Pagination.PageSize
		}
		// PageSize == 0 uses the default (50)

		// Parse page token as offset
		if req.Pagination.PageToken != "" {
			parsedOffset, err := strconv.Atoi(req.Pagination.PageToken)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
			}
			if parsedOffset < 0 {
				return nil, status.Error(codes.InvalidArgument, "page_token cannot be negative")
			}
			offset = parsedOffset
		}
	}

	// Build filter
	filter := domain.PositionLogFilter{
		Limit:  int(pageSize),
		Offset: offset,
	}

	// Add account ID filter - account_ids takes precedence over account_id
	if len(req.AccountIds) > 0 {
		if len(req.AccountIds) > 100 {
			return nil, status.Error(codes.InvalidArgument, "account_ids must not exceed 100 items")
		}
		filter.AccountIDs = req.AccountIds
	} else if req.AccountId != "" {
		filter.AccountID = &req.AccountId
	}

	// Add status filter
	if req.Status != commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		domainStatus := fromProtoTransactionStatus(req.Status)
		filter.Status = &domainStatus
	}

	// Add date range filter
	if req.DateRange != nil {
		if req.DateRange.StartDate != "" {
			fromDate, err := time.Parse("2006-01-02", req.DateRange.StartDate)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid start_date format: %v", err)
			}
			filter.FromDate = &fromDate
		}

		if req.DateRange.EndDate != "" {
			toDate, err := time.Parse("2006-01-02", req.DateRange.EndDate)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid end_date format: %v", err)
			}
			// Set to start of next day (exclusive upper bound)
			// This ensures records on end_date are included (< next day midnight)
			toDate = toDate.AddDate(0, 0, 1)
			filter.ToDate = &toDate
		}
	}

	// Check for context cancellation before potentially expensive query
	if err := ctx.Err(); err != nil {
		return nil, status.Errorf(codes.Canceled, "request cancelled: %v", err)
	}

	// Query repository
	logs, err := s.repository.List(ctx, filter)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Errorf(codes.Canceled, "request cancelled: %v", err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Errorf(codes.DeadlineExceeded, "request timed out: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to list financial position logs: %v", err)
	}

	// Convert to protobuf
	protoLogs := make([]*positionkeepingv1.FinancialPositionLog, 0, len(logs))
	for _, log := range logs {
		protoLogs = append(protoLogs, toProtoFinancialPositionLog(log))
	}

	// Build pagination response
	// Note: TotalCount is set to 0 as counting all matching records would be expensive.
	// Clients should paginate using NextPageToken until no more results are returned.
	paginationResp := &commonv1.PaginationResponse{
		TotalCount: -1, // Unknown - would require separate COUNT query
	}

	// If we got a full page, there might be more
	if len(protoLogs) == int(pageSize) {
		nextOffset := offset + int(pageSize)
		paginationResp.NextPageToken = strconv.Itoa(nextOffset)
	}

	return &positionkeepingv1.ListFinancialPositionLogsResponse{
		Logs:       protoLogs,
		Pagination: paginationResp,
	}, nil
}
