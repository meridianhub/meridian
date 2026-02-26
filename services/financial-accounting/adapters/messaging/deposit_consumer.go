// Package messaging provides Kafka consumer adapters for event-driven communication.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrMissingValueDate is returned when a deposit event has no value date
	ErrMissingValueDate = errors.New("deposit event: value_date is required")
	// ErrMissingTimestamp is returned when a deposit event has no timestamp
	ErrMissingTimestamp = errors.New("deposit event: timestamp is required")
	// ErrInvalidCurrency is returned when a deposit event has an unknown or unspecified currency
	ErrInvalidCurrency = errors.New("deposit event: unknown or unspecified currency")
	// ErrUnexpectedMessageType is returned when the message is not a DepositEvent
	ErrUnexpectedMessageType = errors.New("unexpected message type")
	// ErrNilIdempotencyService is returned when the idempotency service is nil
	ErrNilIdempotencyService = errors.New("idempotency service cannot be nil")
	// ErrConcurrentProcessing is returned when another consumer is processing the same event
	ErrConcurrentProcessing = errors.New("deposit event is being processed by another consumer")
)

// Idempotency TTL constants. These control how long results are cached in Redis.
const (
	// lockTTL is how long a processing lock is held before automatic expiration.
	// This prevents deadlocks if a consumer crashes while processing.
	lockTTL = 5 * time.Minute

	// successResultTTL is how long successful results are cached.
	// Longer TTL reduces Redis lookups for frequently retried events.
	successResultTTL = 24 * time.Hour

	// failureResultTTL is how long failed results are cached.
	// Shorter TTL allows retries after transient failures are resolved.
	failureResultTTL = 1 * time.Hour
)

// DepositConsumer consumes DepositEvent messages from Kafka and processes them
// through the PostingService to create double-entry ledger postings.
type DepositConsumer struct {
	consumer       *kafka.ProtoConsumer
	postingService *service.PostingService
	validator      protovalidate.Validator
	idempotency    idempotency.Service
}

// NewDepositConsumer creates a Kafka consumer for DepositEvent messages.
// It connects to Kafka using the provided configuration and sets up a handler
// that converts DepositEvents into PostingService commands.
//
// Parameters:
// - config: Kafka consumer configuration (bootstrap servers, group ID, etc.)
// - postingService: Service that creates ledger postings
// - idempotencySvc: Service that provides distributed idempotency protection
//
// Returns an error if the consumer cannot be initialized or if idempotency service is nil.
func NewDepositConsumer(config kafka.ConsumerConfig, postingService *service.PostingService, idempotencySvc idempotency.Service) (*DepositConsumer, error) {
	if idempotencySvc == nil {
		return nil, ErrNilIdempotencyService
	}

	validator, err := protovalidate.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create validator: %w", err)
	}

	dc := &DepositConsumer{
		postingService: postingService,
		validator:      validator,
		idempotency:    idempotencySvc,
	}

	// Message factory creates new DepositEvent instances for deserialization
	msgFactory := func() proto.Message {
		return &eventsv1.DepositEvent{}
	}

	// Handler converts Kafka messages to service commands
	handler := func(ctx context.Context, _ []byte, msg proto.Message) error {
		event, ok := msg.(*eventsv1.DepositEvent)
		if !ok {
			return fmt.Errorf("%w: expected *DepositEvent, got %T", ErrUnexpectedMessageType, msg)
		}
		return dc.handleDepositEvent(ctx, event)
	}

	consumer, err := kafka.NewProtoConsumer(config, msgFactory, handler)
	if err != nil {
		return nil, fmt.Errorf("failed to create deposit consumer: %w", err)
	}

	dc.consumer = consumer
	return dc, nil
}

