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
	"github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
)

// Service initialization errors
var (
	// ErrRepositoryNil is returned when attempting to create a service with a nil repository
	ErrRepositoryNil = errors.New("position keeping service: repository cannot be nil")
	// ErrEventPublisherNil is returned when attempting to create a service with a nil event publisher
	ErrEventPublisherNil = errors.New("position keeping service: event publisher cannot be nil")
	// ErrIdempotencyServiceNil is returned when attempting to create a service with a nil idempotency service
	ErrIdempotencyServiceNil = errors.New("position keeping service: idempotency service cannot be nil")
	// ErrMeasurementRepoNil is returned when attempting to create a service with a nil measurement repository
	ErrMeasurementRepoNil = errors.New("position keeping service: measurement repository cannot be nil")
	// ErrOutboxPublisherNil is returned when attempting to create a service with a nil outbox publisher
	ErrOutboxPublisherNil = errors.New("position keeping service: outbox publisher cannot be nil")
)

// MaxBucketsPerAccountInstrument is the maximum number of distinct buckets allowed
// per account/instrument combination. This is a safety valve to prevent "Infinite Buckets"
// DOS attacks where a malicious or misconfigured instrument could create unbounded
// numbers of buckets. Most legitimate accounts will have far fewer buckets.
const MaxBucketsPerAccountInstrument = 10000

// PositionKeepingService implements the gRPC service for Position Keeping operations.
type PositionKeepingService struct {
	positionkeepingv1.UnimplementedPositionKeepingServiceServer
	repository      domain.FinancialPositionLogRepository
	measurementRepo domain.MeasurementRepository
	eventPublisher  domain.EventPublisher
	outboxPublisher *messaging.OutboxEventPublisher
	idempotency     idempotency.Service
	// instrumentCache is OPTIONAL - if nil, CEL validation is skipped.
	// This allows backwards compatibility with existing deployments.
	instrumentCache InstrumentCache
	// bucketCounter is OPTIONAL - if nil, cardinality checking is skipped.
	// Used to enforce MaxBucketsPerAccountInstrument limit.
	bucketCounter BucketCounter
	// currentAccountClient is OPTIONAL - if nil, Reserve/Available/Free balance
	// computations will fail with a FailedPrecondition error.
	// Used to query active liens (amount blocks) from Current Account service.
	currentAccountClient domain.CurrentAccountClient
	// accountValidator is OPTIONAL - if nil, account validation is skipped.
	// When set along with accountValidationEnabled, validates that accounts exist
	// in Current Account service before creating position logs.
	accountValidator AccountValidator
	// accountValidationEnabled controls whether account validation is performed.
	// Both this flag and accountValidator must be set for validation to occur.
	// Defaults to false for backwards compatibility.
	accountValidationEnabled bool
	// reservationRepo is OPTIONAL - if nil, reservation RPCs return FailedPrecondition.
	reservationRepo domain.ReservationRepository
	// positionRepo is OPTIONAL - if nil, projected balance RPCs return FailedPrecondition.
	positionRepo domain.PositionRepository
	// instrumentResolver is OPTIONAL - if nil, asset instrument resolution falls back to defaults.
	// When set, resolves instrument dimension and precision from Reference Data.
	instrumentResolver refdata.InstrumentResolver
}

// Option configures optional dependencies for PositionKeepingService.
type Option func(*PositionKeepingService)

// WithInstrumentCache sets an optional instrument cache for CEL validation.
// If not set or set to nil, CEL validation is skipped for backwards compatibility.
func WithInstrumentCache(cache InstrumentCache) Option {
	return func(s *PositionKeepingService) {
		s.instrumentCache = cache
	}
}

// WithBucketCounter sets an optional bucket counter for cardinality enforcement.
// If not set or set to nil, cardinality checking is skipped.
// When set, the service will reject measurements that would exceed
// MaxBucketsPerAccountInstrument buckets for any account/instrument combination.
func WithBucketCounter(counter BucketCounter) Option {
	return func(s *PositionKeepingService) {
		s.bucketCounter = counter
	}
}

