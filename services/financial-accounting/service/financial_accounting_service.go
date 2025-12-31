package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/services/financial-accounting/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/events"
)

const (
	// defaultIdempotencyTTL is the default TTL for idempotency keys when not specified by the client.
	// This should be long enough to allow for retries but short enough to not consume excessive storage.
	defaultIdempotencyTTL = 1 * time.Hour
)

// Service initialization errors
var (
	// ErrRepositoryNil is returned when attempting to create a service with a nil repository
	ErrRepositoryNil = errors.New("financial accounting service: repository cannot be nil")
	// ErrEventPublisherNil is returned when attempting to create a service with a nil event publisher
	ErrEventPublisherNil = errors.New("financial accounting service: event publisher cannot be nil")
	// ErrIdempotencyServiceNil is returned when attempting to create a service with a nil idempotency service
	ErrIdempotencyServiceNil = errors.New("financial accounting service: idempotency service cannot be nil")
	// ErrOutboxPublisherNil is returned when attempting to create a service with a nil outbox publisher
	ErrOutboxPublisherNil = errors.New("financial accounting service: outbox publisher cannot be nil")
	// ErrOutboxRepositoryNil is returned when attempting to create a service with a nil outbox repository
	ErrOutboxRepositoryNil = errors.New("financial accounting service: outbox repository cannot be nil")
)

// DomainEvent is a marker interface for all financial accounting domain events.
// In practice, events are protobuf messages from eventsv1 package.
// The interface uses proto.Message for maximum compatibility with protobuf events.
//
// Event types (protobuf-based):
//   - eventsv1.LedgerPostingCapturedEvent
//   - eventsv1.LedgerPostingAmendedEvent
//   - eventsv1.LedgerPostingPostedEvent
//   - eventsv1.LedgerPostingRejectedEvent
//   - eventsv1.FinancialBookingLogInitiatedEvent
//   - eventsv1.FinancialBookingLogUpdatedEvent
//   - eventsv1.FinancialBookingLogPostedEvent
//   - eventsv1.FinancialBookingLogClosedEvent
//   - eventsv1.BalanceValidationFailedEvent
type DomainEvent = proto.Message

// EventPublisher defines the interface for publishing domain events to the messaging infrastructure.
// Events are published to Kafka following ADR-0004 (Event Schema Evolution Strategy).
//
// Implementation will be provided by adapters/messaging package following the pattern
// from position-keeping/adapters/messaging/kafka_event_publisher.go
type EventPublisher interface {
	// Publish publishes a single domain event to the appropriate Kafka topic.
	// The topic is determined based on the event type (one topic per event type per ADR-0004).
	// Returns an error if publishing fails.
	Publish(ctx context.Context, event DomainEvent) error

	// PublishBatch publishes multiple domain events as a batch for efficiency.
	// All events should succeed or fail together (transactional semantics where possible).
	// Returns an error if any event in the batch fails to publish.
	PublishBatch(ctx context.Context, events []DomainEvent) error
}

// FinancialAccountingService implements the gRPC service for Financial Accounting operations.
//
// This service follows the BIAN (Banking Industry Architecture Network) Financial Accounting
// service domain specification, providing operations for:
// - Financial Booking Log lifecycle management (Initiate, Update, Retrieve, List)
// - Ledger Posting operations (Capture, Retrieve)
// - Double-entry bookkeeping validation
//
// Architecture patterns:
// - ADR-0002: One microservice per BIAN domain
// - ADR-0004: Event schema evolution with buf tooling
// - ADR-0005: Adapter pattern for layer translation
// - Constructor injection for dependencies
// - Idempotency for exactly-once processing
type FinancialAccountingService struct {
	financialaccountingv1.UnimplementedFinancialAccountingServiceServer

	// repository provides persistence operations for ledger postings and booking logs
	repository *persistence.LedgerRepository

	// eventPublisher publishes domain events to Kafka for inter-service coordination
	eventPublisher EventPublisher

	// idempotency ensures exactly-once processing of requests with idempotency keys
	idempotency idempotency.Service

	// idempotencyExecutor wraps business logic with atomic idempotency handling.
	// This ensures that PENDING state is cleaned up if the operation fails,
	// eliminating the gap between MarkPending and StoreResult.
	idempotencyExecutor *idempotency.Executor

	// outboxPublisher publishes events through the transactional outbox pattern
	// for guaranteed at-least-once delivery of audit-critical control operation events
	outboxPublisher *events.OutboxPublisher

	// outboxRepo provides persistence operations for the event outbox table
	outboxRepo *events.PostgresOutboxRepository
}