// handleDepositEvent processes a single DepositEvent by converting it to
// the PostingService format and creating double-entry ledger postings.
// Uses Redis-based idempotency to prevent duplicate ledger entries from
// Kafka's at-least-once delivery semantics.
func (dc *DepositConsumer) handleDepositEvent(ctx context.Context, event *eventsv1.DepositEvent) error {
	// Validate proto message
	if err := dc.validator.Validate(event); err != nil {
		return fmt.Errorf("invalid deposit event: %w", err)
	}

	// Validate required fields to prevent nil pointer panics
	if event.ValueDate == nil {
		return ErrMissingValueDate
	}
	if event.Timestamp == nil {
		return ErrMissingTimestamp
	}

	// Build idempotency key for duplicate detection
	idempotencyKey := idempotency.Key{
		TenantID:  extractTenantID(ctx),
		Namespace: "financial-accounting",
		Operation: "process-deposit",
		EntityID:  event.AccountId,
		RequestID: event.CorrelationId,
	}

	// Check if already processed (fast path for duplicates)
	_, err := dc.idempotency.Check(ctx, idempotencyKey)
	if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
		// Already processed - return success (idempotent)
		return nil
	}
	if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		return fmt.Errorf("idempotency check failed: %w", err)
	}

	// Generate unique token for lock ownership
	lockToken := uuid.New().String()

	// Acquire distributed lock atomically (uses SETNX - prevents race condition)
	// MaxRetries=0 means if lock is held, return immediately with error
	lockOpts := idempotency.LockOptions{
		TTL:        lockTTL,
		Token:      lockToken,
		MaxRetries: 0,
		RetryDelay: 0,
	}
	if err := dc.idempotency.Acquire(ctx, idempotencyKey, lockOpts); err != nil {
		if errors.Is(err, idempotency.ErrLockAcquisitionFailed) {
			return ErrConcurrentProcessing
		}
		return fmt.Errorf("failed to acquire lock: %w", err)
	}

	// Ensure lock is released when done (best-effort cleanup)
	defer func() {
		_ = dc.idempotency.Release(ctx, idempotencyKey, lockToken)
	}()

	// Re-check after acquiring lock (double-check pattern)
	// Another consumer may have completed processing between our initial check and lock acquisition
	_, err = dc.idempotency.Check(ctx, idempotencyKey)
	if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
		return nil
	}
	if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		return fmt.Errorf("idempotency re-check failed: %w", err)
	}

	// Convert proto timestamp to time.Time
	valueDate := event.ValueDate.AsTime()

	// Validate instrument code
	currencyCode := event.InstrumentCode
	if currencyCode == "" {
		// Store failure result before returning error
		dc.storeFailureResult(ctx, idempotencyKey, fmt.Sprintf("%v: %v", ErrInvalidCurrency, event.InstrumentCode))
		return fmt.Errorf("%w: %v", ErrInvalidCurrency, event.InstrumentCode)
	}

	// Create service event
	depositEvent := service.DepositEvent{
		AccountID:     event.AccountId,
		AmountCents:   event.AmountCents,
		Currency:      currencyCode,
		CorrelationID: event.CorrelationId,
		ValueDate:     valueDate,
	}

	// Process through posting service
	if err := dc.postingService.ProcessDeposit(ctx, depositEvent); err != nil {
		// Store failure result
		dc.storeFailureResult(ctx, idempotencyKey, err.Error())
		return fmt.Errorf("failed to process deposit: %w", err)
	}

	// Store success result
	successResult := idempotency.Result{
		Key:         idempotencyKey,
		Status:      idempotency.StatusCompleted,
		Data:        nil, // No response data needed for events
		Error:       "",
		CompletedAt: time.Now(),
		TTL:         successResultTTL,
	}
	if err := dc.idempotency.StoreResult(ctx, successResult); err != nil {
		return fmt.Errorf("failed to store idempotency result: %w", err)
	}

	return nil
}

// storeFailureResult stores a failure result in the idempotency cache (best-effort).
// Failed operations are cached for failureResultTTL to prevent retry storms.
func (dc *DepositConsumer) storeFailureResult(ctx context.Context, key idempotency.Key, errMsg string) {
	failureResult := idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusFailed,
		Data:        nil,
		Error:       errMsg,
		CompletedAt: time.Now(),
		TTL:         failureResultTTL,
	}
	_ = dc.idempotency.StoreResult(ctx, failureResult) // Best effort
}

// extractTenantID extracts the tenant ID from context for multi-tenant isolation.
// Returns empty string if no tenant is present (single-tenant mode).
func extractTenantID(ctx context.Context) string {
	if tenantID, ok := tenant.FromContext(ctx); ok {
		return string(tenantID)
	}
	return ""
}

// Start begins consuming DepositEvent messages from the specified topics.
// This method blocks until Stop() is called or an error occurs.
func (dc *DepositConsumer) Start(topics []string) error {
	if err := dc.consumer.Subscribe(topics); err != nil {
		return fmt.Errorf("failed to subscribe to topics: %w", err)
	}
	return nil
}

// Stop gracefully stops the consumer.
func (dc *DepositConsumer) Stop() {
	dc.consumer.Stop()
}

// Close closes the consumer and releases resources.
func (dc *DepositConsumer) Close() error {
	if err := dc.consumer.Close(); err != nil {
		return fmt.Errorf("failed to close consumer: %w", err)
	}
	return nil
}
