// Code generated from api/asyncapi/payment-order.yaml. DO NOT EDIT.

package payment_order

import (
	"context"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/shared/platform/events"
	"gorm.io/gorm"
)

// Publisher provides type-safe event publishing for the Payment Order Events domain.
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

// PublishPaymentOrderCancelled publishes a PaymentOrderCancelledEvent to "payment-order.cancelled.v1".
//
// Published when a payment order is cancelled before execution
func (p *Publisher) PublishPaymentOrderCancelled(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PaymentOrderCancelledEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "payment_order.cancelled.v1",
		Topic:         "payment-order.cancelled.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishPaymentOrderCompleted publishes a PaymentOrderCompletedEvent to "payment-order.completed.v1".
//
// Published when a payment order successfully completes
func (p *Publisher) PublishPaymentOrderCompleted(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PaymentOrderCompletedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "payment_order.completed.v1",
		Topic:         "payment-order.completed.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishPaymentOrderExecuting publishes a PaymentOrderExecutingEvent to "payment-order.executing.v1".
//
// Published when a payment order begins execution with the payment gateway
func (p *Publisher) PublishPaymentOrderExecuting(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PaymentOrderExecutingEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "payment_order.executing.v1",
		Topic:         "payment-order.executing.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishPaymentOrderFailed publishes a PaymentOrderFailedEvent to "payment-order.failed.v1".
//
// Published when a payment order fails and cannot be retried
func (p *Publisher) PublishPaymentOrderFailed(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PaymentOrderFailedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "payment_order.failed.v1",
		Topic:         "payment-order.failed.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishPaymentOrderInitiated publishes a PaymentOrderInitiatedEvent to "payment-order.initiated.v1".
//
// Published when a new payment order is initiated
func (p *Publisher) PublishPaymentOrderInitiated(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PaymentOrderInitiatedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "payment_order.initiated.v1",
		Topic:         "payment-order.initiated.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishPaymentOrderReserved publishes a PaymentOrderReservedEvent to "payment-order.reserved.v1".
//
// Published when funds are successfully reserved for a payment order
func (p *Publisher) PublishPaymentOrderReserved(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PaymentOrderReservedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "payment_order.reserved.v1",
		Topic:         "payment-order.reserved.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishPaymentOrderReversed publishes a PaymentOrderReversedEvent to "payment-order.reversed.v1".
//
// Published when a completed payment order is reversed
func (p *Publisher) PublishPaymentOrderReversed(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PaymentOrderReversedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "payment_order.reversed.v1",
		Topic:         "payment-order.reversed.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}