// NewFinancialAccountingService creates a new FinancialAccountingService with dependency injection.
//
// Dependencies:
//   - repository: Persistence layer for ledger postings and booking logs (must not be nil)
//   - eventPublisher: Publishes domain events to Kafka (must not be nil)
//   - idempotencySvc: Ensures exactly-once processing of idempotent operations (must not be nil)
//   - outboxPublisher: Publishes events through transactional outbox (must not be nil)
//   - outboxRepo: Persistence for event outbox entries (must not be nil)
//
// The returned service embeds UnimplementedFinancialAccountingServiceServer, which provides
// default "Unimplemented" responses for all gRPC methods. Methods will be implemented incrementally
// in subsequent subtasks (9.2, 9.3, 9.4, 9.5).
//
// Returns an error if any dependency is nil.
//
// Example usage:
//
//	repo := persistence.NewLedgerRepository(db)
//	publisher := messaging.NewKafkaEventPublisher(kafkaProducer)
//	idempotencySvc := idempotency.NewRedisService(redisClient)
//	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
//	outboxRepo := events.NewPostgresOutboxRepository(db)
//
//	service, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
//	if err != nil {
//	    return fmt.Errorf("failed to create financial accounting service: %w", err)
//	}
func NewFinancialAccountingService(
	repository *persistence.LedgerRepository,
	eventPublisher EventPublisher,
	idempotencySvc idempotency.Service,
	outboxPublisher *events.OutboxPublisher,
	outboxRepo *events.PostgresOutboxRepository,
) (*FinancialAccountingService, error) {
	if repository == nil {
		return nil, ErrRepositoryNil
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
	if outboxRepo == nil {
		return nil, ErrOutboxRepositoryNil
	}

	// Create the idempotency executor with default configuration
	executor := idempotency.NewExecutor(idempotencySvc, nil)

	return &FinancialAccountingService{
		repository:          repository,
		eventPublisher:      eventPublisher,
		idempotency:         idempotencySvc,
		idempotencyExecutor: executor,
		outboxPublisher:     outboxPublisher,
		outboxRepo:          outboxRepo,
	}, nil
}

// CaptureLedgerPosting creates a new ledger posting with validation and event publishing.
//
// Workflow:
// 1. Check idempotency using request's IdempotencyKey
// 2. Validate that the financial booking log exists
// 3. Parse and validate all request fields
// 4. Create domain entity with business logic validation
// 5. Persist posting in transaction
// 6. Publish domain event (LedgerPostingCapturedEvent)
// 7. Return gRPC response with created posting
//
// Error mapping:
// - Invalid request fields -> codes.InvalidArgument
// - Duplicate idempotency key -> codes.AlreadyExists
// - Booking log not found -> codes.NotFound
// - Internal errors -> codes.Internal
func (s *FinancialAccountingService) CaptureLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.CaptureLedgerPostingRequest,
) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	// Check idempotency (only if service is configured and key is provided)
	var idempotencyKey idempotency.Key
	if s.idempotency != nil && req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKey = idempotency.Key{
			Namespace: "financial-accounting",
			Operation: "capture-posting",
			EntityID:  req.GetFinancialBookingLogId(),
			RequestID: req.IdempotencyKey.Key,
		}

		result, err := s.idempotency.Check(ctx, idempotencyKey)
		if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				if result != nil && result.Status == idempotency.StatusCompleted && len(result.Data) > 0 {
					// Deserialize cached response from protobuf
					var cachedResponse financialaccountingv1.CaptureLedgerPostingResponse
					if unmarshalErr := proto.Unmarshal(result.Data, &cachedResponse); unmarshalErr != nil {
						// Log deserialization error but fall back to generic AlreadyExists response
						slog.Error("failed to deserialize cached idempotency response",
							"error", unmarshalErr,
							"idempotency_key", req.IdempotencyKey.Key,
							"operation", "capture-posting")
						return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
					}
					// Return cached response for idempotent behavior
					return &cachedResponse, nil
				}
				// No cached data available - return generic AlreadyExists
				return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
			}
			return nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
		}

		// Mark as pending to prevent concurrent processing
		if err := s.idempotency.MarkPending(ctx, idempotencyKey, defaultIdempotencyTTL); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
		}
	}

	// Parse booking log ID
	bookingLogID, err := parseUUID(req.GetFinancialBookingLogId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid financial_booking_log_id: %v", err)
	}

	// Validate booking log exists (optional check - could be deferred to database constraint)
	// For now we'll trust the database foreign key constraint

	// Parse and validate posting amount
	postingAmount, err := fromProtoMoney(req.GetPostingAmount())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid posting_amount: %v", err)
	}

	// Validate posting direction
	if req.PostingDirection == commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "posting_direction must be specified")
	}
	direction := fromProtoPostingDirection(req.PostingDirection)

	// Validate account ID
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	// Validate value date
	if req.ValueDate == nil {
		return nil, status.Error(codes.InvalidArgument, "value_date is required")
	}
	valueDate := req.ValueDate.AsTime()

	// Extract correlation ID from idempotency key (or use empty string)
	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}

	// Create domain entity with validation
	posting, err := domain.NewLedgerPosting(
		bookingLogID,
		direction,
		postingAmount,
		req.AccountId,
		valueDate,
		correlationID,
	)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid posting data: %v", err)
	}

	// Persist posting
	if err := s.repository.SavePosting(ctx, posting); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save posting: %v", err)
	}

	// Publish LedgerPostingCapturedEvent for inter-service coordination
	// Event publishing is best-effort - errors are logged but don't fail the operation
	event := &eventsv1.LedgerPostingCapturedEvent{
		PostingId:        posting.ID.String(),
		BookingLogId:     posting.FinancialBookingLogID.String(),
		PostingDirection: toProtoPostingDirection(posting.Direction),
		PostingAmount:    toProtoMoney(posting.Amount),
		AccountId:        posting.AccountID,
		ValueDate:        timestamppb.New(posting.ValueDate),
		Status:           toProtoTransactionStatus(posting.Status),
		CorrelationId:    correlationID,
		CausationId:      correlationID, // Request caused this event
		Timestamp:        timestamppb.Now(),
		Version:          1, // Initial version for newly created posting
	}
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish LedgerPostingCapturedEvent",
			"error", err,
			"posting_id", posting.ID.String(),
			"booking_log_id", posting.FinancialBookingLogID.String())
	}

	// Convert to proto response
	response := &financialaccountingv1.CaptureLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}

	// Store result for idempotency (only if service configured and key provided)
	if s.idempotency != nil && req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		ttl := defaultIdempotencyTTL
		if req.IdempotencyKey.TtlSeconds > 0 {
			ttl = time.Duration(req.IdempotencyKey.TtlSeconds) * time.Second
		}

		// Serialize response using protobuf for idempotent storage
		responseData, marshalErr := proto.Marshal(response)
		if marshalErr != nil {
			// Log serialization error but don't fail the operation - response was successful
			slog.Error("failed to serialize response for idempotency cache",
				"error", marshalErr,
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "capture-posting")
		} else {
			result := idempotency.Result{
				Key:         idempotencyKey,
				Status:      idempotency.StatusCompleted,
				Data:        responseData,
				CompletedAt: time.Now(),
				TTL:         ttl,
			}

			// Store result in idempotency cache (best-effort, failures are logged but don't fail request)
			if storeErr := s.idempotency.StoreResult(ctx, result); storeErr != nil {
				slog.Error("failed to store idempotency result",
					"error", storeErr,
					"idempotency_key", req.IdempotencyKey.Key,
					"operation", "capture-posting")
			}
		}
	}

	return response, nil
}

