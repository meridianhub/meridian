// Package messaging provides event publishing for the reconciliation service.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// Errors for the outbox event publisher.
var (
	errUnsupportedTopic = errors.New("unsupported topic")
	errUnexpectedType   = errors.New("unexpected event type")
)

// OutboxEventPublisher publishes reconciliation domain events through the transactional outbox pattern.
// It implements the service.EventPublisher interface by converting JSON-serializable Go structs
// to protobuf event messages and writing them to the outbox table.
//
// Since reconciliation uses GORM for persistence, outbox entries are written in a separate
// GORM transaction. This provides at-least-once delivery via the outbox worker.
type OutboxEventPublisher struct {
	db        *gorm.DB
	publisher *events.OutboxPublisher
}

// NewOutboxEventPublisher creates a new outbox-based event publisher for reconciliation.
func NewOutboxEventPublisher(db *gorm.DB, publisher *events.OutboxPublisher) *OutboxEventPublisher {
	return &OutboxEventPublisher{
		db:        db,
		publisher: publisher,
	}
}

// Publish implements the service.EventPublisher interface.
// It maps the topic and event to a protobuf message and writes it to the outbox.
func (p *OutboxEventPublisher) Publish(ctx context.Context, topic string, event interface{}) error {
	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		switch topic {
		case TopicDisputeCreated:
			return p.publishDisputeCreated(ctx, tx, event)
		case TopicDisputeResolved:
			return p.publishDisputeResolved(ctx, tx, event)
		case TopicPositionLockRequested:
			return p.publishPositionLockRequested(ctx, tx, event)
		default:
			return fmt.Errorf("%w: %s", errUnsupportedTopic, topic)
		}
	})
}

// Close is a no-op for the outbox publisher (the outbox worker handles Kafka lifecycle).
func (p *OutboxEventPublisher) Close() {}

func (p *OutboxEventPublisher) publishDisputeCreated(ctx context.Context, tx *gorm.DB, event interface{}) error {
	e, ok := event.(disputeCreatedEvent)
	if !ok {
		return fmt.Errorf("%w: expected DisputeCreatedEvent, got %T", errUnexpectedType, event)
	}

	protoEvent := &eventsv1.DisputeCreatedEvent{
		DisputeId:     e.GetDisputeID(),
		VarianceId:    e.GetVarianceID(),
		RunId:         e.GetRunID(),
		AccountId:     e.GetAccountID(),
		Reason:        e.GetReason(),
		RaisedBy:      e.GetRaisedBy(),
		CorrelationId: e.GetDisputeID(),
		Timestamp:     timestamppb.Now(),
	}

	return p.publisher.Publish(ctx, tx, protoEvent, events.PublishConfig{
		EventType:     "reconciliation.dispute_created.v1",
		Topic:         topics.ReconciliationDisputeCreatedV1,
		AggregateType: "Dispute",
		AggregateID:   e.GetDisputeID(),
		PartitionKey:  e.GetAccountID(),
	})
}

func (p *OutboxEventPublisher) publishDisputeResolved(ctx context.Context, tx *gorm.DB, event interface{}) error {
	e, ok := event.(disputeResolvedEvent)
	if !ok {
		return fmt.Errorf("%w: expected DisputeResolvedEvent, got %T", errUnexpectedType, event)
	}

	outcome := e.GetAction()
	protoEvent := &eventsv1.DisputeResolvedEvent{
		DisputeId:     e.GetDisputeID(),
		VarianceId:    e.GetVarianceID(),
		AccountId:     e.GetAccountID(),
		Outcome:       outcome,
		Resolution:    e.GetResolution(),
		ResolvedBy:    e.GetResolvedBy(),
		CorrelationId: e.GetDisputeID(),
		Timestamp:     timestamppb.Now(),
	}

	return p.publisher.Publish(ctx, tx, protoEvent, events.PublishConfig{
		EventType:     "reconciliation.dispute_resolved.v1",
		Topic:         topics.ReconciliationDisputeResolvedV1,
		AggregateType: "Dispute",
		AggregateID:   e.GetDisputeID(),
		PartitionKey:  e.GetAccountID(),
	})
}

func (p *OutboxEventPublisher) publishPositionLockRequested(ctx context.Context, tx *gorm.DB, event interface{}) error {
	e, ok := event.(positionLockRequestedEvent)
	if !ok {
		return fmt.Errorf("%w: expected PositionLockRequestedEvent, got %T", errUnexpectedType, event)
	}

	// Parse period timestamps
	periodStart, _ := time.Parse(time.RFC3339, e.GetPeriodStart())
	periodEnd, _ := time.Parse(time.RFC3339, e.GetPeriodEnd())

	protoEvent := &eventsv1.PositionLockRequestedEvent{
		RunId:               e.GetRunID(),
		AccountId:           e.GetAccountID(),
		LockDurationSeconds: 300, // Default lock duration
		CorrelationId:       e.GetRunID(),
		Timestamp:           timestamppb.Now(),
	}
	if !periodStart.IsZero() {
		_ = periodStart // period info carried in correlation context
	}
	if !periodEnd.IsZero() {
		_ = periodEnd
	}

	return p.publisher.Publish(ctx, tx, protoEvent, events.PublishConfig{
		EventType:     "reconciliation.position_lock_requested.v1",
		Topic:         topics.ReconciliationPositionLockRequestedV1,
		AggregateType: "SettlementRun",
		AggregateID:   e.GetRunID(),
		PartitionKey:  e.GetAccountID(),
	})
}

// Interface adapters to decouple from concrete event struct types in the service package.
// These interfaces match the fields on the Go structs used by the service layer.

type disputeCreatedEvent interface {
	GetDisputeID() string
	GetVarianceID() string
	GetRunID() string
	GetAccountID() string
	GetReason() string
	GetRaisedBy() string
}

type disputeResolvedEvent interface {
	GetDisputeID() string
	GetVarianceID() string
	GetRunID() string
	GetAccountID() string
	GetAction() string
	GetResolution() string
	GetResolvedBy() string
}

type positionLockRequestedEvent interface {
	GetRunID() string
	GetAccountID() string
	GetScope() string
	GetPeriodStart() string
	GetPeriodEnd() string
	GetStatus() string
}
