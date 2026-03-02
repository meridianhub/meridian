// Package messaging provides event publishing adapters for the operational gateway service.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// topicToEventType maps Kafka topic constants to outbox event type strings.
var topicToEventType = map[string]string{
	topics.OperationalGatewayInstructionCreatedV1:      "operational_gateway.instruction_created.v1",
	topics.OperationalGatewayInstructionDispatchedV1:   "operational_gateway.instruction_dispatched.v1",
	topics.OperationalGatewayInstructionDeliveredV1:    "operational_gateway.instruction_delivered.v1",
	topics.OperationalGatewayInstructionAcknowledgedV1: "operational_gateway.instruction_acknowledged.v1",
	topics.OperationalGatewayInstructionFailedV1:       "operational_gateway.instruction_failed.v1",
	topics.OperationalGatewayInstructionExpiredV1:      "operational_gateway.instruction_expired.v1",
	topics.OperationalGatewayInstructionCancelledV1:    "operational_gateway.instruction_cancelled.v1",
}

// ErrUnknownTopic is returned when a topic has no mapping in the outbox publisher.
var ErrUnknownTopic = errors.New("unknown topic for outbox publishing")

// InstructionEventPublisher publishes instruction lifecycle events to the transactional outbox.
// Events are written to the event_outbox table within the same database transaction as the
// business operation, ensuring at-least-once delivery via the background outbox worker.
type InstructionEventPublisher struct {
	publisher *events.OutboxPublisher
}

// NewInstructionEventPublisher creates a new InstructionEventPublisher.
func NewInstructionEventPublisher(publisher *events.OutboxPublisher) *InstructionEventPublisher {
	return &InstructionEventPublisher{publisher: publisher}
}

// PublishCreated writes an instruction-created event to the outbox within the provided transaction.
func (p *InstructionEventPublisher) PublishCreated(ctx context.Context, tx *gorm.DB, instr *domain.Instruction) error {
	event := &eventsv1.InstructionCreatedEvent{
		EventId:              uuid.New().String(),
		InstructionId:        instr.ID.String(),
		TenantId:             instr.TenantID.String(),
		InstructionType:      instr.InstructionType,
		ProviderConnectionId: instr.ProviderConnectionID,
		CorrelationId:        instr.CorrelationID,
		CausationId:          instr.CausationID,
		OccurredAt:           timestamppb.New(time.Now()),
		Version:              instr.Version,
	}
	return p.publish(ctx, tx, event, topics.OperationalGatewayInstructionCreatedV1, instr.ID.String(), instr.CorrelationID, instr.CausationID)
}

// PublishDispatched writes an instruction-dispatched event to the outbox within the provided transaction.
func (p *InstructionEventPublisher) PublishDispatched(ctx context.Context, tx *gorm.DB, instr *domain.Instruction) error {
	event := &eventsv1.InstructionDispatchedEvent{
		EventId:              uuid.New().String(),
		InstructionId:        instr.ID.String(),
		TenantId:             instr.TenantID.String(),
		InstructionType:      instr.InstructionType,
		ProviderConnectionId: instr.ProviderConnectionID,
		AttemptNumber:        int32(instr.AttemptCount),
		CorrelationId:        instr.CorrelationID,
		CausationId:          instr.CausationID,
		OccurredAt:           timestamppb.New(time.Now()),
		Version:              instr.Version,
	}
	return p.publish(ctx, tx, event, topics.OperationalGatewayInstructionDispatchedV1, instr.ID.String(), instr.CorrelationID, instr.CausationID)
}

// PublishDelivered writes an instruction-delivered event to the outbox within the provided transaction.
func (p *InstructionEventPublisher) PublishDelivered(ctx context.Context, tx *gorm.DB, instr *domain.Instruction) error {
	event := &eventsv1.InstructionDeliveredEvent{
		EventId:              uuid.New().String(),
		InstructionId:        instr.ID.String(),
		TenantId:             instr.TenantID.String(),
		InstructionType:      instr.InstructionType,
		ProviderConnectionId: instr.ProviderConnectionID,
		CorrelationId:        instr.CorrelationID,
		CausationId:          instr.CausationID,
		OccurredAt:           timestamppb.New(time.Now()),
		Version:              instr.Version,
	}
	return p.publish(ctx, tx, event, topics.OperationalGatewayInstructionDeliveredV1, instr.ID.String(), instr.CorrelationID, instr.CausationID)
}