// RetrieveLedgerPosting retrieves a specific ledger posting by ID.
//
// This method implements subtask 9.3 - simple retrieve operation.
//
// gRPC Error Codes:
//   - codes.InvalidArgument: Invalid posting ID format
//   - codes.NotFound: Posting does not exist
//   - codes.Internal: Database or system errors
//
// Example:
//
//	req := &financialaccountingv1.RetrieveLedgerPostingRequest{
//	    Id: "550e8400-e29b-41d4-a716-446655440000",
//	}
//	resp, err := service.RetrieveLedgerPosting(ctx, req)
func (s *FinancialAccountingService) RetrieveLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.RetrieveLedgerPostingRequest,
) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	// Parse and validate posting ID
	postingID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid posting id: %v", err)
	}

	// Retrieve from repository
	posting, err := s.repository.GetPosting(ctx, postingID)
	if err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		// Don't expose internal errors to clients (security best practice)
		return nil, status.Error(codes.Internal, "failed to retrieve ledger posting")
	}

	// Convert to protobuf and return
	return &financialaccountingv1.RetrieveLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}, nil
}

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

	// Apply account ID filter if provided
	if req.AccountId != "" {
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

	// Apply currency filter if provided
	if req.Currency != "" {
		// Validate currency code format (must be 3 uppercase letters per ISO 4217)
		if !isValidCurrencyCode(req.Currency) {
			return nil, status.Errorf(codes.InvalidArgument, "invalid currency code: %s (must be 3 uppercase letters)", req.Currency)
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

// UpdateLedgerPosting updates an existing ledger posting's status and result.
//
// Workflow:
// 1. Check idempotency using request's IdempotencyKey
// 2. Parse and validate request fields
// 3. Retrieve existing posting by ID
// 4. Validate state transition rules (e.g., cannot change POSTED status)
// 5. Apply update using domain methods (Post/Fail)
// 6. Persist updated posting
// 7. Publish domain event (LedgerPostingUpdatedEvent)
// 8. Return updated posting
//
// Idempotency Note:
// Unlike CaptureLedgerPosting where idempotency is optional (create operations
// naturally fail on duplicate IDs), update operations REQUIRE idempotency keys
// because state-machine transitions must be exactly-once. A duplicate update
// could incorrectly transition an entity through multiple states.
//
// Error mapping:
// - Invalid request fields -> codes.InvalidArgument
// - Duplicate idempotency key -> codes.AlreadyExists
// - Posting not found -> codes.NotFound
// - Invalid state transition -> codes.FailedPrecondition
// - Internal errors -> codes.Internal
func (s *FinancialAccountingService) UpdateLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.UpdateLedgerPostingRequest,
) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
	// Validate idempotency key is provided
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	idempotencyKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "update-posting",
		EntityID:  req.GetId(),
		RequestID: req.IdempotencyKey.Key,
	}

	// Determine TTL for idempotency key
	ttl := defaultIdempotencyTTL
	if req.IdempotencyKey.TtlSeconds > 0 {
		ttl = time.Duration(req.IdempotencyKey.TtlSeconds) * time.Second
	}

	// Use idempotency executor to wrap business logic with atomic PENDING cleanup.
	// This ensures orphaned PENDING keys are cleaned up if the operation fails.
	var response *financialaccountingv1.UpdateLedgerPostingResponse

	execResult, err := s.idempotencyExecutor.Execute(ctx, idempotencyKey, ttl, func(ctx context.Context) ([]byte, error) {
		// Execute business logic
		resp, execErr := s.executeUpdateLedgerPosting(ctx, req)
		if execErr != nil {
			return nil, execErr
		}

		// Serialize response for idempotency cache
		responseData, marshalErr := proto.Marshal(resp)
		if marshalErr != nil {
			slog.Error("failed to serialize response for idempotency cache",
				"error", marshalErr,
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "update-posting")
			// Still return success - the operation completed, just caching failed
			responseData = nil
		}

		response = resp
		return responseData, nil
	})
	if err != nil {
		// Handle specific idempotency errors
		if errors.Is(err, idempotency.ErrOperationInProgress) {
			return nil, status.Error(codes.Aborted, "operation already in progress")
		}
		// ExecutorErrors wrap idempotency layer errors - return as Internal
		var execErr *idempotency.ExecutorError
		if errors.As(err, &execErr) {
			return nil, status.Errorf(codes.Internal, "idempotency error: %v", err)
		}
		// Business logic errors from the fn() callback pass through directly
		// These are already gRPC status errors, so return as-is
		return nil, err
	}

	// Handle cached result
	if execResult.FromCache {
		if len(execResult.Data) > 0 {
			var cachedResponse financialaccountingv1.UpdateLedgerPostingResponse
			if unmarshalErr := proto.Unmarshal(execResult.Data, &cachedResponse); unmarshalErr != nil {
				slog.Error("failed to deserialize cached idempotency response",
					"error", unmarshalErr,
					"idempotency_key", req.IdempotencyKey.Key,
					"operation", "update-posting")
				return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
			}
			slog.Info("returning cached idempotent response",
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "update-posting",
				"posting_id", req.GetId())
			return &cachedResponse, nil
		}
		return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
	}

	return response, nil
}

