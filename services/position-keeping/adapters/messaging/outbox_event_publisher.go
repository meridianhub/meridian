// Package messaging provides adapters for event-driven communication.
package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
)

// ErrOutboxPublishNotSupported is returned when attempting to publish without a transaction.
// Use BuildOutboxFn with CreateWithOutbox or UpdateWithOutbox for transactional writes.
var ErrOutboxPublishNotSupported = errors.New("OutboxEventPublisher requires a transaction: use BuildOutboxFn with CreateWithOutbox or UpdateWithOutbox")

// outboxTopicMap maps position-keeping event types to their Kafka topics.
var outboxTopicMap = map[string]string{
	"position_keeping.transaction_captured.v1":      topics.PositionKeepingTransactionCapturedV1,
	"position_keeping.transaction_amended.v1":       topics.PositionKeepingTransactionAmendedV1,
	"position_keeping.transaction_reconciled.v1":    topics.PositionKeepingTransactionReconciledV1,
	"position_keeping.transaction_posted.v1":        topics.PositionKeepingTransactionPostedV1,
	"position_keeping.transaction_rejected.v1":      topics.PositionKeepingTransactionRejectedV1,
	"position_keeping.transaction_failed.v1":        topics.PositionKeepingTransactionFailedV1,
	"position_keeping.transaction_cancelled.v1":     topics.PositionKeepingTransactionCancelledV1,
	"position_keeping.bulk_transaction_captured.v1": topics.PositionKeepingBulkTransactionCapturedV1,
	"position_keeping.opening_balance_recorded.v1":  topics.PositionKeepingOpeningBalanceRecordedV1,
}

// OutboxEventPublisher implements domain.EventPublisher using the transactional outbox pattern.
// Events are written to the event_outbox table within a provided pgx transaction, guaranteeing
// at-least-once delivery via the background outbox worker.
//
// This publisher is used with the repository's transactional write methods (CreateWithOutbox,
// UpdateWithOutbox) which call Publish within the same transaction as the business operation.
type OutboxEventPublisher struct {
	outboxRepo  *events.PgxOutboxRepository
	serviceName string
}

// NewOutboxEventPublisher creates a new OutboxEventPublisher.
func NewOutboxEventPublisher(outboxRepo *events.PgxOutboxRepository) (*OutboxEventPublisher, error) {
	if outboxRepo == nil {
		return nil, ErrNilProducer
	}
	return &OutboxEventPublisher{
		outboxRepo:  outboxRepo,
		serviceName: "position-keeping",
	}, nil
}

// BuildOutboxFn builds a pgx transaction callback that writes the given events to the outbox.
// The returned function is intended to be passed to repository methods like CreateWithOutbox
// and UpdateWithOutbox, which invoke it within the same transaction as the database write.
func (p *OutboxEventPublisher) BuildOutboxFn(ctx context.Context, evts []domain.DomainEvent) func(pgx.Tx) error {
	return func(tx pgx.Tx) error {
		for i, event := range evts {
			if err := p.publishWithTx(ctx, tx, event); err != nil {
				return fmt.Errorf("failed to write event at index %d to outbox: %w", i, err)
			}
		}
		return nil
	}
}

// publishWithTx writes a single domain event to the outbox table within the provided transaction.
func (p *OutboxEventPublisher) publishWithTx(ctx context.Context, tx pgx.Tx, event domain.DomainEvent) error {
	if event == nil {
		return ErrNilEvent
	}

	topic, ok := outboxTopicMap[event.EventType()]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownEventType, event.EventType())
	}

	protoEvent := event.ToProto()
	protoMsg, ok := protoEvent.(proto.Message)
	if !ok {
		return fmt.Errorf("%w: event type %s", ErrInvalidProtoEvent, event.EventType())
	}

	payload, err := proto.Marshal(protoMsg)
	if err != nil {
		return fmt.Errorf("failed to serialize event %s: %w", event.EventType(), err)
	}

	entry := &events.EventOutbox{
		EventType:     event.EventType(),
		AggregateID:   event.AggregateID(),
		AggregateType: "FinancialPositionLog",
		EventPayload:  payload,
		Topic:         topic,
		PartitionKey:  event.AggregateID(),
		ServiceName:   p.serviceName,
	}

	if err := p.outboxRepo.InsertWithPgxTx(ctx, tx, entry); err != nil {
		return fmt.Errorf("failed to insert outbox entry for event %s: %w", event.EventType(), err)
	}

	return nil
}

// Publish implements domain.EventPublisher for backwards compatibility.
// This variant does not provide transactional guarantees — prefer BuildOutboxFn with
// the repository's CreateWithOutbox / UpdateWithOutbox methods for atomic writes.
//
// This method is provided so that OutboxEventPublisher satisfies the domain.EventPublisher
// interface and can be used as a drop-in replacement in contexts where a transaction is not
// available (e.g., batch operations that use non-transactional code paths).
func (p *OutboxEventPublisher) Publish(_ context.Context, _ domain.DomainEvent) error {
	// Non-transactional publish is not supported by this publisher.
	// Callers must use BuildOutboxFn with repository transactional write methods.
	return fmt.Errorf("publish: %w", ErrOutboxPublishNotSupported)
}

// PublishBatch implements domain.EventPublisher for backwards compatibility.
func (p *OutboxEventPublisher) PublishBatch(_ context.Context, _ []domain.DomainEvent) error {
	return fmt.Errorf("publish batch: %w", ErrOutboxPublishNotSupported)
}
