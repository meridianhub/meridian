package messaging_test

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOutboxEventPublisher_NilRepo(t *testing.T) {
	publisher, err := messaging.NewOutboxEventPublisher(nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrNilProducer)
	assert.Nil(t, publisher)
}

func TestNewOutboxEventPublisher_Success(t *testing.T) {
	repo := events.NewPgxOutboxRepository(nil)
	publisher, err := messaging.NewOutboxEventPublisher(repo)
	require.NoError(t, err)
	assert.NotNil(t, publisher)
}

func TestOutboxEventPublisher_Publish_ReturnsNotSupported(t *testing.T) {
	repo := events.NewPgxOutboxRepository(nil)
	publisher, err := messaging.NewOutboxEventPublisher(repo)
	require.NoError(t, err)

	event := &mockDomainEvent{
		eventType:   "position_keeping.transaction_captured.v1",
		aggregateID: "test-id",
		occurredAt:  time.Now().UTC(),
	}

	err = publisher.Publish(context.Background(), event)
	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrOutboxPublishNotSupported)
}

func TestOutboxEventPublisher_PublishBatch_ReturnsNotSupported(t *testing.T) {
	repo := events.NewPgxOutboxRepository(nil)
	publisher, err := messaging.NewOutboxEventPublisher(repo)
	require.NoError(t, err)

	evts := []domain.DomainEvent{
		&mockDomainEvent{
			eventType:   "position_keeping.transaction_captured.v1",
			aggregateID: "test-id",
			occurredAt:  time.Now().UTC(),
		},
	}

	err = publisher.PublishBatch(context.Background(), evts)
	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrOutboxPublishNotSupported)
}

func TestOutboxEventPublisher_BuildOutboxFn_NilEvent(t *testing.T) {
	repo := events.NewPgxOutboxRepository(nil)
	publisher, err := messaging.NewOutboxEventPublisher(repo)
	require.NoError(t, err)

	fn := publisher.BuildOutboxFn(context.Background(), []domain.DomainEvent{nil})
	err = fn(nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrNilEvent)
	assert.Contains(t, err.Error(), "failed to write event at index 0 to outbox")
}

func TestOutboxEventPublisher_BuildOutboxFn_UnknownEventType(t *testing.T) {
	repo := events.NewPgxOutboxRepository(nil)
	publisher, err := messaging.NewOutboxEventPublisher(repo)
	require.NoError(t, err)

	unknownEvent := &mockDomainEvent{
		eventType:   "unknown.event.type",
		aggregateID: "test-id",
		occurredAt:  time.Now().UTC(),
	}

	fn := publisher.BuildOutboxFn(context.Background(), []domain.DomainEvent{unknownEvent})
	err = fn(nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrUnknownEventType)
}

func TestOutboxEventPublisher_BuildOutboxFn_InvalidProtoEvent(t *testing.T) {
	repo := events.NewPgxOutboxRepository(nil)
	publisher, err := messaging.NewOutboxEventPublisher(repo)
	require.NoError(t, err)

	// mockDomainEvent returns nil from ToProto(), which doesn't implement proto.Message
	invalidEvent := &mockDomainEvent{
		eventType:   "position_keeping.transaction_captured.v1",
		aggregateID: "test-id",
		occurredAt:  time.Now().UTC(),
	}

	fn := publisher.BuildOutboxFn(context.Background(), []domain.DomainEvent{invalidEvent})
	err = fn(nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrInvalidProtoEvent)
}

func TestOutboxEventPublisher_BuildOutboxFn_EmptyEvents(t *testing.T) {
	repo := events.NewPgxOutboxRepository(nil)
	publisher, err := messaging.NewOutboxEventPublisher(repo)
	require.NoError(t, err)

	fn := publisher.BuildOutboxFn(context.Background(), []domain.DomainEvent{})
	err = fn(nil)
	assert.NoError(t, err)
}

func TestOutboxEventPublisher_ErrorVariables(t *testing.T) {
	assert.Equal(t, "OutboxEventPublisher requires a transaction: use BuildOutboxFn with CreateWithOutbox or UpdateWithOutbox", messaging.ErrOutboxPublishNotSupported.Error())
}

func TestKafkaEventPublisher_PublishBatch_EmptySlice(t *testing.T) {
	mock := &mockProtoPublisher{}
	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(mock, topicConfig)
	require.NoError(t, err)

	ctx := context.Background()
	err = publisher.PublishBatch(ctx, nil)
	assert.NoError(t, err)
	assert.Len(t, mock.publishedMessages, 0)
}

func TestKafkaEventPublisher_ErrorVariables(t *testing.T) {
	assert.Equal(t, "kafka producer cannot be nil", messaging.ErrNilProducer.Error())
	assert.Equal(t, "domain event cannot be nil", messaging.ErrNilEvent.Error())
	assert.Equal(t, "event does not implement proto.Message conversion", messaging.ErrInvalidProtoEvent.Error())
	assert.Equal(t, "unknown event type", messaging.ErrUnknownEventType.Error())
}

func TestKafkaEventPublisher_Publish_InvalidProtoEvent(t *testing.T) {
	// mockDomainEvent returns nil from ToProto(), which won't implement proto.Message
	invalidProtoEvent := &mockDomainEvent{
		eventType:   "position_keeping.transaction_captured.v1", // Valid event type
		aggregateID: "test-id",
		occurredAt:  time.Now().UTC(),
	}

	mock := &mockProtoPublisher{}
	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(mock, topicConfig)
	require.NoError(t, err)

	ctx := testOrgContext()
	err = publisher.Publish(ctx, invalidProtoEvent)

	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrInvalidProtoEvent)
	assert.Len(t, mock.publishedMessages, 0)
}

func TestKafkaEventPublisher_PublishBatch_FailsOnBadEvent(t *testing.T) {
	// Event with valid type but nil ToProto result
	badEvent := &mockDomainEvent{
		eventType:   "position_keeping.transaction_captured.v1",
		aggregateID: "test-id",
		occurredAt:  time.Now().UTC(),
	}

	mock := &mockProtoPublisher{}
	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(mock, topicConfig)
	require.NoError(t, err)

	ctx := testOrgContext()
	events := []domain.DomainEvent{badEvent}
	err = publisher.PublishBatch(ctx, events)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to publish event at index 0")
}

func TestKafkaEventPublisher_PublishBatch_FailsOnUnknownEventInBatch(t *testing.T) {
	unknownEvent := &mockDomainEvent{
		eventType:   "unknown.event.type",
		aggregateID: "test-id",
		occurredAt:  time.Now().UTC(),
	}

	mock := &mockProtoPublisher{}
	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(mock, topicConfig)
	require.NoError(t, err)

	ctx := testOrgContext()
	events := []domain.DomainEvent{unknownEvent}
	err = publisher.PublishBatch(ctx, events)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to publish event at index 0")
}