// executeUpdateLedgerPosting contains the core business logic for UpdateLedgerPosting.
// This is separated from the main method to allow the idempotency executor to wrap it.
func (s *FinancialAccountingService) executeUpdateLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.UpdateLedgerPostingRequest,
) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
	// Parse posting ID
	postingID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	// Validate status
	if req.Status == commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "status must be specified")
	}
	newStatus := fromProtoTransactionStatus(req.Status)

	// Extract correlation ID from idempotency key
	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}

	// Retrieve existing posting
	posting, err := s.repository.GetPosting(ctx, postingID)
	if err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve posting: %v", err)
	}

	// Capture previous state BEFORE any modifications (for LedgerPostingAmendedEvent)
	previousAmount := posting.Amount
	previousStatus := posting.Status

	// Validate and apply state transition using domain methods
	postingResult := req.PostingResult
	if postingResult == "" {
		postingResult = posting.PostingResult // Preserve existing if not provided
	}

	switch newStatus {
	case domain.TransactionStatusPosted:
		if err := posting.Post(postingResult); err != nil {
			if errors.Is(err, domain.ErrAlreadyPosted) {
				return nil, status.Error(codes.FailedPrecondition, "posting already posted")
			}
			return nil, status.Errorf(codes.InvalidArgument, "cannot post: %v", err)
		}
	case domain.TransactionStatusFailed:
		if err := posting.Fail(postingResult); err != nil {
			if errors.Is(err, domain.ErrCannotFailPosted) {
				return nil, status.Error(codes.FailedPrecondition, "cannot fail a posted transaction")
			}
			return nil, status.Errorf(codes.InvalidArgument, "cannot fail: %v", err)
		}
	case domain.TransactionStatusPending:
		// Allow transition back to pending (for retry scenarios)
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	case domain.TransactionStatusCancelled:
		// Allow cancellation
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	case domain.TransactionStatusReversed:
		// Allow reversal
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported status: %v", newStatus)
	}

	// Persist updated posting
	if err := s.repository.UpdatePosting(ctx, posting); err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		return nil, status.Errorf(codes.Internal, "failed to update posting: %v", err)
	}

	// Publish LedgerPostingAmendedEvent for inter-service coordination
	// Event publishing is best-effort - errors are logged but don't fail the operation
	// Note: UpdateLedgerPosting changes status, not amount. Both previous_amount and new_amount
	// will be the same value since amount doesn't change in status transitions.
	event := &eventsv1.LedgerPostingAmendedEvent{
		PostingId:      posting.ID.String(),
		BookingLogId:   posting.FinancialBookingLogID.String(),
		PreviousAmount: toProtoMoney(previousAmount),
		NewAmount:      toProtoMoney(posting.Amount),
		Reason:         fmt.Sprintf("Status updated from %v to %v", previousStatus, newStatus),
		AmendedBy:      "system", // Status transitions are system-driven
		CorrelationId:  correlationID,
		CausationId:    correlationID, // Request caused this event
		Timestamp:      timestamppb.Now(),
		Version:        1, // Increment version for optimistic locking
	}
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish LedgerPostingAmendedEvent",
			"error", err,
			"posting_id", posting.ID.String(),
			"booking_log_id", posting.FinancialBookingLogID.String(),
			"status", newStatus)
	}

	// Convert to proto response
	return &financialaccountingv1.UpdateLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}, nil
}

