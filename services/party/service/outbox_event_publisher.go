package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// OutboxVerificationEventPublisher implements VerificationEventPublisher using the outbox pattern.
// Events are written to the database outbox table and published asynchronously to Kafka.
type OutboxVerificationEventPublisher struct {
	publisher *events.OutboxPublisher
	db        *gorm.DB
}

// NewOutboxVerificationEventPublisher creates a publisher that writes verification events
// to the outbox using the provided publisher and raw database connection.
func NewOutboxVerificationEventPublisher(publisher *events.OutboxPublisher, db *gorm.DB) *OutboxVerificationEventPublisher {
	return &OutboxVerificationEventPublisher{
		publisher: publisher,
		db:        db,
	}
}

// PublishVerificationCompleted writes a PartyVerificationCompletedEvent to the outbox.
// The event is written in its own transaction since the verification status update is
// performed separately and the outbox guarantees at-least-once delivery.
func (p *OutboxVerificationEventPublisher) PublishVerificationCompleted(ctx context.Context, e VerificationCompletedEvent) error {
	proto := &eventsv1.PartyVerificationCompletedEvent{
		EventId:        e.EventID,
		PartyId:        e.PartyID,
		VerificationId: e.VerificationID,
		Provider:       e.Provider,
		Status:         e.Status,
		CompletedAt:    timestamppb.New(e.CompletedAt),
		Timestamp:      timestamppb.New(time.Now().UTC()),
		Metadata:       e.Metadata,
	}

	if e.RiskScore != nil {
		proto.RiskScore = e.RiskScore
	}
	if e.Reason != nil {
		proto.Reason = e.Reason
	}

	correlationID := uuid.New().String()

	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return p.publisher.Publish(ctx, tx, proto, events.PublishConfig{
			EventType:     "party.verification-completed.v1",
			AggregateID:   e.PartyID,
			AggregateType: "Party",
			Topic:         topics.PartyVerificationCompletedV1,
			CorrelationID: correlationID,
		})
	})
}
