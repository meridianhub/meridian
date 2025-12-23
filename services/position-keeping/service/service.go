package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

// Service initialization errors
var (
	// ErrRepositoryNil is returned when attempting to create a service with a nil repository
	ErrRepositoryNil = errors.New("position keeping service: repository cannot be nil")
	// ErrEventPublisherNil is returned when attempting to create a service with a nil event publisher
	ErrEventPublisherNil = errors.New("position keeping service: event publisher cannot be nil")
	// ErrIdempotencyServiceNil is returned when attempting to create a service with a nil idempotency service
	ErrIdempotencyServiceNil = errors.New("position keeping service: idempotency service cannot be nil")
)

// PositionKeepingService implements the gRPC service for Position Keeping operations.
type PositionKeepingService struct {
	positionkeepingv1.UnimplementedPositionKeepingServiceServer
	repository     domain.FinancialPositionLogRepository
	eventPublisher domain.EventPublisher
	idempotency    idempotency.Service
}

// NewPositionKeepingService creates a new PositionKeepingService with dependency injection.
//
// Dependencies:
//   - repository: Persistence layer for financial position logs (must not be nil)
//   - eventPublisher: Publishes domain events (must not be nil)
//   - idempotencySvc: Ensures exactly-once processing of idempotent operations (must not be nil)
//
// Returns an error if any dependency is nil.
func NewPositionKeepingService(
	repository domain.FinancialPositionLogRepository,
	eventPublisher domain.EventPublisher,
	idempotencySvc idempotency.Service,
) (*PositionKeepingService, error) {
	if repository == nil {
		return nil, ErrRepositoryNil
	}
	if eventPublisher == nil {
		return nil, ErrEventPublisherNil
	}
	if idempotencySvc == nil {
		return nil, ErrIdempotencyServiceNil
	}

	return &PositionKeepingService{
		repository:     repository,
		eventPublisher: eventPublisher,
		idempotency:    idempotencySvc,
	}, nil
}

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
		if req.Pagination.PageSize == 0 {
			return nil, status.Error(codes.InvalidArgument, "page_size must be positive")
		} else if req.Pagination.PageSize < 0 {
			return nil, status.Error(codes.InvalidArgument, "page_size must be positive")
		} else if req.Pagination.PageSize > 1000 {
			return nil, status.Error(codes.InvalidArgument, "page_size exceeds maximum of 1000")
		}
		pageSize = req.Pagination.PageSize

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

	// Add account ID filter
	if req.AccountId != "" {
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
		TotalCount: 0, // Not implemented - would require separate COUNT query
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

// ControlFinancialPositionLog controls the lifecycle of a financial position log.
// Supports SUSPEND, RESUME, and TERMINATE actions for log processing lifecycle management.
//
// gRPC Error Codes:
//   - codes.InvalidArgument: Invalid log_id format, unspecified control action, or invalid operator_id
//   - codes.NotFound: Financial position log does not exist
//   - codes.FailedPrecondition: Control action not allowed in current state
//   - codes.Internal: Database or system errors
func (s *PositionKeepingService) ControlFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.ControlFinancialPositionLogRequest,
) (*positionkeepingv1.ControlFinancialPositionLogResponse, error) {
	// Parse and validate log ID
	logID, err := parseUUID(req.GetLogId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid log_id: %v", err)
	}

	// Map proto ControlAction to domain ControlAction
	var domainAction domain.ControlAction
	switch req.ControlAction {
	case positionkeepingv1.ControlAction_CONTROL_ACTION_SUSPEND:
		domainAction = domain.ControlActionSuspend
	case positionkeepingv1.ControlAction_CONTROL_ACTION_RESUME:
		domainAction = domain.ControlActionResume
	case positionkeepingv1.ControlAction_CONTROL_ACTION_TERMINATE:
		domainAction = domain.ControlActionTerminate
	case positionkeepingv1.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		return nil, status.Error(codes.InvalidArgument, "control_action must be specified")
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unknown control_action: %v", req.ControlAction)
	}

	// Validate operator_id
	operatorID := req.GetOperatorId()
	if operatorID == "" {
		operatorID = "system" // Default to system if not provided
	}

	// Retrieve position log from repository
	log, err := s.repository.FindByID(ctx, logID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial position log not found: %s", logID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve financial position log: %v", err)
	}

	// Capture previous status before the control action
	previousStatus := log.StatusTracking.CurrentStatus

	// Apply control action via domain model
	if err := log.ControlLog(domainAction, req.GetReason(), operatorID); err != nil {
		// Map domain errors to gRPC status codes
		switch {
		case errors.Is(err, domain.ErrInvalidControlAction):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, domain.ErrCannotSuspend):
			return nil, status.Errorf(codes.FailedPrecondition, "cannot suspend log in current state: %s", previousStatus)
		case errors.Is(err, domain.ErrCannotResume):
			return nil, status.Errorf(codes.FailedPrecondition, "cannot resume log in current state: %s", previousStatus)
		case errors.Is(err, domain.ErrCannotTerminate):
			return nil, status.Errorf(codes.FailedPrecondition, "cannot terminate log in current state: %s", previousStatus)
		case errors.Is(err, domain.ErrAlreadyTerminated):
			return nil, status.Error(codes.FailedPrecondition, "log already terminated")
		case errors.Is(err, domain.ErrEmptyOperatorID):
			return nil, status.Error(codes.InvalidArgument, "operator_id cannot be empty")
		default:
			return nil, status.Errorf(codes.Internal, "control action failed: %v", err)
		}
	}

	// Persist updated log to database
	if err := s.repository.Update(ctx, log); err != nil {
		if errors.Is(err, domain.ErrOptimisticLock) {
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		}
		return nil, status.Errorf(codes.Internal, "failed to update financial position log: %v", err)
	}

	// Emit PositionLogStatusChanged event to Kafka
	event := &domain.PositionLogStatusChanged{
		LogID:          log.LogID,
		AccountID:      log.AccountID,
		PreviousStatus: previousStatus,
		NewStatus:      log.StatusTracking.CurrentStatus,
		ControlAction:  domainAction,
		Reason:         req.GetReason(),
		OperatorID:     operatorID,
		CorrelationID:  logID.String(), // Use log ID as correlation ID
		Timestamp:      time.Now().UTC(),
		Version:        log.Version,
	}
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		// Event publishing is best-effort - errors are logged but don't fail the operation
		// The log has already been updated in the database
		_ = err // Silence lint - TODO: Add structured logging with slog
	}

	// Map domain status to proto PositionLogStatus
	var protoStatus positionkeepingv1.PositionLogStatus
	var protoPreviousStatus positionkeepingv1.PositionLogStatus
	switch log.StatusTracking.CurrentStatus { //nolint:exhaustive // Only mapping to 3 proto values
	case domain.TransactionStatusSuspended:
		protoStatus = positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_SUSPENDED
	case domain.TransactionStatusTerminated:
		protoStatus = positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_TERMINATED
	default:
		protoStatus = positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_ACTIVE
	}

	switch previousStatus { //nolint:exhaustive // Only mapping to 3 proto values
	case domain.TransactionStatusSuspended:
		protoPreviousStatus = positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_SUSPENDED
	case domain.TransactionStatusTerminated:
		protoPreviousStatus = positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_TERMINATED
	default:
		protoPreviousStatus = positionkeepingv1.PositionLogStatus_POSITION_LOG_STATUS_ACTIVE
	}

	return &positionkeepingv1.ControlFinancialPositionLogResponse{
		LogId:          log.LogID.String(),
		Status:         protoStatus,
		Timestamp:      timestamppb.New(log.UpdatedAt),
		PreviousStatus: protoPreviousStatus,
	}, nil
}

// fromProtoTransactionStatus converts protobuf TransactionStatus to domain.
func fromProtoTransactionStatus(status commonv1.TransactionStatus) domain.TransactionStatus {
	switch status {
	case commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING:
		return domain.TransactionStatusPending
	case commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED:
		return domain.TransactionStatusPosted
	case commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED:
		return domain.TransactionStatusFailed
	case commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED:
		return domain.TransactionStatusCancelled
	case commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED:
		return domain.TransactionStatusReversed
	case commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED:
		return domain.TransactionStatusPending // Default unspecified to Pending
	default:
		return domain.TransactionStatusPending
	}
}