// isValidCurrencyCode validates that a currency code matches ISO 4217 format.
// Valid codes are exactly 3 uppercase letters (e.g., USD, GBP, EUR).
func isValidCurrencyCode(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// InitiateFinancialBookingLog creates a new financial booking log.
//
// Workflow:
// 1. Check idempotency using request's IdempotencyKey
// 2. Validate all request fields
// 3. Create domain entity
// 4. Persist booking log
// 5. Return gRPC response with created booking log
//
// Error mapping:
// - Invalid request fields -> codes.InvalidArgument
// - Duplicate idempotency key -> codes.AlreadyExists
// - Internal errors -> codes.Internal
func (s *FinancialAccountingService) InitiateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.InitiateFinancialBookingLogRequest,
) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	// Validate idempotency key is provided
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	idempotencyKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "initiate-booking-log",
		EntityID:  req.IdempotencyKey.Key,
		RequestID: req.IdempotencyKey.Key,
	}

	// Check idempotency (skip if service not configured - e.g., Redis unavailable in dev)
	if s.idempotency != nil {
		result, err := s.idempotency.Check(ctx, idempotencyKey)
		if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				if result != nil && result.Status == idempotency.StatusCompleted {
					return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
				}
			}
			return nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
		}

		// Mark as pending to prevent concurrent processing
		if err := s.idempotency.MarkPending(ctx, idempotencyKey, defaultIdempotencyTTL); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
		}
	}

	// Validate account type
	if req.FinancialAccountType == commonv1.AccountType_ACCOUNT_TYPE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "financial_account_type must be specified")
	}
	accountType := fromProtoAccountType(req.FinancialAccountType)
	if accountType == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid financial_account_type")
	}

	// Validate product service reference
	if req.ProductServiceReference == "" {
		return nil, status.Error(codes.InvalidArgument, "product_service_reference is required")
	}

	// Validate business unit reference
	if req.BusinessUnitReference == "" {
		return nil, status.Error(codes.InvalidArgument, "business_unit_reference is required")
	}

	// Validate chart of accounts rules
	if req.ChartOfAccountsRules == "" {
		return nil, status.Error(codes.InvalidArgument, "chart_of_accounts_rules is required")
	}

	// Validate base currency
	if req.BaseCurrency == commonv1.Currency_CURRENCY_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "base_currency must be specified")
	}
	baseCurrency := fromProtoCurrency(req.BaseCurrency)
	if baseCurrency == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid base_currency")
	}

	// Create domain entity
	bookingLog := domain.NewFinancialBookingLog(
		accountType,
		req.ProductServiceReference,
		req.BusinessUnitReference,
		req.ChartOfAccountsRules,
		baseCurrency,
	)

	// Persist booking log
	if err := s.repository.SaveBookingLog(ctx, bookingLog, req.IdempotencyKey.Key); err != nil {
		if errors.Is(err, persistence.ErrDuplicateIdempotencyKey) {
			return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
		}
		return nil, status.Errorf(codes.Internal, "failed to save booking log: %v", err)
	}

	// Publish FinancialBookingLogInitiatedEvent for inter-service coordination
	// Event publishing is best-effort - errors are logged but don't fail the operation
	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}
	event := &eventsv1.FinancialBookingLogInitiatedEvent{
		BookingLogId:            bookingLog.ID.String(),
		FinancialAccountType:    toProtoAccountType(bookingLog.FinancialAccountType),
		ProductServiceReference: bookingLog.ProductServiceReference,
		BusinessUnitReference:   bookingLog.BusinessUnitReference,
		BaseCurrency:            toProtoCurrency(bookingLog.BaseCurrency),
		CorrelationId:           correlationID,
		CausationId:             correlationID, // Request caused this event
		Timestamp:               timestamppb.Now(),
		Version:                 1, // Initial version for newly created booking log
	}
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish FinancialBookingLogInitiatedEvent",
			"error", err,
			"booking_log_id", bookingLog.ID.String())
	}

	// Store idempotency result (only if service configured)
	if s.idempotency != nil {
		ttl := defaultIdempotencyTTL
		if req.IdempotencyKey.TtlSeconds > 0 {
			ttl = time.Duration(req.IdempotencyKey.TtlSeconds) * time.Second
		}
		idempResult := idempotency.Result{
			Key:         idempotencyKey,
			Status:      idempotency.StatusCompleted,
			Data:        nil,
			CompletedAt: time.Now(),
			TTL:         ttl,
		}
		_ = s.idempotency.StoreResult(ctx, idempResult)
	}

	// Convert to proto response
	return &financialaccountingv1.InitiateFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(bookingLog),
	}, nil
}

// RetrieveFinancialBookingLog retrieves a specific booking log by ID.
//
// gRPC Error Codes:
//   - codes.InvalidArgument: Invalid booking log ID format
//   - codes.NotFound: Booking log does not exist
//   - codes.Internal: Database or system errors
func (s *FinancialAccountingService) RetrieveFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.RetrieveFinancialBookingLogRequest,
) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
	// Parse and validate booking log ID
	bookingLogID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid booking log id: %v", err)
	}

	// Retrieve from repository
	bookingLog, err := s.repository.GetBookingLog(ctx, bookingLogID)
	if err != nil {
		if errors.Is(err, persistence.ErrBookingLogNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
		}
		return nil, status.Error(codes.Internal, "failed to retrieve booking log")
	}

	// Convert to protobuf and return
	return &financialaccountingv1.RetrieveFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(bookingLog),
	}, nil
}

// UpdateFinancialBookingLog updates an existing booking log's status and rules.
//
// Workflow:
// 1. Check idempotency using request's IdempotencyKey
// 2. Parse and validate request fields
// 3. Retrieve existing booking log by ID
// 4. Validate state transition rules
// 5. Apply updates using domain methods
// 6. Persist updated booking log
// 7. Return updated booking log
//
// Idempotency Note:
// Unlike InitiateFinancialBookingLog where idempotency is optional (create operations
// naturally fail on duplicate IDs), update operations REQUIRE idempotency keys
// because state-machine transitions must be exactly-once. A duplicate update
// could incorrectly transition an entity through multiple states.
//
// Error mapping:
// - Invalid request fields -> codes.InvalidArgument
// - Duplicate idempotency key -> codes.AlreadyExists
// - Booking log not found -> codes.NotFound
// - Invalid state transition -> codes.FailedPrecondition
// - Internal errors -> codes.Internal
func (s *FinancialAccountingService) UpdateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.UpdateFinancialBookingLogRequest,
) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	// Validate idempotency key is provided
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	idempotencyKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "update-booking-log",
		EntityID:  req.GetId(),
		RequestID: req.IdempotencyKey.Key,
	}

	// Determine TTL for idempotency key
	ttl := defaultIdempotencyTTL
	if req.IdempotencyKey.TtlSeconds > 0 {
		ttl = time.Duration(req.IdempotencyKey.TtlSeconds) * time.Second
	}

	// Use idempotency executor to wrap business logic with atomic PENDING cleanup.
	// This ensures orphaned PENDING keys are cleaned up if the operation fails.
	var response *financialaccountingv1.UpdateFinancialBookingLogResponse

	execResult, err := s.idempotencyExecutor.Execute(ctx, idempotencyKey, ttl, func(ctx context.Context) ([]byte, error) {
		// Execute business logic
		resp, execErr := s.executeUpdateFinancialBookingLog(ctx, req)
		if execErr != nil {
			return nil, execErr
		}

		// Serialize response for idempotency cache
		responseData, marshalErr := proto.Marshal(resp)
		if marshalErr != nil {
			slog.Error("failed to serialize response for idempotency cache",
				"error", marshalErr,
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "update-booking-log")
			// Still return success - the operation completed, just caching failed
			responseData = nil
		}

		response = resp
		return responseData, nil
	})
	if err != nil {
		// Handle specific idempotency errors
		if errors.Is(err, idempotency.ErrOperationInProgress) {
			return nil, status.Error(codes.Aborted, "operation already in progress")
		}
		// ExecutorErrors wrap idempotency layer errors - return as Internal
		var execErr *idempotency.ExecutorError
		if errors.As(err, &execErr) {
			return nil, status.Errorf(codes.Internal, "idempotency error: %v", err)
		}
		// Business logic errors from the fn() callback pass through directly
		// These are already gRPC status errors, so return as-is
		return nil, err
	}

	// Handle cached result
	if execResult.FromCache {
		if len(execResult.Data) > 0 {
			var cachedResponse financialaccountingv1.UpdateFinancialBookingLogResponse
			if unmarshalErr := proto.Unmarshal(execResult.Data, &cachedResponse); unmarshalErr != nil {
				slog.Error("failed to deserialize cached idempotency response",
					"error", unmarshalErr,
					"idempotency_key", req.IdempotencyKey.Key,
					"operation", "update-booking-log")
				return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
			}
			slog.Info("returning cached idempotent response",
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "update-booking-log",
				"booking_log_id", req.GetId())
			return &cachedResponse, nil
		}
		return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
	}

	return response, nil
}

