// Code generated from api/asyncapi/current-account.yaml. DO NOT EDIT.

package current_account

import (
	"context"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/shared/platform/events"
	"gorm.io/gorm"
)

// Publisher provides type-safe event publishing for the Current Account Events domain.
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

// PublishAccountClosedEvent publishes a AccountClosedEvent to "current-account.account-closed.v1".
//
// Published when an account is permanently closed
func (p *Publisher) PublishAccountClosedEvent(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.AccountClosedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "current_account.account_closed.v1",
		Topic:         "current-account.account-closed.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishAccountFrozenEvent publishes a AccountFrozenEvent to "current-account.account-frozen.v1".
//
// Published when an account is frozen (no debits or credits permitted)
func (p *Publisher) PublishAccountFrozenEvent(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.AccountFrozenEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "current_account.account_frozen.v1",
		Topic:         "current-account.account-frozen.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishAccountUnfrozenEvent publishes a AccountUnfrozenEvent to "current-account.account-unfrozen.v1".
//
// Published when a previously frozen account is unfrozen
func (p *Publisher) PublishAccountUnfrozenEvent(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.AccountUnfrozenEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "current_account.account_unfrozen.v1",
		Topic:         "current-account.account-unfrozen.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishWithdrawalStatusUpdated publishes a WithdrawalStatusUpdatedEvent to "current-account.withdrawal-status.v1".
//
// Published when a withdrawal status changes (initiated, completed, failed, reversed)
func (p *Publisher) PublishWithdrawalStatusUpdated(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.WithdrawalStatusUpdatedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "current_account.withdrawal_status.v1",
		Topic:         "current-account.withdrawal-status.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}
