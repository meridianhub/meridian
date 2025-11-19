package service

import (
	"context"
	"errors"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/internal/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/pkg/platform/idempotency"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
		return nil, status.Errorf(codes.Internal, "failed to retrieve ledger posting: %v", err)
	}

	// Convert to protobuf and return
	return &financialaccountingv1.RetrieveLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}, nil
}

// Method implementations will be added in subsequent subtasks:
//
// Subtask 9.2 - gRPC method implementations with full workflow:
//   - InitiateFinancialBookingLog: Creates new booking log with idempotency
//   - UpdateFinancialBookingLog: Updates booking log status and rules
//   - RetrieveFinancialBookingLog: Retrieves booking log by ID
//   - CaptureLedgerPosting: Creates posting with validation and events
//
// Subtask 9.5 - List operations:
//   - ListFinancialBookingLogs: Lists booking logs with filtering/pagination
//
// Until implemented, the embedded UnimplementedFinancialAccountingServiceServer
// will return codes.Unimplemented for all RPC calls.