// executeUpdateFinancialBookingLog contains the core business logic for UpdateFinancialBookingLog.
// This is separated from the main method to allow the idempotency executor to wrap it.
func (s *FinancialAccountingService) executeUpdateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.UpdateFinancialBookingLogRequest,
) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	// Parse booking log ID
	bookingLogID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	// Validate status
	if req.Status == commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "status must be specified")
	}
	newStatus := fromProtoTransactionStatus(req.Status)

	// Retrieve existing booking log
	bookingLog, err := s.repository.GetBookingLog(ctx, bookingLogID)
	if err != nil {
		if errors.Is(err, persistence.ErrBookingLogNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve booking log: %v", err)
	}

	// Capture previous status BEFORE update for event publishing
	previousStatus := bookingLog.Status

	// Validate state transition using the state machine
	// This handles all valid transitions including POSTED -> REVERSED for reversals
	if !isValidBookingLogTransition(bookingLog.Status, newStatus) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"invalid status transition from %s to %s", bookingLog.Status, newStatus)
	}

	// Enforce double-entry bookkeeping constraint when transitioning to POSTED
	if newStatus == domain.TransactionStatusPosted {
		validationStart := time.Now()
		postings, err := s.repository.GetPostingsByBookingLogID(ctx, bookingLogID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to retrieve postings for balance validation: %v", err)
		}

		// Empty postings are not allowed for POSTED status
		if len(postings) == 0 {
			observability.RecordBalanceValidationDuration(time.Since(validationStart))
			observability.RecordDoubleEntryValidation(observability.ValidationResultUnbalanced, observability.CurrencyUnknown)
			observability.LogBalanceValidationFailure(
				bookingLogID.String(),
				observability.CurrencyUnknown,
				"0",
				"0",
				"0",
			)
			return nil, status.Error(codes.FailedPrecondition,
				"cannot post booking log with no postings")
		}

		// Calculate debit and credit totals
		debitTotal := decimal.Zero
		creditTotal := decimal.Zero
		var currency string
		for _, posting := range postings {
			// Capture currency from first posting
			if currency == "" {
				currency = posting.Amount.CurrencyCode()
			}
			switch posting.Direction {
			case domain.PostingDirectionDebit:
				debitTotal = debitTotal.Add(posting.Amount.Amount())
			case domain.PostingDirectionCredit:
				creditTotal = creditTotal.Add(posting.Amount.Amount())
			}
		}

		observability.RecordBalanceValidationDuration(time.Since(validationStart))

		// Validate double-entry balance
		if !debitTotal.Equal(creditTotal) {
			imbalance := debitTotal.Sub(creditTotal)
			observability.RecordDoubleEntryValidation(observability.ValidationResultUnbalanced, currency)
			observability.LogBalanceValidationFailure(
				bookingLogID.String(),
				currency,
				debitTotal.String(),
				creditTotal.String(),
				imbalance.String(),
			)
			return nil, status.Error(codes.FailedPrecondition,
				fmt.Sprintf("cannot post unbalanced booking log: debits=%s credits=%s imbalance=%s",
					debitTotal.String(), creditTotal.String(), imbalance.String()))
		}

		// Record successful balance validation
		observability.RecordDoubleEntryValidation(observability.ValidationResultBalanced, currency)
	}

	// Apply status update
	updated := bookingLog.WithStatus(newStatus)

	// Apply chart of accounts rules update if provided
	if req.ChartOfAccountsRules != "" {
		updated = updated.WithChartOfAccountsRules(req.ChartOfAccountsRules)
	}

	// Persist updated booking log
	if err := s.repository.UpdateBookingLog(ctx, &updated); err != nil {
		if errors.Is(err, persistence.ErrBookingLogNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
		}
		return nil, status.Errorf(codes.Internal, "failed to update booking log: %v", err)
	}

	// Publish FinancialBookingLogUpdatedEvent for inter-service coordination
	// Event publishing is best-effort - errors are logged but don't fail the operation
	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}
	event := &eventsv1.FinancialBookingLogUpdatedEvent{
		BookingLogId:         bookingLogID.String(),
		Status:               toProtoTransactionStatus(newStatus),
		PreviousStatus:       toProtoTransactionStatus(previousStatus),
		ChartOfAccountsRules: updated.ChartOfAccountsRules,
		Reason:               fmt.Sprintf("Status updated from %s to %s", previousStatus, newStatus),
		UpdatedBy:            "system", // TODO: Extract from auth context when available
		CorrelationId:        correlationID,
		CausationId:          correlationID, // Request caused this event
		Timestamp:            timestamppb.Now(),
		Version:              1, // Version tracking would need to be added to domain model
	}

	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish FinancialBookingLogUpdatedEvent",
			"error", err,
			"booking_log_id", bookingLogID.String(),
			"previous_status", previousStatus,
			"new_status", newStatus)
	}

	// Convert to proto response
	return &financialaccountingv1.UpdateFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(&updated),
	}, nil
}

