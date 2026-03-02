package events

import (
	"context"
	"fmt"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// OutboxPublisher provides methods for publishing events through the transactional outbox pattern.
// Instead of publishing directly to Kafka, events are written to the database outbox table
// within the same transaction as the business operation, ensuring atomic consistency.
//
// The background Worker then processes these events asynchronously and publishes them to Kafka.
type OutboxPublisher struct {
	serviceName string
	validator   protovalidate.Validator
}

// NewOutboxPublisher creates a new OutboxPublisher for the given service.
// Panics if serviceName is empty to fail fast during initialization.
// Panics if the protovalidate validator cannot be created.
func NewOutboxPublisher(serviceName string) *OutboxPublisher {
	if serviceName == "" {
		panic("events: " + ErrEmptyServiceName.Error())
	}
	validator, err := protovalidate.New()
	if err != nil {
		panic(fmt.Sprintf("events: failed to create protovalidate validator: %v", err))
	}
	return &OutboxPublisher{
		serviceName: serviceName,
		validator:   validator,
	}
}

// PublishConfig contains configuration for publishing an event to the outbox.
type PublishConfig struct {
	// EventType is the fully qualified event type name (e.g., "position_keeping.transaction_suspended.v1")
	EventType string

	// AggregateID is the ID of the aggregate that produced this event
	AggregateID string

	// AggregateType is the type of aggregate (e.g., "FinancialPositionLog", "FinancialBookingLog")
	AggregateType string

	// Topic is the Kafka topic to publish to
	Topic string

	// CorrelationID links related events across services
	CorrelationID string

	// CausationID links to the event that caused this event
	CausationID string

	// PartitionKey is the key used for Kafka partitioning (defaults to AggregateID if empty)
	PartitionKey string
}

// Publish writes an event to the outbox table within the provided transaction.
// The event payload is serialized using protobuf.
//
// IMPORTANT: This method must be called within a database transaction (tx) that also
// contains the business operation. This ensures atomic consistency - either both the
// business operation and the event are persisted, or neither is.
//
// Example usage:
//
//	err := db.Transaction(func(tx *gorm.DB) error {
//	    // Business operation
//	    if err := tx.Save(&entity).Error; err != nil {
//	        return err
//	    }
//
//	    // Publish event (within same transaction)
//	    event := &eventsv1.EntitySuspendedEvent{...}
//	    if err := publisher.Publish(ctx, tx, event, config); err != nil {
//	        return err
//	    }
//
//	    return nil // Transaction commits only if both succeed
//	})
func (p *OutboxPublisher) Publish(
	ctx context.Context,
	tx *gorm.DB,
	event proto.Message,
	config PublishConfig,
) error {
	if tx == nil {
		return ErrNilTransaction
	}
	if event == nil {
		return ErrNilEvent
	}
	if config.EventType == "" {
		return ErrInvalidEventType
	}
	if config.Topic == "" {
		return ErrEmptyTopic
	}
	if config.AggregateID == "" {
		return ErrEmptyAggregateID
	}
	if config.AggregateType == "" {
		return ErrEmptyAggregateType
	}

	// Validate the protobuf event against its buf/validate constraints.
	// This enforces schema correctness before the event enters the outbox.
	// Once in the outbox, events are considered proven valid.
	if err := p.validator.Validate(event); err != nil {
		return fmt.Errorf("event payload validation failed: %w", err)
	}

	// Serialize the protobuf event
	payload, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to serialize event: %w", err)
	}

	// Use AggregateID as partition key if not specified
	partitionKey := config.PartitionKey
	if partitionKey == "" {
		partitionKey = config.AggregateID
	}

	// Create outbox entry
	entry := NewEventOutbox(
		config.EventType,
		config.AggregateID,
		config.AggregateType,
		payload,
		config.Topic,
		p.serviceName,
		config.CorrelationID,
	)
	entry.CausationID = config.CausationID
	entry.PartitionKey = partitionKey

	// Insert into outbox (uses the same transaction)
	if err := tx.WithContext(ctx).Create(entry).Error; err != nil {
		return fmt.Errorf("failed to insert outbox entry: %w", err)
	}

	return nil
}

// PublishControlEvent is a convenience method for publishing control operation events
// (SUSPEND, RESUME, TERMINATE). These events are audit-critical and require guaranteed delivery.
//
// Parameters:
//   - ctx: Context for the operation
//   - tx: Active database transaction (MUST be the same transaction as the control operation)
//   - event: The protobuf event to publish
//   - eventType: Fully qualified event type (e.g., "position_keeping.transaction_suspended.v1")
//   - aggregateID: ID of the entity being controlled
//   - aggregateType: Type of the entity (e.g., "FinancialPositionLog")
//   - topic: Kafka topic for the event
//   - correlationID: Correlation ID for tracing
func (p *OutboxPublisher) PublishControlEvent(
	ctx context.Context,
	tx *gorm.DB,
	event proto.Message,
	eventType string,
	aggregateID string,
	aggregateType string,
	topic string,
	correlationID string,
) error {
	return p.Publish(ctx, tx, event, PublishConfig{
		EventType:     eventType,
		AggregateID:   aggregateID,
		AggregateType: aggregateType,
		Topic:         topic,
		CorrelationID: correlationID,
	})
}
