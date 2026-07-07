// Package messaging provides Kafka consumer adapters for event-driven communication.
package messaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"buf.build/go/protovalidate"

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
)

// successResultTTL is how long the Redis fast-path "already processed" marker is
// cached. This marker is an optimisation only: it lets redeliveries short-circuit
// before touching the database. Correctness does not depend on it - the database
// idempotency marker (written in the same transaction as the postings) is the
// source of truth.
const successResultTTL = 24 * time.Hour

// DepositConsumer consumes DepositEvent messages from Kafka and processes them
// through the PostingService to create double-entry ledger postings.
//
// Exactly-once processing is guaranteed at the database layer: each deposit's
// postings are written together with a per-deposit idempotency marker in a
// single transaction, and the marker's unique key rejects redeliveries. Redis is
// used only as a best-effort fast path to skip the database round-trip for
// deposits already known to be complete.
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
// - idempotencySvc: Best-effort fast-path idempotency cache (must not be nil)
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

// handleDepositEvent processes a single DepositEvent by converting it to the
// PostingService format and creating double-entry ledger postings.
//
// Idempotency is enforced at the database layer: the deposit's postings and a
// per-deposit idempotency marker are written in one transaction, so Kafka's
// at-least-once redelivery cannot produce duplicate postings even if the process
// crashes or Redis is unavailable. Redis is consulted only as a fast path.
func (dc *DepositConsumer) handleDepositEvent(ctx context.Context, event *eventsv1.DepositEvent) error {
	if err := dc.validateDepositEvent(event); err != nil {
		return err
	}

	dedupeKey := buildDepositDedupeKey(ctx, event)
	redisKey := buildDepositIdempotencyKey(ctx, event, dedupeKey)

	// Fast path: if Redis already knows this deposit completed, skip the database
	// work. Best-effort only - any Redis error is treated as "unknown" so the
	// authoritative database path still runs.
	if dc.redisMarksProcessed(ctx, redisKey) {
		return nil
	}

	currencyCode := event.InstrumentCode
	if currencyCode == "" {
		return fmt.Errorf("%w: %v", ErrInvalidCurrency, event.InstrumentCode)
	}

	// Pass the raw minor-unit value as a string. The PostingService will
	// resolve instrument precision and convert from minor to major units.
	// This avoids hardcoding a /100 divisor that would be wrong for
	// non-2dp instruments (e.g., JPY=0dp, KWH=3dp).
	depositEvent := service.DepositEvent{
		AccountID:       event.AccountId,
		AmountMinorUnit: event.AmountCents,
		InstrumentCode:  currencyCode,
		CorrelationID:   event.CorrelationId,
		ValueDate:       event.ValueDate.AsTime(),
	}

	err := dc.postingService.ProcessDepositWithDedupeKey(ctx, depositEvent, dedupeKey)
	if err != nil {
		if errors.Is(err, service.ErrDepositAlreadyProcessed) {
			// Duplicate delivery: the deposit is already on the ledger. Refresh the
			// Redis fast-path marker so future redeliveries short-circuit, then
			// treat this as a successful no-op.
			dc.markRedisProcessed(ctx, redisKey)
			return nil
		}
		return fmt.Errorf("failed to process deposit: %w", err)
	}

	// Best-effort: record completion in Redis for the fast path on future deliveries.
	dc.markRedisProcessed(ctx, redisKey)
	return nil
}

// validateDepositEvent validates the proto message and required fields.
func (dc *DepositConsumer) validateDepositEvent(event *eventsv1.DepositEvent) error {
	if err := dc.validator.Validate(event); err != nil {
		return fmt.Errorf("invalid deposit event: %w", err)
	}
	if event.ValueDate == nil {
		return ErrMissingValueDate
	}
	if event.Timestamp == nil {
		return ErrMissingTimestamp
	}
	return nil
}

// buildDepositDedupeKey derives a deterministic, per-deposit idempotency
// fingerprint from the event's identifying fields.
//
// The key is stable across Kafka redeliveries of the same event (identical bytes
// produce an identical key) but distinct for genuinely different deposits -
// including deposits that reuse a correlation ID, which is why correlation_id
// alone must NOT be used as the idempotency key. The event timestamp is included
// so that two economically-identical deposits emitted at different times remain
// distinct.
func buildDepositDedupeKey(ctx context.Context, event *eventsv1.DepositEvent) string {
	var b strings.Builder
	b.WriteString(extractTenantID(ctx))
	b.WriteByte('|')
	b.WriteString(event.AccountId)
	b.WriteByte('|')
	b.WriteString(strconv.FormatInt(event.AmountCents, 10))
	b.WriteByte('|')
	b.WriteString(event.InstrumentCode)
	b.WriteByte('|')
	b.WriteString(event.CorrelationId)
	b.WriteByte('|')
	b.WriteString(formatTimestamp(event.ValueDate.GetSeconds(), event.ValueDate.GetNanos()))
	b.WriteByte('|')
	b.WriteString(formatTimestamp(event.Timestamp.GetSeconds(), event.Timestamp.GetNanos()))

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// formatTimestamp renders a protobuf timestamp's seconds/nanos as a stable string.
func formatTimestamp(seconds int64, nanos int32) string {
	return strconv.FormatInt(seconds, 10) + ":" + strconv.FormatInt(int64(nanos), 10)
}

// buildDepositIdempotencyKey constructs the Redis fast-path idempotency key for a
// deposit event. It keys on the per-deposit dedupe fingerprint (NOT the
// correlation ID) so the fast path agrees with the authoritative database marker.
func buildDepositIdempotencyKey(ctx context.Context, event *eventsv1.DepositEvent, dedupeKey string) idempotency.Key {
	return idempotency.Key{
		TenantID:  extractTenantID(ctx),
		Namespace: "financial-accounting",
		Operation: "process-deposit",
		EntityID:  event.AccountId,
		RequestID: dedupeKey,
	}
}

// redisMarksProcessed reports whether Redis already has a completed marker for
// this deposit. Best-effort: any Redis error (including Redis being down) is
// treated as "not processed" so the authoritative database path still runs.
func (dc *DepositConsumer) redisMarksProcessed(ctx context.Context, key idempotency.Key) bool {
	_, err := dc.idempotency.Check(ctx, key)
	return errors.Is(err, idempotency.ErrOperationAlreadyProcessed)
}

// markRedisProcessed records a completed marker in Redis for the fast path.
// Best-effort: failures are ignored because the database is the source of truth.
func (dc *DepositConsumer) markRedisProcessed(ctx context.Context, key idempotency.Key) {
	result := idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusCompleted,
		Data:        nil,
		Error:       "",
		CompletedAt: time.Now(),
		TTL:         successResultTTL,
	}
	_ = dc.idempotency.StoreResult(ctx, result) // Best effort
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