// isValidBookingLogTransition validates that a status transition is allowed.
//
// Valid transitions:
//
//	From PENDING:
//	  - PENDING -> PENDING (no-op, valid but does nothing)
//	  - PENDING -> POSTED (when all postings balance and are processed)
//	  - PENDING -> FAILED (validation or processing error)
//	  - PENDING -> CANCELLED (business cancellation request)
//
//	From POSTED:
//	  - POSTED -> REVERSED (for correcting errors via reversal entries)
//
// Invalid transitions:
//   - PENDING -> REVERSED (must be POSTED first to reverse)
//   - Any transition from terminal states (FAILED, CANCELLED, REVERSED)
func isValidBookingLogTransition(from, to domain.TransactionStatus) bool {
	switch from {
	case domain.TransactionStatusPending:
		switch to {
		case domain.TransactionStatusPending, // No-op but valid
			domain.TransactionStatusPosted,
			domain.TransactionStatusFailed,
			domain.TransactionStatusCancelled:
			return true
		case domain.TransactionStatusReversed:
			// PENDING -> REVERSED is invalid (must be POSTED first)
			return false
		}
	case domain.TransactionStatusPosted:
		// Only REVERSED is valid from POSTED
		return to == domain.TransactionStatusReversed
	case domain.TransactionStatusFailed,
		domain.TransactionStatusCancelled,
		domain.TransactionStatusReversed:
		// Terminal states - no transitions allowed
		return false
	}
	return false
}

