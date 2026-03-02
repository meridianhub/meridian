// Code generated from api/asyncapi/internal-account.yaml. DO NOT EDIT.

package internal_account

import (
	"context"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/shared/platform/events"
	"gorm.io/gorm"
)

// Publisher provides type-safe event publishing for the Internal Account Events domain.
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

// PublishBookingCreatedEvent publishes a BookingCreatedEvent to "internal-account.booking-created.v1".
//
// Published when a new booking is posted to an internal account
func (p *Publisher) PublishBookingCreatedEvent(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.BookingCreatedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "internal_account.booking_created.v1",
		Topic:         "internal-account.booking-created.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishFacilityCreatedEvent publishes a FacilityCreatedEvent to "internal-account.facility-created.v1".
//
// Published when a new internal account facility is initiated
func (p *Publisher) PublishFacilityCreatedEvent(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.FacilityCreatedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "internal_account.facility_created.v1",
		Topic:         "internal-account.facility-created.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}