// WithCurrentAccountClient sets an optional client for querying liens from Current Account.
// If not set or set to nil, Reserve/Available/Free balance computations will return
// a FailedPrecondition error, as these balances require lien information.
func WithCurrentAccountClient(client domain.CurrentAccountClient) Option {
	return func(s *PositionKeepingService) {
		s.currentAccountClient = client
	}
}

// WithAccountValidator sets an optional account validator for validating account existence.
// When enabled via WithAccountValidationEnabled, the service will validate that accounts
// exist in the Current Account service before creating position logs.
// If not set or set to nil, account validation is skipped for backwards compatibility.
func WithAccountValidator(validator AccountValidator) Option {
	return func(s *PositionKeepingService) {
		s.accountValidator = validator
	}
}

// WithAccountValidationEnabled enables or disables account validation.
// When set to true and an AccountValidator is configured, the service will validate
// that accounts exist before creating position logs.
// Defaults to false for backwards compatibility.
func WithAccountValidationEnabled(enabled bool) Option {
	return func(s *PositionKeepingService) {
		s.accountValidationEnabled = enabled
	}
}

// WithReservationRepository sets the reservation repository for reservation RPCs.
// If not set, RecordReservation, ReleaseReservation, and GetProjectedBalance return FailedPrecondition.
func WithReservationRepository(repo domain.ReservationRepository) Option {
	return func(s *PositionKeepingService) {
		s.reservationRepo = repo
	}
}

// WithPositionRepository sets the position repository for projected balance queries.
// If not set, GetProjectedBalance returns FailedPrecondition.
func WithPositionRepository(repo domain.PositionRepository) Option {
	return func(s *PositionKeepingService) {
		s.positionRepo = repo
	}
}

// WithInstrumentResolver sets the InstrumentResolver for resolving instrument properties
// (dimension, precision) from Reference Data. If not set, asset instrument resolution
// uses hardcoded defaults for backwards compatibility.
func WithInstrumentResolver(resolver refdata.InstrumentResolver) Option {
	return func(s *PositionKeepingService) {
		s.instrumentResolver = resolver
	}
}

// NewPositionKeepingService creates a new PositionKeepingService with dependency injection.
//
// Dependencies:
//   - repository: Persistence layer for financial position logs (must not be nil)
//   - measurementRepo: Persistence layer for measurements (must not be nil)
//   - eventPublisher: Publishes domain events (must not be nil)
//   - idempotencySvc: Ensures exactly-once processing of idempotent operations (must not be nil)
//   - outboxPublisher: Publishes events via transactional outbox (must not be nil)
//
// Optional dependencies can be provided via Option functions:
//   - WithInstrumentCache: Enables CEL validation of measurements against instrument definitions
//
// Returns an error if any required dependency is nil.
func NewPositionKeepingService(
	repository domain.FinancialPositionLogRepository,
	measurementRepo domain.MeasurementRepository,
	eventPublisher domain.EventPublisher,
	idempotencySvc idempotency.Service,
	outboxPublisher *messaging.OutboxEventPublisher,
	opts ...Option,
) (*PositionKeepingService, error) {
	if repository == nil {
		return nil, ErrRepositoryNil
	}
	if measurementRepo == nil {
		return nil, ErrMeasurementRepoNil
	}
	if eventPublisher == nil {
		return nil, ErrEventPublisherNil
	}
	if idempotencySvc == nil {
		return nil, ErrIdempotencyServiceNil
	}
	if outboxPublisher == nil {
		return nil, ErrOutboxPublisherNil
	}

	svc := &PositionKeepingService{
		repository:      repository,
		measurementRepo: measurementRepo,
		eventPublisher:  eventPublisher,
		outboxPublisher: outboxPublisher,
		idempotency:     idempotencySvc,
	}

	// Apply optional configurations
	for _, opt := range opts {
		opt(svc)
	}

	return svc, nil
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
