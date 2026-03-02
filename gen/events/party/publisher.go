// Code generated from api/asyncapi/party.yaml. DO NOT EDIT.

package party

import (
	"context"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/shared/platform/events"
	"gorm.io/gorm"
)

// Publisher provides type-safe event publishing for the Party Events domain.
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

// PublishPartyCreatedEvent publishes a PartyCreatedEvent to "party.created.v1".
//
// Published when a new party is registered in the system
func (p *Publisher) PublishPartyCreatedEvent(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PartyCreatedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "party.created.v1",
		Topic:         "party.created.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishPartyUpdatedEvent publishes a PartyUpdatedEvent to "party.updated.v1".
//
// Published when an existing party's details are updated
func (p *Publisher) PublishPartyUpdatedEvent(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PartyUpdatedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "party.updated.v1",
		Topic:         "party.updated.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishPartyVerificationCompletedEvent publishes a PartyVerificationCompletedEvent to "party.verification-completed.v1".
//
// Published when a KYC/AML verification reaches a terminal state (APPROVED, REJECTED, MANUAL_REVIEW)
func (p *Publisher) PublishPartyVerificationCompletedEvent(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PartyVerificationCompletedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "party.verification_completed.v1",
		Topic:         "party.verification-completed.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}
