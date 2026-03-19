package messaging

import (
	"context"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// stubUpdater is a minimal stub for PaymentOrderUpdater used in unit tests
// that don't need to verify call behavior.
type stubUpdater struct{}

func (s *stubUpdater) UpdatePaymentOrder(_ context.Context, _ *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
	return nil, nil
}
