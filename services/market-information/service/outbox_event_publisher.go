// Package service provides application services for the Market Information service.
package service

import (
	"context"
	"fmt"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"gorm.io/gorm"
)

// OutboxEventPublisher publishes observation domain events through the transactional outbox pattern.
// It implements both EventPublisher and ObservationEventPublisher interfaces.
//
// Since market-information uses pgxpool for domain persistence (not GORM), outbox entries are
// written in a separate GORM transaction. This provides at-least-once delivery via the outbox
// worker, replacing the previous fire-and-forget Kafka publishing.
type OutboxEventPublisher struct {
	db        *gorm.DB
	publisher *events.OutboxPublisher
}

// NewOutboxEventPublisher creates a new outbox-based event publisher.
func NewOutboxEventPublisher(db *gorm.DB, publisher *events.OutboxPublisher) *OutboxEventPublisher {
	return &OutboxEventPublisher{
		db:        db,
		publisher: publisher,
	}
}

// PublishObservationRecorded publishes an ObservationRecorded event through the outbox.
func (p *OutboxEventPublisher) PublishObservationRecorded(
	ctx context.Context,
	observation domain.MarketPriceObservation,
) error {
	event := mapObservationToProtoEvent(observation)
	partitionKey := observation.DataSetCode()

	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return p.publisher.Publish(ctx, tx, event, events.PublishConfig{
			EventType:     "market_information.observation_recorded.v1",
			Topic:         topics.MarketInformationObservationRecordedV1,
			AggregateType: "MarketPriceObservation",
			AggregateID:   observation.ID().String(),
			PartitionKey:  partitionKey,
		})
	})
}

// Publish implements the EventPublisher interface by type-switching on the event.
func (p *OutboxEventPublisher) Publish(ctx context.Context, event any) error {
	switch e := event.(type) {
	case *marketinformationv1.ObservationRecorded:
		partitionKey := e.DatasetCode
		return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return p.publisher.Publish(ctx, tx, e, events.PublishConfig{
				EventType:     "market_information.observation_recorded.v1",
				Topic:         topics.MarketInformationObservationRecordedV1,
				AggregateType: "MarketPriceObservation",
				AggregateID:   e.ObservationId,
				PartitionKey:  partitionKey,
			})
		})
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedEventType, event)
	}
}
