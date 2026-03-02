// Code generated from api/asyncapi/position-keeping.yaml. DO NOT EDIT.

package position_keeping

import (
	"context"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/shared/platform/events"
	"gorm.io/gorm"
)

// Publisher provides type-safe event publishing for the Position Keeping Events domain.
type Publisher struct {
	outbox *events.OutboxPublisher
}

// NewPublisher creates a new Publisher wrapping the given OutboxPublisher.
func NewPublisher(outbox *events.OutboxPublisher) *Publisher {
	return &Publisher{outbox: outbox}
}

// PublishOption configures event publishing behavior.
type PublishOption func(*events.PublishConfig)

// WithCorrelationID returns a PublishOption that sets the correlation ID.
func WithCorrelationID(id string) PublishOption {
	return func(c *events.PublishConfig) { c.CorrelationID = id }
}

// WithCausationID returns a PublishOption that sets the causation ID.
func WithCausationID(id string) PublishOption {
	return func(c *events.PublishConfig) { c.CausationID = id }
}

// WithPartitionKey returns a PublishOption that overrides the default partition key.
func WithPartitionKey(key string) PublishOption {
	return func(c *events.PublishConfig) { c.PartitionKey = key }
}

// PublishBulkTransactionCaptured publishes a BulkTransactionCapturedEvent to "position-keeping.bulk-transaction-captured.v1".
//
// Published when a batch of transactions is captured atomically
func (p *Publisher) PublishBulkTransactionCaptured(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.BulkTransactionCapturedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "position_keeping.bulk_transaction_captured.v1",
		Topic:         "position-keeping.bulk-transaction-captured.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishOpeningBalanceRecorded publishes a OpeningBalanceRecordedEvent to "position-keeping.opening-balance-recorded.v1".
//
// Published when an opening balance is recorded for a new account
func (p *Publisher) PublishOpeningBalanceRecorded(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.OpeningBalanceRecordedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "position_keeping.opening_balance_recorded.v1",
		Topic:         "position-keeping.opening-balance-recorded.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishTransactionAmended publishes a TransactionAmendedEvent to "position-keeping.transaction-amended.v1".
//
// Published when a captured transaction is amended
func (p *Publisher) PublishTransactionAmended(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.TransactionAmendedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "position_keeping.transaction_amended.v1",
		Topic:         "position-keeping.transaction-amended.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishTransactionCancelled publishes a TransactionCancelledEvent to "position-keeping.transaction-cancelled.v1".
//
// Published when a transaction is cancelled
func (p *Publisher) PublishTransactionCancelled(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.TransactionCancelledEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "position_keeping.transaction_cancelled.v1",
		Topic:         "position-keeping.transaction-cancelled.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishTransactionCaptured publishes a TransactionCapturedEvent to "position-keeping.transaction-captured.v1".
//
// Published when a new financial transaction is captured
func (p *Publisher) PublishTransactionCaptured(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.TransactionCapturedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "position_keeping.transaction_captured.v1",
		Topic:         "position-keeping.transaction-captured.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishTransactionFailed publishes a TransactionFailedEvent to "position-keeping.transaction-failed.v1".
//
// Published when a transaction fails due to a system error
func (p *Publisher) PublishTransactionFailed(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.TransactionFailedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "position_keeping.transaction_failed.v1",
		Topic:         "position-keeping.transaction-failed.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishTransactionPosted publishes a TransactionPostedEvent to "position-keeping.transaction-posted.v1".
//
// Published when a transaction is posted to the ledger
func (p *Publisher) PublishTransactionPosted(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.TransactionPostedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "position_keeping.transaction_posted.v1",
		Topic:         "position-keeping.transaction-posted.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishTransactionReconciled publishes a TransactionReconciledEvent to "position-keeping.transaction-reconciled.v1".
//
// Published when a transaction is reconciled against actual settlement data
func (p *Publisher) PublishTransactionReconciled(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.TransactionReconciledEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "position_keeping.transaction_reconciled.v1",
		Topic:         "position-keeping.transaction-reconciled.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishTransactionRejected publishes a TransactionRejectedEvent to "position-keeping.transaction-rejected.v1".
//
// Published when a transaction is rejected due to validation failure
func (p *Publisher) PublishTransactionRejected(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.TransactionRejectedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "position_keeping.transaction_rejected.v1",
		Topic:         "position-keeping.transaction-rejected.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}
