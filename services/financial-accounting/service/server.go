package service

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
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
	// ErrRegistryUnavailable is returned when the reference-data registry cannot be reached
	// and fungibility validation is required.
	ErrRegistryUnavailable = errors.New("reference-data registry unavailable")
)

// InstrumentRegistry defines the interface for looking up instrument definitions.
// This is implemented by the reference-data client for production use,
// and can be mocked for testing.
type InstrumentRegistry interface {
	// GetInstrument retrieves an instrument definition with pre-compiled CEL programs.
	// Returns an error if the instrument is not found or the registry is unavailable.
	GetInstrument(ctx context.Context, code string, version int) (InstrumentDefinition, error)
}

// InstrumentDefinition contains instrument metadata needed for fungibility validation.
// This mirrors the relevant fields from the reference-data cache.CachedInstrument.
type InstrumentDefinition interface {
	// GetFungibilityKeyProgram returns the pre-compiled CEL program for evaluating
	// fungibility keys. Returns nil if the instrument is fully fungible (no expression).
	GetFungibilityKeyProgram() domain.FungibilityKeyProgram
}

// ReferenceDataClient defines the interface for the reference-data service client.
// This is the interface that the actual refclient.Client implements.
type ReferenceDataClient interface {
	// GetInstrument retrieves an instrument with compiled CEL programs from the tiered cache.
	GetInstrument(ctx context.Context, code string, version int) (CachedInstrumentResult, error)
}

// CachedInstrumentResult defines the interface for cached instrument results.
// This matches the relevant methods from cache.CachedInstrument.
type CachedInstrumentResult interface {
	// GetBucketKeyProgram returns the CEL program for fungibility key evaluation.
	// Returns nil if no expression is defined (fully fungible).
	GetBucketKeyProgram() interface{}
}

// ReferenceDataRegistryAdapter adapts the reference-data client to the InstrumentRegistry interface.
// This allows the financial-accounting service to use the reference-data client for
// fungibility validation without directly depending on the cache package types.
type ReferenceDataRegistryAdapter struct {
	client ReferenceDataClient
}

// NewReferenceDataRegistryAdapter creates a new adapter wrapping the reference-data client.
func NewReferenceDataRegistryAdapter(client ReferenceDataClient) *ReferenceDataRegistryAdapter {
	return &ReferenceDataRegistryAdapter{client: client}
}

// GetInstrument retrieves an instrument definition from the reference-data service.
func (a *ReferenceDataRegistryAdapter) GetInstrument(ctx context.Context, code string, version int) (InstrumentDefinition, error) {
	cached, err := a.client.GetInstrument(ctx, code, version)
	if err != nil {
		return nil, err
	}
	return &cachedInstrumentAdapter{cached: cached}, nil
}

// cachedInstrumentAdapter adapts CachedInstrumentResult to InstrumentDefinition.
type cachedInstrumentAdapter struct {
	cached CachedInstrumentResult
}

// GetFungibilityKeyProgram returns the CEL program wrapped as a FungibilityKeyProgram.
func (a *cachedInstrumentAdapter) GetFungibilityKeyProgram() domain.FungibilityKeyProgram {
	program := a.cached.GetBucketKeyProgram()
	if program == nil {
		return nil
	}
	return domain.NewCELProgramAdapter(program)
}

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
// - Fungibility validation for non-fungible instruments
//
// Architecture patterns:
// - ADR-0002: One microservice per BIAN domain
// - ADR-0004: Event schema evolution with buf tooling
// - ADR-0005: Adapter pattern for layer translation
// - Constructor injection for dependencies
// - Idempotency for exactly-once processing
//
// gRPC endpoint implementations are split across files:
// - grpc_posting_endpoints.go: CaptureLedgerPosting, RetrieveLedgerPosting, UpdateLedgerPosting
// - grpc_booking_endpoints.go: InitiateFinancialBookingLog, RetrieveFinancialBookingLog, UpdateFinancialBookingLog, ControlFinancialBookingLog
// - grpc_ledger_endpoints.go: ListFinancialBookingLogs, ListLedgerPostings
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

	// registry provides instrument definitions for fungibility validation.
	// May be nil if fungibility validation is disabled (e.g., in tests or legacy mode).
	// When nil, fungibility validation is skipped (all instruments treated as fully fungible).
	registry InstrumentRegistry

	// instrumentResolver resolves instrument properties from Reference Data.
	// Used for validating instrument codes (replacing legacy isValidCurrencyCode).
	// May be nil if Reference Data is unavailable; validation is skipped when nil.
	instrumentResolver refdata.InstrumentResolver
}

// Option configures a FinancialAccountingService.
type Option func(*FinancialAccountingService)

// WithRegistry enables fungibility validation using the provided instrument registry.
// When the registry is nil or this option is not used, fungibility validation is skipped
// and all instruments are treated as fully fungible.
func WithRegistry(registry InstrumentRegistry) Option {
	return func(s *FinancialAccountingService) {
		s.registry = registry
	}
}

// WithInstrumentResolver enables instrument code validation via Reference Data lookup.
// When configured, instrument codes are validated by resolving them through the resolver
// instead of the legacy isValidCurrencyCode() check (which only accepted 3-char ISO 4217 codes).
// When nil or not used, instrument code validation in list filters is skipped.
func WithInstrumentResolver(resolver refdata.InstrumentResolver) Option {
	return func(s *FinancialAccountingService) {
		s.instrumentResolver = resolver
	}
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
// Optional configuration via Option:
//   - WithRegistry: Enables fungibility validation using a reference-data registry
//
// The returned service embeds UnimplementedFinancialAccountingServiceServer, which provides
// default "Unimplemented" responses for all gRPC methods. Methods will be implemented incrementally
// in subsequent subtasks (9.2, 9.3, 9.4, 9.5).
//
// Returns an error if any required dependency is nil.
//
// Example usage:
//
//	repo := persistence.NewLedgerRepository(db)
//	publisher := messaging.NewKafkaEventPublisher(kafkaProducer)
//	idempotencySvc := idempotency.NewRedisService(redisClient)
//	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
//	outboxRepo := events.NewPostgresOutboxRepository(db)
//	registryClient := refclient.New(...)
//
//	service, err := NewFinancialAccountingService(
//	    repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
//	    WithRegistry(registryClient),
//	)
//	if err != nil {
//	    return fmt.Errorf("failed to create financial accounting service: %w", err)
//	}
func NewFinancialAccountingService(
	repository *persistence.LedgerRepository,
	eventPublisher EventPublisher,
	idempotencySvc idempotency.Service,
	outboxPublisher *events.OutboxPublisher,
	outboxRepo *events.PostgresOutboxRepository,
	opts ...Option,
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

	svc := &FinancialAccountingService{
		repository:          repository,
		eventPublisher:      eventPublisher,
		idempotency:         idempotencySvc,
		idempotencyExecutor: executor,
		outboxPublisher:     outboxPublisher,
		outboxRepo:          outboxRepo,
	}

	// Apply optional configurations
	for _, opt := range opts {
		opt(svc)
	}

	return svc, nil
}
