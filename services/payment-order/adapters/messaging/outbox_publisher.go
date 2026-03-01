// Package messaging provides event publishing for the payment order service.
package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// errUnsupportedTopic is returned when a topic has no mapping in the outbox publisher.
var errUnsupportedTopic = errors.New("unsupported topic for outbox publishing")

// topicToEventType maps Kafka topic constants to outbox event type strings.
var topicToEventType = map[string]string{
	topics.PaymentOrderInitiatedV1: "payment_order.initiated.v1",
	topics.PaymentOrderReservedV1:  "payment_order.reserved.v1",
	topics.PaymentOrderExecutingV1: "payment_order.executing.v1",
	topics.PaymentOrderCompletedV1: "payment_order.completed.v1",
	topics.PaymentOrderFailedV1:    "payment_order.failed.v1",
	topics.PaymentOrderCancelledV1: "payment_order.cancelled.v1",
	topics.PaymentOrderReversedV1:  "payment_order.reversed.v1",
}

// OutboxPublisher implements the service.KafkaPublisher interface by writing proto events
// to the transactional outbox table instead of publishing directly to Kafka.
// The outbox worker handles reliable delivery to Kafka asynchronously.
type OutboxPublisher struct {
	db        *gorm.DB
	publisher *events.OutboxPublisher
}

// NewOutboxPublisher creates a new outbox-based event publisher for payment orders.
func NewOutboxPublisher(db *gorm.DB, publisher *events.OutboxPublisher) *OutboxPublisher {
	return &OutboxPublisher{
		db:        db,
		publisher: publisher,
	}
}

// Publish implements service.KafkaPublisher by writing the proto event to the outbox table.
// The key parameter is used as both the aggregate ID and partition key (payment order ID).
func (p *OutboxPublisher) Publish(ctx context.Context, topic string, key string, msg proto.Message) error {
	eventType, ok := topicToEventType[topic]
	if !ok {
		return fmt.Errorf("%w: %s", errUnsupportedTopic, topic)
	}

	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return p.publisher.Publish(ctx, tx, msg, events.PublishConfig{
			EventType:     eventType,
			Topic:         topic,
			AggregateType: "PaymentOrder",
			AggregateID:   key,
			PartitionKey:  key,
		})
	})
}
