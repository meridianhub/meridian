package messaging

import (
	"context"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupPaymentOutboxTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&events.EventOutbox{})
	require.NoError(t, err)

	return db
}

func TestNewOutboxPublisher(t *testing.T) {
	t.Parallel()

	publisher := NewOutboxPublisher(nil, nil)
	assert.NotNil(t, publisher)
}

func TestTopicToEventType_AllMappingsExist(t *testing.T) {
	t.Parallel()

	// Verify all expected topics have mappings
	expectedTopics := []string{
		"payment-order.initiated.v1",
		"payment-order.reserved.v1",
		"payment-order.executing.v1",
		"payment-order.completed.v1",
		"payment-order.failed.v1",
		"payment-order.cancelled.v1",
		"payment-order.reversed.v1",
	}

	for _, topic := range expectedTopics {
		eventType, ok := topicToEventType[topic]
		require.True(t, ok, "expected topic %q to exist in topicToEventType map", topic)
		assert.NotEmpty(t, eventType, "event type should not be empty for topic %s", topic)
	}

	// Verify the map has 7 entries
	assert.Len(t, topicToEventType, 7)
}

func TestPaymentEventConsumer_NewPaymentEventConsumer_NilPanics(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		NewPaymentEventConsumer(nil)
	})
}

func TestPaymentEventConsumer_Start_NilConsumers(t *testing.T) {
	t.Parallel()

	consumer := &PaymentEventConsumer{
		svc: &stubUpdater{},
	}

	err := consumer.Start("topic-a", "topic-b")
	assert.ErrorIs(t, err, ErrConsumerNotConfigured)
}

func TestPaymentEventConsumer_Stop_NilConsumers(t *testing.T) {
	t.Parallel()

	consumer := &PaymentEventConsumer{
		svc: &stubUpdater{},
	}

	// Should not panic with nil consumers
	assert.NotPanics(t, func() {
		consumer.Stop()
	})
}

func TestPaymentEventConsumer_Close_NilConsumers(t *testing.T) {
	t.Parallel()

	consumer := &PaymentEventConsumer{
		svc: &stubUpdater{},
	}

	// Should not panic with nil consumers
	err := consumer.Close()
	assert.NoError(t, err)
}

func TestIdempotencyKey(t *testing.T) {
	t.Parallel()

	key := idempotencyKey("captured", "evt-123", "po-456")
	assert.Equal(t, "fg-event:captured:evt-123:po-456", key)

	key2 := idempotencyKey("failed", "evt-789", "po-012")
	assert.Equal(t, "fg-event:failed:evt-789:po-012", key2)

	// Different inputs produce different keys
	assert.NotEqual(t, key, key2)
}

func TestNewPaymentEventConsumerWithKafka_NilUpdater(t *testing.T) {
	t.Parallel()

	_, err := NewPaymentEventConsumerWithKafka(
		kafka.ConsumerConfig{},
		nil,
		nil,
	)
	assert.ErrorIs(t, err, ErrNilPaymentOrderUpdater)
}

// --- OutboxPublisher.Publish ---

func TestOutboxPublisher_Publish_UnsupportedTopic(t *testing.T) {
	t.Parallel()

	db := setupPaymentOutboxTestDB(t)
	p := NewOutboxPublisher(db, events.NewOutboxPublisher("payment-order"))

	msg := timestamppb.Now()
	err := p.Publish(context.Background(), "unknown.topic.v1", "key-1", msg)
	require.Error(t, err)
	assert.ErrorIs(t, err, errUnsupportedTopic)
}

func TestOutboxPublisher_Publish_WritesOutboxEntry(t *testing.T) {
	db := setupPaymentOutboxTestDB(t)
	p := NewOutboxPublisher(db, events.NewOutboxPublisher("payment-order"))

	// Use a simple proto message that has no validation constraints.
	msg := timestamppb.Now()
	orderID := "order-abc-123"

	err := p.Publish(context.Background(), topics.PaymentOrderInitiatedV1, orderID, msg)
	require.NoError(t, err)

	var entries []events.EventOutbox
	db.Find(&entries)
	require.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, topics.PaymentOrderInitiatedV1, entry.Topic)
	assert.Equal(t, "payment_order.initiated.v1", entry.EventType)
	assert.Equal(t, orderID, entry.AggregateID)
	assert.Equal(t, orderID, entry.PartitionKey)
	assert.Equal(t, "PaymentOrder", entry.AggregateType)
	assert.Equal(t, "payment-order", entry.ServiceName)
	assert.Equal(t, events.StatusPending, entry.Status)
}

func TestOutboxPublisher_Publish_AllTopics(t *testing.T) {
	allTopics := []struct {
		topic     string
		eventType string
	}{
		{topics.PaymentOrderInitiatedV1, "payment_order.initiated.v1"},
		{topics.PaymentOrderReservedV1, "payment_order.reserved.v1"},
		{topics.PaymentOrderExecutingV1, "payment_order.executing.v1"},
		{topics.PaymentOrderCompletedV1, "payment_order.completed.v1"},
		{topics.PaymentOrderFailedV1, "payment_order.failed.v1"},
		{topics.PaymentOrderCancelledV1, "payment_order.cancelled.v1"},
		{topics.PaymentOrderReversedV1, "payment_order.reversed.v1"},
	}

	for _, tc := range allTopics {
		t.Run(tc.topic, func(t *testing.T) {
			db := setupPaymentOutboxTestDB(t)
			p := NewOutboxPublisher(db, events.NewOutboxPublisher("payment-order"))

			msg := timestamppb.Now()
			err := p.Publish(context.Background(), tc.topic, "order-1", msg)
			require.NoError(t, err)

			var entries []events.EventOutbox
			db.Where("topic = ?", tc.topic).Find(&entries)
			require.Len(t, entries, 1)
			assert.Equal(t, tc.eventType, entries[0].EventType)
		})
	}
}

func TestOutboxPublisher_Publish_DBError(t *testing.T) {
	db := setupPaymentOutboxTestDB(t)
	p := NewOutboxPublisher(db, events.NewOutboxPublisher("payment-order"))

	db.Exec("DROP TABLE event_outbox")

	msg := timestamppb.Now()
	err := p.Publish(context.Background(), topics.PaymentOrderCompletedV1, "order-1", msg)
	require.Error(t, err)
}

// stubUpdater is a minimal stub for PaymentOrderUpdater used in unit tests
// that don't need to verify call behavior.
type stubUpdater struct{}

func (s *stubUpdater) UpdatePaymentOrder(_ context.Context, _ *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
	return nil, nil
}
