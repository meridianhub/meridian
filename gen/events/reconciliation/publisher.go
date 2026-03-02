// Code generated from api/asyncapi/reconciliation.yaml. DO NOT EDIT.

package reconciliation

import (
	"context"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/shared/platform/events"
	"gorm.io/gorm"
)

// Publisher provides type-safe event publishing for the Reconciliation Events domain.
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

// PublishDisputeCreated publishes a DisputeCreatedEvent to "reconciliation.dispute-created.v1".
//
// Published when a reconciliation dispute is raised
func (p *Publisher) PublishDisputeCreated(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.DisputeCreatedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "reconciliation.dispute_created.v1",
		Topic:         "reconciliation.dispute-created.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishDisputeResolved publishes a DisputeResolvedEvent to "reconciliation.dispute-resolved.v1".
//
// Published when a reconciliation dispute is resolved
func (p *Publisher) PublishDisputeResolved(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.DisputeResolvedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "reconciliation.dispute_resolved.v1",
		Topic:         "reconciliation.dispute-resolved.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishPositionLockRequested publishes a PositionLockRequestedEvent to "reconciliation.position-lock-requested.v1".
//
// Published when a position lock is requested to freeze a position for reconciliation
func (p *Publisher) PublishPositionLockRequested(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.PositionLockRequestedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "reconciliation.position_lock_requested.v1",
		Topic:         "reconciliation.position-lock-requested.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishReconciliationRunCompleted publishes a ReconciliationRunCompletedEvent to "reconciliation.run-completed.v1".
//
// Published when a reconciliation run completes (with or without variances)
func (p *Publisher) PublishReconciliationRunCompleted(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.ReconciliationRunCompletedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "reconciliation.run_completed.v1",
		Topic:         "reconciliation.run-completed.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishReconciliationRunStarted publishes a ReconciliationRunStartedEvent to "reconciliation.run-started.v1".
//
// Published when a reconciliation run begins
func (p *Publisher) PublishReconciliationRunStarted(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.ReconciliationRunStartedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "reconciliation.run_started.v1",
		Topic:         "reconciliation.run-started.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}

// PublishVarianceDetected publishes a VarianceDetectedEvent to "reconciliation.variance-detected.v1".
//
// Published when a position variance is detected during reconciliation
func (p *Publisher) PublishVarianceDetected(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.VarianceDetectedEvent,
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "reconciliation.variance_detected.v1",
		Topic:         "reconciliation.variance-detected.v1",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}