// ControlFinancialBookingLog applies a control action (SUSPEND, RESUME, TERMINATE) to a booking log.
//
// This method implements the BIAN CoCR (Control Correlation Reference) pattern for
// administrative control operations. It uses the transactional outbox pattern to ensure
// atomic persistence of both the state change and the corresponding event.
//
// Control Actions:
//   - SUSPEND: Temporarily suspends processing (PENDING -> FAILED/suspended)
//   - RESUME: Reactivates a suspended booking log (FAILED/suspended -> PENDING)
//   - TERMINATE: Permanently ends the booking log lifecycle (PENDING/FAILED -> CANCELLED)
//
// Transactional Guarantees:
//   - State change and event are written in the same database transaction
//   - Either both succeed or both are rolled back
//   - Background worker publishes events to Kafka for at-least-once delivery
//
// Idempotency:
//   - Operations are idempotent via the provided idempotency key
//   - Duplicate requests with the same key return the original result
//
// gRPC Error Codes:
//   - codes.InvalidArgument: Invalid booking log ID, control action, or missing reason
//   - codes.NotFound: Booking log does not exist
//   - codes.FailedPrecondition: Control action not allowed for current state
//   - codes.AlreadyExists: Duplicate idempotency key
//   - codes.Internal: Database or system errors
func (s *FinancialAccountingService) ControlFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.ControlFinancialBookingLogRequest,
) (*financialaccountingv1.ControlFinancialBookingLogResponse, error) {
	// Validate idempotency key is provided
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	idempotencyKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "control-booking-log",
		EntityID:  req.GetId(),
		RequestID: req.IdempotencyKey.Key,
	}

	// Check idempotency
	if s.idempotency != nil {
		result, err := s.idempotency.Check(ctx, idempotencyKey)
		if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				if result != nil && result.Status == idempotency.StatusCompleted && len(result.Data) > 0 {
					// Deserialize cached response from protobuf
					var cachedResponse financialaccountingv1.ControlFinancialBookingLogResponse
					if unmarshalErr := proto.Unmarshal(result.Data, &cachedResponse); unmarshalErr != nil {
						slog.Error("failed to deserialize cached idempotency response",
							"error", unmarshalErr,
							"idempotency_key", req.IdempotencyKey.Key,
							"operation", "control-booking-log")
						return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
					}
					slog.Info("returning cached idempotent response",
						"idempotency_key", req.IdempotencyKey.Key,
						"operation", "control-booking-log",
						"booking_log_id", req.GetId())
					return &cachedResponse, nil
				}
				return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
			}
			return nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
		}

		// Mark as pending to prevent concurrent processing
		if err := s.idempotency.MarkPending(ctx, idempotencyKey, defaultIdempotencyTTL); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
		}
	}

	// Parse booking log ID
	bookingLogID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid booking log id: %v", err)
	}

	// Validate control action
	if req.ControlAction == financialaccountingv1.ControlAction_CONTROL_ACTION_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "control_action must be specified")
	}

	// Convert proto control action to domain
	var domainAction domain.ControlAction
	switch req.ControlAction {
	case financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND:
		domainAction = domain.ControlActionSuspend
	case financialaccountingv1.ControlAction_CONTROL_ACTION_RESUME:
		domainAction = domain.ControlActionResume
	case financialaccountingv1.ControlAction_CONTROL_ACTION_TERMINATE:
		domainAction = domain.ControlActionTerminate
	case financialaccountingv1.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		// Already handled above, but included for exhaustive switch linter
		return nil, status.Error(codes.InvalidArgument, "control_action must be specified")
	}

	// Validate reason is provided
	if req.Reason == "" {
		return nil, status.Error(codes.InvalidArgument, "reason is required for control operations")
	}

	// Extract correlation ID
	correlationID := req.IdempotencyKey.Key

	// Variables to capture results from transaction
	var previousStatus domain.TransactionStatus
	var newStatus domain.TransactionStatus
	var controlledAt time.Time
	var updatedBookingLog *domain.FinancialBookingLog

	// Execute state change and event write atomically using transactional outbox pattern
	err = s.repository.WithTransaction(ctx, func(tx *gorm.DB) error {
		// 1. Retrieve and lock booking log entity within transaction (FOR UPDATE)
		var entity persistence.FinancialBookingLogEntity
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entity, "id = ?", bookingLogID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return persistence.ErrBookingLogNotFound
			}
			return err
		}

		// 2. Reconstruct domain model from locked entity
		bookingLog := &domain.FinancialBookingLog{
			ID:                      entity.ID,
			FinancialAccountType:    entity.FinancialAccountType,
			ProductServiceReference: entity.ProductServiceReference,
			BusinessUnitReference:   entity.BusinessUnitReference,
			ChartOfAccountsRules:    entity.ChartOfAccountsRules,
			BaseCurrency:            domain.Currency(entity.BaseCurrency),
			Status:                  domain.TransactionStatus(entity.Status),
			CreatedAt:               entity.CreatedAt,
			UpdatedAt:               entity.UpdatedAt,
		}

		// 3. Capture previous status for event
		previousStatus = bookingLog.Status

		// 4. Apply control action using domain method
		updated, err := bookingLog.ControlLog(domainAction, req.Reason)
		if err != nil {
			return err // Will be handled by error mapping below
		}

		// 5. Apply domain updates to locked entity
		entity.Status = string(updated.Status)
		entity.UpdatedAt = updated.UpdatedAt
		if err := tx.Save(&entity).Error; err != nil {
			return err
		}

		// 6. Capture results for response and event
		newStatus = updated.Status
		controlledAt = updated.UpdatedAt
		updatedBookingLog = &updated

		// 7. Build and write control event to outbox within the same transaction
		controlEvent := &eventsv1.FinancialBookingLogControlledEvent{
			BookingLogId:   bookingLogID.String(),
			ControlAction:  domainAction.String(),
			PreviousStatus: toProtoTransactionStatus(previousStatus),
			NewStatus:      toProtoTransactionStatus(newStatus),
			Reason:         req.Reason,
			ControlledBy:   "system", // TODO: Extract from auth context when available
			CorrelationId:  correlationID,
			CausationId:    correlationID,
			Timestamp:      timestamppb.New(controlledAt),
			Version:        1,
		}

		eventTopic := "financial-accounting.booking-log.controlled"
		if err := s.outboxPublisher.PublishControlEvent(
			ctx,
			tx,
			controlEvent,
			"financial_accounting.booking_log_controlled.v1",
			bookingLogID.String(),
			"FinancialBookingLog",
			eventTopic,
			correlationID,
		); err != nil {
			return fmt.Errorf("failed to write event to outbox: %w", err)
		}

		return nil
	})
	if err != nil {
		// Map domain errors to gRPC status codes
		switch {
		case errors.Is(err, persistence.ErrBookingLogNotFound):
			return nil, status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
		case errors.Is(err, domain.ErrInvalidControlAction):
			return nil, status.Errorf(codes.InvalidArgument, "invalid control action: %v", err)
		case errors.Is(err, domain.ErrReasonRequired):
			return nil, status.Error(codes.InvalidArgument, "reason is required for control operations")
		case errors.Is(err, domain.ErrCannotSuspendTerminal):
			return nil, status.Error(codes.FailedPrecondition, "cannot suspend booking log in terminal state")
		case errors.Is(err, domain.ErrCannotResumePending):
			return nil, status.Error(codes.FailedPrecondition, "cannot resume booking log that is not suspended")
		case errors.Is(err, domain.ErrCannotTerminateTerminal):
			return nil, status.Error(codes.FailedPrecondition, "cannot terminate booking log already in terminal state")
		default:
			return nil, status.Errorf(codes.Internal, "failed to apply control operation: %v", err)
		}
	}

	// Log success
	slog.Info("control operation applied successfully",
		"booking_log_id", bookingLogID.String(),
		"control_action", domainAction.String(),
		"previous_status", previousStatus.String(),
		"new_status", newStatus.String(),
		"reason", req.Reason)

	// Convert to proto response
	response := &financialaccountingv1.ControlFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(updatedBookingLog),
	}

	// Store idempotency result
	if s.idempotency != nil {
		ttl := defaultIdempotencyTTL
		if req.IdempotencyKey.TtlSeconds > 0 {
			ttl = time.Duration(req.IdempotencyKey.TtlSeconds) * time.Second
		}

		responseData, marshalErr := proto.Marshal(response)
		if marshalErr != nil {
			slog.Error("failed to serialize response for idempotency cache",
				"error", marshalErr,
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "control-booking-log")
		} else {
			result := idempotency.Result{
				Key:         idempotencyKey,
				Status:      idempotency.StatusCompleted,
				Data:        responseData,
				CompletedAt: time.Now(),
				TTL:         ttl,
			}

			if storeErr := s.idempotency.StoreResult(ctx, result); storeErr != nil {
				slog.Error("failed to store idempotency result",
					"error", storeErr,
					"idempotency_key", req.IdempotencyKey.Key,
					"operation", "control-booking-log")
			}
		}
	}

	return response, nil
}
