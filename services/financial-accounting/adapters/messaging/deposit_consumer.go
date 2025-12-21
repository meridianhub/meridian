// Package messaging provides Kafka consumer adapters for event-driven communication.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"buf.build/go/protovalidate"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
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

	// Check if already processed
	result, err := dc.idempotency.Check(ctx, idempotencyKey)
	if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
		// Already processed - return success (idempotent)
		return nil
	}
	if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		return fmt.Errorf("idempotency check failed: %w", err)
	}

	// If we found a pending result, it means another consumer might be processing
	// Let the caller retry (return error so Kafka doesn't commit)
	if result != nil && result.Status == idempotency.StatusPending {
		return ErrConcurrentProcessing
	}

	// Mark as pending to prevent concurrent processing
	if err := dc.idempotency.MarkPending(ctx, idempotencyKey, 5*time.Minute); err != nil {
		return fmt.Errorf("failed to mark pending: %w", err)
	}

	// Convert proto timestamp to time.Time
	valueDate := event.ValueDate.AsTime()

	// Convert proto currency enum to ISO code (e.g., CURRENCY_GBP -> GBP)
	currencyCode := convertCurrencyToISO(event.Currency)
	if currencyCode == "" {
		// Store failure result before returning error
		dc.storeFailureResult(ctx, idempotencyKey, fmt.Sprintf("%v: %v", ErrInvalidCurrency, event.Currency))
		return fmt.Errorf("%w: %v", ErrInvalidCurrency, event.Currency)
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
		TTL:         24 * time.Hour, // Cache for 24 hours
	}
	if err := dc.idempotency.StoreResult(ctx, successResult); err != nil {
		return fmt.Errorf("failed to store idempotency result: %w", err)
	}

	return nil
}

// storeFailureResult stores a failure result in the idempotency cache (best-effort).
// Failed operations are cached for 1 hour to prevent retry storms.
func (dc *DepositConsumer) storeFailureResult(ctx context.Context, key idempotency.Key, errMsg string) {
	failureResult := idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusFailed,
		Data:        nil,
		Error:       errMsg,
		CompletedAt: time.Now(),
		TTL:         1 * time.Hour,
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

// convertCurrencyToISO converts proto Currency enum to ISO 4217 code string.
// Example: CURRENCY_GBP -> "GBP"
func convertCurrencyToISO(currency commonv1.Currency) string {
	switch currency {
	case commonv1.Currency_CURRENCY_UNSPECIFIED:
		return ""
	case commonv1.Currency_CURRENCY_GBP:
		return "GBP"
	case commonv1.Currency_CURRENCY_USD:
		return "USD"
	case commonv1.Currency_CURRENCY_EUR:
		return "EUR"
	case commonv1.Currency_CURRENCY_JPY:
		return "JPY"
	case commonv1.Currency_CURRENCY_CHF:
		return "CHF"
	case commonv1.Currency_CURRENCY_CAD:
		return "CAD"
	case commonv1.Currency_CURRENCY_AUD:
		return "AUD"
	default:
		return ""
	}
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
