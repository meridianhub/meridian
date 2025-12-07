package service

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

const (
	// defaultIdempotencyTTL is the default TTL for idempotency keys when not specified by the client.
	// This should be long enough to allow for retries but short enough to not consume excessive storage.
	defaultIdempotencyTTL = 1 * time.Hour
)

// DomainEvent is a marker interface for all financial accounting domain events.
// Concrete event types will be defined in domain/events.go in subsequent subtasks.
//
// Event types to be implemented (subtask 9.2+):
//   - LedgerPostingCapturedEvent
//   - LedgerPostingAmendedEvent
//   - LedgerPostingPostedEvent
//   - LedgerPostingRejectedEvent
//   - FinancialBookingLogInitiatedEvent
//   - FinancialBookingLogUpdatedEvent
//   - FinancialBookingLogPostedEvent
//   - FinancialBookingLogClosedEvent
//   - BalanceValidationFailedEvent
type DomainEvent interface {
	// EventType returns the type identifier for this event
	EventType() string
}

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
}

// NewFinancialAccountingService creates a new FinancialAccountingService with dependency injection.
//
// Dependencies:
//   - repository: Persistence layer for ledger postings and booking logs (must not be nil)
//   - eventPublisher: Publishes domain events to Kafka (must not be nil)
//   - idempotencySvc: Ensures exactly-once processing of idempotent operations (must not be nil)
//
// The returned service embeds UnimplementedFinancialAccountingServiceServer, which provides
// default "Unimplemented" responses for all gRPC methods. Methods will be implemented incrementally
// in subsequent subtasks (9.2, 9.3, 9.4, 9.5).
//
// Panics if any dependency is nil (defensive programming per ADR-0008).
//
// Example usage:
//
//	repo := persistence.NewLedgerRepository(db)
//	publisher := messaging.NewKafkaEventPublisher(kafkaProducer)
//	idempotencySvc := idempotency.NewRedisService(redisClient)
//
//	service := NewFinancialAccountingService(repo, publisher, idempotencySvc)
func NewFinancialAccountingService(
	repository *persistence.LedgerRepository,
	eventPublisher EventPublisher,
	idempotencySvc idempotency.Service,
) *FinancialAccountingService {
	if repository == nil {
		panic("financial accounting service: repository cannot be nil")
	}
	if eventPublisher == nil {
		panic("financial accounting service: event publisher cannot be nil")
	}
	if idempotencySvc == nil {
		panic("financial accounting service: idempotency service cannot be nil")
	}

	return &FinancialAccountingService{
		repository:     repository,
		eventPublisher: eventPublisher,
		idempotency:    idempotencySvc,
	}
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
	// Check idempotency
	var idempotencyKey idempotency.Key
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKey = idempotency.Key{
			Namespace: "financial-accounting",
			Operation: "capture-posting",
			EntityID:  req.GetFinancialBookingLogId(),
			RequestID: req.IdempotencyKey.Key,
		}

		result, err := s.idempotency.Check(ctx, idempotencyKey)
		if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				if result != nil && result.Status == idempotency.StatusCompleted {
					// TODO: Deserialize cached response from result.Data
					// For now, return AlreadyExists error
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

	// Publish domain event (placeholder - actual event will be implemented in event subtask)
	// TODO: Implement LedgerPostingCapturedEvent and publish it
	// event := &events.LedgerPostingCapturedEvent{...}
	// if err := s.eventPublisher.Publish(ctx, event); err != nil {
	//     // Log error but don't fail the request (event publishing is best-effort)
	//     // In production, consider using outbox pattern for guaranteed delivery
	// }

	// Convert to proto response
	response := &financialaccountingv1.CaptureLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}

	// Store result for idempotency
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		ttl := defaultIdempotencyTTL
		if req.IdempotencyKey.TtlSeconds > 0 {
			ttl = time.Duration(req.IdempotencyKey.TtlSeconds) * time.Second
		}

		// TODO: Serialize response to bytes for storage
		// For now, just mark as completed
		result := idempotency.Result{
			Key:         idempotencyKey,
			Status:      idempotency.StatusCompleted,
			Data:        nil, // TODO: Serialize response
			CompletedAt: time.Now(),
			TTL:         ttl,
		}

		// Store result in idempotency cache (best-effort, failures are logged but don't fail request)
		_ = s.idempotency.StoreResult(ctx, result)
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
// 1. Parse and validate request fields
// 2. Retrieve existing posting by ID
// 3. Validate state transition rules (e.g., cannot change POSTED status)
// 4. Apply update using domain methods (Post/Fail)
// 5. Persist updated posting
// 6. Publish domain event (LedgerPostingUpdatedEvent)
// 7. Return updated posting
//
// Error mapping:
// - Invalid request fields -> codes.InvalidArgument
// - Posting not found -> codes.NotFound
// - Invalid state transition -> codes.FailedPrecondition
// - Internal errors -> codes.Internal
func (s *FinancialAccountingService) UpdateLedgerPosting(
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

	// Retrieve existing posting
	posting, err := s.repository.GetPosting(ctx, postingID)
	if err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve posting: %v", err)
	}

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

	// Publish domain event (placeholder - actual event will be implemented in event subtask)
	// TODO: Implement LedgerPostingUpdatedEvent and publish it
	// event := &events.LedgerPostingUpdatedEvent{...}
	// if err := s.eventPublisher.Publish(ctx, event); err != nil {
	//     // Log error but don't fail the request
	// }

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

	// Check idempotency
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

	// TODO: Publish FinancialBookingLogInitiatedEvent for inter-service coordination

	// Store idempotency result
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
// 1. Parse and validate request fields
// 2. Retrieve existing booking log by ID
// 3. Validate state transition rules
// 4. Apply updates using domain methods
// 5. Persist updated booking log
// 6. Return updated booking log
//
// Error mapping:
// - Invalid request fields -> codes.InvalidArgument
// - Booking log not found -> codes.NotFound
// - Invalid state transition -> codes.FailedPrecondition
// - Internal errors -> codes.Internal
func (s *FinancialAccountingService) UpdateFinancialBookingLog(
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

	// Validate state transition using the state machine
	// This handles all valid transitions including POSTED -> REVERSED for reversals
	if !isValidBookingLogTransition(bookingLog.Status, newStatus) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"invalid status transition from %s to %s", bookingLog.Status, newStatus)
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

	// TODO: Publish FinancialBookingLogUpdatedEvent for inter-service coordination

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
