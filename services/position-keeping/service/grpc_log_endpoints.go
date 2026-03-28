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
	pageSize, offset, err := validatePagination(req.Pagination)
	if err != nil {
		return nil, err
	}

	filter, err := buildPositionLogFilter(req, pageSize, offset)
	if err != nil {
		return nil, err
	}

	// Check for context cancellation before potentially expensive query
	if err := ctx.Err(); err != nil {
		return nil, status.Errorf(codes.Canceled, "request cancelled: %v", err)
	}

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

	protoLogs := make([]*positionkeepingv1.FinancialPositionLog, 0, len(logs))
	for _, log := range logs {
		protoLogs = append(protoLogs, toProtoFinancialPositionLog(log))
	}

	return &positionkeepingv1.ListFinancialPositionLogsResponse{
		Logs:       protoLogs,
		Pagination: buildPaginationResponse(len(protoLogs), pageSize, offset),
	}, nil
}

// validatePagination extracts and validates pagination parameters from the request.
func validatePagination(pagination *commonv1.Pagination) (int32, int, error) {
	pageSize := int32(50) // Default page size
	offset := 0

	if pagination == nil {
		return pageSize, offset, nil
	}

	if pagination.PageSize < 0 {
		return 0, 0, status.Error(codes.InvalidArgument, "page_size must be positive")
	} else if pagination.PageSize > 1000 {
		return 0, 0, status.Error(codes.InvalidArgument, "page_size exceeds maximum of 1000")
	} else if pagination.PageSize > 0 {
		pageSize = pagination.PageSize
	}

	if pagination.PageToken != "" {
		parsedOffset, err := strconv.Atoi(pagination.PageToken)
		if err != nil {
			return 0, 0, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
		}
		if parsedOffset < 0 {
			return 0, 0, status.Error(codes.InvalidArgument, "page_token cannot be negative")
		}
		offset = parsedOffset
	}

	return pageSize, offset, nil
}

// buildPositionLogFilter constructs the domain filter from the list request.
func buildPositionLogFilter(req *positionkeepingv1.ListFinancialPositionLogsRequest, pageSize int32, offset int) (domain.PositionLogFilter, error) {
	filter := domain.PositionLogFilter{
		Limit:  int(pageSize),
		Offset: offset,
	}

	if len(req.AccountIds) > 0 {
		if len(req.AccountIds) > 100 {
			return filter, status.Error(codes.InvalidArgument, "account_ids must not exceed 100 items")
		}
		filter.AccountIDs = req.AccountIds
	} else if req.AccountId != "" {
		filter.AccountID = &req.AccountId
	}

	if req.Status != commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		domainStatus := fromProtoTransactionStatus(req.Status)
		filter.Status = &domainStatus
	}

	if req.DateRange != nil {
		if req.DateRange.StartDate != "" {
			fromDate, err := time.Parse("2006-01-02", req.DateRange.StartDate)
			if err != nil {
				return filter, status.Errorf(codes.InvalidArgument, "invalid start_date format: %v", err)
			}
			filter.FromDate = &fromDate
		}

		if req.DateRange.EndDate != "" {
			toDate, err := time.Parse("2006-01-02", req.DateRange.EndDate)
			if err != nil {
				return filter, status.Errorf(codes.InvalidArgument, "invalid end_date format: %v", err)
			}
			toDate = toDate.AddDate(0, 0, 1)
			filter.ToDate = &toDate
		}
	}

	return filter, nil
}

// buildPaginationResponse creates the pagination response with next page token.
func buildPaginationResponse(resultCount int, pageSize int32, offset int) *commonv1.PaginationResponse {
	resp := &commonv1.PaginationResponse{
		TotalCount: -1, // Unknown - would require separate COUNT query
	}

	if resultCount == int(pageSize) {
		nextOffset := offset + int(pageSize)
		resp.NextPageToken = strconv.Itoa(nextOffset)
	}

	return resp
}