// PublishAcknowledged writes an instruction-acknowledged event to the outbox within the provided transaction.
func (p *InstructionEventPublisher) PublishAcknowledged(ctx context.Context, tx *gorm.DB, instr *domain.Instruction) error {
	event := &eventsv1.InstructionAcknowledgedEvent{
		EventId:              uuid.New().String(),
		InstructionId:        instr.ID.String(),
		TenantId:             instr.TenantID.String(),
		InstructionType:      instr.InstructionType,
		ProviderConnectionId: instr.ProviderConnectionID,
		CorrelationId:        instr.CorrelationID,
		CausationId:          instr.CausationID,
		OccurredAt:           timestamppb.New(time.Now()),
		Version:              instr.Version,
	}
	return p.publish(ctx, tx, event, topics.OperationalGatewayInstructionAcknowledgedV1, instr.ID.String(), instr.CorrelationID, instr.CausationID)
}

// PublishFailed writes an instruction-failed event to the outbox within the provided transaction.
func (p *InstructionEventPublisher) PublishFailed(ctx context.Context, tx *gorm.DB, instr *domain.Instruction) error {
	event := &eventsv1.InstructionFailedEvent{
		EventId:              uuid.New().String(),
		InstructionId:        instr.ID.String(),
		TenantId:             instr.TenantID.String(),
		InstructionType:      instr.InstructionType,
		ProviderConnectionId: instr.ProviderConnectionID,
		FailureReason:        instr.FailureReason,
		ErrorCode:            instr.ErrorCode,
		AttemptCount:         int32(instr.AttemptCount),
		CorrelationId:        instr.CorrelationID,
		CausationId:          instr.CausationID,
		OccurredAt:           timestamppb.New(time.Now()),
		Version:              instr.Version,
	}
	return p.publish(ctx, tx, event, topics.OperationalGatewayInstructionFailedV1, instr.ID.String(), instr.CorrelationID, instr.CausationID)
}

// PublishExpired writes an instruction-expired event to the outbox within the provided transaction.
func (p *InstructionEventPublisher) PublishExpired(ctx context.Context, tx *gorm.DB, instr *domain.Instruction) error {
	event := &eventsv1.InstructionExpiredEvent{
		EventId:              uuid.New().String(),
		InstructionId:        instr.ID.String(),
		TenantId:             instr.TenantID.String(),
		InstructionType:      instr.InstructionType,
		ProviderConnectionId: instr.ProviderConnectionID,
		CorrelationId:        instr.CorrelationID,
		CausationId:          instr.CausationID,
		OccurredAt:           timestamppb.New(time.Now()),
		Version:              instr.Version,
	}
	return p.publish(ctx, tx, event, topics.OperationalGatewayInstructionExpiredV1, instr.ID.String(), instr.CorrelationID, instr.CausationID)
}

// PublishCancelled writes an instruction-cancelled event to the outbox within the provided transaction.
func (p *InstructionEventPublisher) PublishCancelled(ctx context.Context, tx *gorm.DB, instr *domain.Instruction) error {
	event := &eventsv1.InstructionCancelledEvent{
		EventId:              uuid.New().String(),
		InstructionId:        instr.ID.String(),
		TenantId:             instr.TenantID.String(),
		InstructionType:      instr.InstructionType,
		ProviderConnectionId: instr.ProviderConnectionID,
		CorrelationId:        instr.CorrelationID,
		CausationId:          instr.CausationID,
		OccurredAt:           timestamppb.New(time.Now()),
		Version:              instr.Version,
	}
	return p.publish(ctx, tx, event, topics.OperationalGatewayInstructionCancelledV1, instr.ID.String(), instr.CorrelationID, instr.CausationID)
}

// publish is the internal helper that serializes and writes a proto event to the outbox.
func (p *InstructionEventPublisher) publish(
	ctx context.Context,
	tx *gorm.DB,
	event proto.Message,
	topic string,
	aggregateID string,
	correlationID string,
	causationID string,
) error {
	eventType, ok := topicToEventType[topic]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTopic, topic)
	}

	return p.publisher.Publish(ctx, tx, event, events.PublishConfig{
		EventType:     eventType,
		Topic:         topic,
		AggregateType: "Instruction",
		AggregateID:   aggregateID,
		PartitionKey:  aggregateID,
		CorrelationID: correlationID,
		CausationID:   causationID,
	})
}
