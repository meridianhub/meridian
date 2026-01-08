package messaging_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// mockProtoPublisher is a mock implementation of protoPublisher for testing
type mockProtoPublisher struct {
	publishedMessages []publishedMessage
	flushCount        int
	closed            bool
}

type publishedMessage struct {
	topic string
	key   string
	msg   proto.Message
}

func (m *mockProtoPublisher) PublishWithTenant(_ context.Context, topic, key string, msg proto.Message) error {
	m.publishedMessages = append(m.publishedMessages, publishedMessage{
		topic: topic,
		key:   key,
		msg:   msg,
	})
	return nil
}

func (m *mockProtoPublisher) FlushWithTimeout(_ int) int {
	m.flushCount++
	return 0
}

func (m *mockProtoPublisher) Close() {
	m.closed = true
}

// testOrgContext creates a context with organization for testing
func testOrgContext() context.Context {
	orgID := tenant.MustNewTenantID("test_org")
	return tenant.WithTenant(context.Background(), orgID)
}

func TestDefaultTopicConfig(t *testing.T) {
	config := messaging.DefaultTopicConfig()

	assert.Equal(t, "position-keeping.transaction-captured.v1", config.TransactionCapturedTopic)
	assert.Equal(t, "position-keeping.transaction-amended.v1", config.TransactionAmendedTopic)
	assert.Equal(t, "position-keeping.transaction-reconciled.v1", config.TransactionReconciledTopic)
	assert.Equal(t, "position-keeping.transaction-posted.v1", config.TransactionPostedTopic)
	assert.Equal(t, "position-keeping.transaction-rejected.v1", config.TransactionRejectedTopic)
	assert.Equal(t, "position-keeping.transaction-failed.v1", config.TransactionFailedTopic)
	assert.Equal(t, "position-keeping.transaction-cancelled.v1", config.TransactionCancelledTopic)
	assert.Equal(t, "position-keeping.bulk-transaction-captured.v1", config.BulkTransactionCapturedTopic)
}

func TestNewKafkaEventPublisher_NilProducer(t *testing.T) {
	config := messaging.DefaultTopicConfig()

	publisher, err := messaging.NewKafkaEventPublisher(nil, config)
	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrNilProducer)
	assert.Nil(t, publisher)
}

func TestNewKafkaEventPublisher_Success(t *testing.T) {
	// Create a real producer for initialization test
	// Note: This requires actual Kafka connection in integration tests
	// For unit tests, we would need to mock the producer
	t.Skip("Requires Kafka connection - convert to integration test")

	producerConfig := kafka.ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-publisher",
	}

	producer, err := kafka.NewProtoProducer(producerConfig)
	require.NoError(t, err)
	defer producer.Close()

	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(producer, topicConfig)
	require.NoError(t, err)
	assert.NotNil(t, publisher)
}

func TestKafkaEventPublisher_Publish_NilEvent(t *testing.T) {
	t.Skip("Requires Kafka connection - convert to integration test")

	producerConfig := kafka.ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-publisher",
	}

	producer, err := kafka.NewProtoProducer(producerConfig)
	require.NoError(t, err)
	defer producer.Close()

	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(producer, topicConfig)
	require.NoError(t, err)

	ctx := context.Background()
	err = publisher.Publish(ctx, nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrNilEvent)
}

// Integration test demonstrating event publishing
func TestKafkaEventPublisher_Publish_Integration(t *testing.T) {
	t.Skip("Integration test - requires running Kafka")

	// Setup Kafka producer
	producerConfig := kafka.ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-publisher",
	}

	producer, err := kafka.NewProtoProducer(producerConfig)
	require.NoError(t, err)
	defer producer.Close()

	// Create publisher
	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(producer, topicConfig)
	require.NoError(t, err)
	defer publisher.Close()

	// Create test event
	money, err := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	require.NoError(t, err)

	event := &domain.TransactionCaptured{
		LogID:         uuid.New(),
		AccountID:     "ACC-123",
		TransactionID: uuid.New(),
		Amount:        money,
		Direction:     domain.PostingDirectionDebit,
		Source:        domain.TransactionSourceAutomated,
		Description:   "Test transaction",
		Reference:     "REF-001",
		CorrelationID: "CORR-123",
		Timestamp:     time.Now().UTC(),
		Version:       1,
	}

	// Publish event
	ctx := context.Background()
	err = publisher.Publish(ctx, event)
	require.NoError(t, err)

	// Flush to ensure delivery
	remaining := publisher.FlushWithTimeout(5000)
	assert.Equal(t, 0, remaining, "all messages should be delivered")
}

// Integration test demonstrating batch publishing
func TestKafkaEventPublisher_PublishBatch_Integration(t *testing.T) {
	t.Skip("Integration test - requires running Kafka")

	// Setup Kafka producer
	producerConfig := kafka.ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-publisher",
	}

	producer, err := kafka.NewProtoProducer(producerConfig)
	require.NoError(t, err)
	defer producer.Close()

	// Create publisher
	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(producer, topicConfig)
	require.NoError(t, err)
	defer publisher.Close()

	// Create test events
	logID1 := uuid.New()
	logID2 := uuid.New()

	events := []domain.DomainEvent{
		&domain.TransactionCaptured{
			LogID:         logID1,
			AccountID:     "ACC-123",
			TransactionID: uuid.New(),
			CorrelationID: "CORR-123",
			Timestamp:     time.Now().UTC(),
			Version:       1,
		},
		&domain.TransactionReconciled{
			LogID:                logID2,
			AccountID:            "ACC-456",
			ReconciliationStatus: domain.ReconciliationStatusMatched,
			Reason:               "Auto reconciled",
			ReconciledBy:         "system",
			CorrelationID:        "CORR-124",
			Timestamp:            time.Now().UTC(),
			Version:              2,
		},
	}

	// Publish batch
	ctx := context.Background()
	err = publisher.PublishBatch(ctx, events)
	require.NoError(t, err)

	// Flush to ensure delivery
	remaining := publisher.FlushWithTimeout(5000)
	assert.Equal(t, 0, remaining, "all messages should be delivered")
}

// Test demonstrating all event types map to correct topics
func TestKafkaEventPublisher_TopicMapping(t *testing.T) {
	logID := uuid.New()
	batchID := uuid.New()
	timestamp := time.Now().UTC()

	// Create Money for TransactionCaptured
	money, err := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	require.NoError(t, err)

	tests := []struct {
		name          string
		event         domain.DomainEvent
		expectedTopic string
	}{
		{
			name: "TransactionCaptured",
			event: &domain.TransactionCaptured{
				LogID:         logID,
				AccountID:     "ACC-123",
				TransactionID: uuid.New(),
				Amount:        money,
				Direction:     domain.PostingDirectionDebit,
				Source:        domain.TransactionSourceAutomated,
				Description:   "Test transaction",
				Reference:     "REF-001",
				CorrelationID: "CORR-123",
				Timestamp:     timestamp,
				Version:       1,
			},
			expectedTopic: "position-keeping.transaction-captured.v1",
		},
		{
			name: "TransactionAmended",
			event: &domain.TransactionAmended{
				LogID:     logID,
				AccountID: "ACC-123",
				Timestamp: timestamp,
				Version:   2,
			},
			expectedTopic: "position-keeping.transaction-amended.v1",
		},
		{
			name: "TransactionReconciled",
			event: &domain.TransactionReconciled{
				LogID:     logID,
				AccountID: "ACC-123",
				Timestamp: timestamp,
				Version:   2,
			},
			expectedTopic: "position-keeping.transaction-reconciled.v1",
		},
		{
			name: "TransactionPosted",
			event: &domain.TransactionPosted{
				LogID:     logID,
				AccountID: "ACC-123",
				Timestamp: timestamp,
				Version:   3,
			},
			expectedTopic: "position-keeping.transaction-posted.v1",
		},
		{
			name: "TransactionRejected",
			event: &domain.TransactionRejected{
				LogID:     logID,
				AccountID: "ACC-123",
				Timestamp: timestamp,
				Version:   1,
			},
			expectedTopic: "position-keeping.transaction-rejected.v1",
		},
		{
			name: "TransactionFailed",
			event: &domain.TransactionFailed{
				LogID:     logID,
				AccountID: "ACC-123",
				Timestamp: timestamp,
				Version:   1,
			},
			expectedTopic: "position-keeping.transaction-failed.v1",
		},
		{
			name: "TransactionCancelled",
			event: &domain.TransactionCancelled{
				LogID:     logID,
				AccountID: "ACC-123",
				Timestamp: timestamp,
				Version:   1,
			},
			expectedTopic: "position-keeping.transaction-cancelled.v1",
		},
		{
			name: "BulkTransactionCaptured",
			event: &domain.BulkTransactionCaptured{
				BatchID:   batchID,
				Timestamp: timestamp,
			},
			expectedTopic: "position-keeping.bulk-transaction-captured.v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock producer
			mock := &mockProtoPublisher{}

			// Create publisher with default topic config
			topicConfig := messaging.DefaultTopicConfig()
			publisher, err := messaging.NewKafkaEventPublisher(mock, topicConfig)
			require.NoError(t, err)

			// Publish event with tenant context
			ctx := testOrgContext()
			err = publisher.Publish(ctx, tt.event)
			require.NoError(t, err)

			// Verify the event was published to the correct topic
			require.Len(t, mock.publishedMessages, 1, "should have published exactly one message")
			assert.Equal(t, tt.expectedTopic, mock.publishedMessages[0].topic, "event should be routed to correct topic")

			// Verify partition key is the aggregate ID
			expectedKey := tt.event.AggregateID()
			assert.Equal(t, expectedKey, mock.publishedMessages[0].key, "partition key should be aggregate ID")
		})
	}
}

func TestKafkaEventPublisher_UnknownEventType(t *testing.T) {
	// Create a mock event with unknown type
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
	err = publisher.Publish(ctx, unknownEvent)

	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrUnknownEventType)
	assert.Len(t, mock.publishedMessages, 0, "should not publish unknown event types")
}

func TestKafkaEventPublisher_NilEvent(t *testing.T) {
	mock := &mockProtoPublisher{}
	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(mock, topicConfig)
	require.NoError(t, err)

	ctx := testOrgContext()
	err = publisher.Publish(ctx, nil)

	assert.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrNilEvent)
}

func TestKafkaEventPublisher_PublishBatch(t *testing.T) {
	logID1 := uuid.New()
	logID2 := uuid.New()
	timestamp := time.Now().UTC()

	// Create Money for TransactionCaptured
	money, err := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	require.NoError(t, err)

	events := []domain.DomainEvent{
		&domain.TransactionCaptured{
			LogID:         logID1,
			AccountID:     "ACC-123",
			TransactionID: uuid.New(),
			Amount:        money,
			Direction:     domain.PostingDirectionDebit,
			Source:        domain.TransactionSourceAutomated,
			Description:   "Test transaction",
			Reference:     "REF-001",
			CorrelationID: "CORR-123",
			Timestamp:     timestamp,
			Version:       1,
		},
		&domain.TransactionAmended{
			LogID:     logID2,
			AccountID: "ACC-456",
			Timestamp: timestamp,
			Version:   2,
		},
	}

	mock := &mockProtoPublisher{}
	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(mock, topicConfig)
	require.NoError(t, err)

	ctx := testOrgContext()
	err = publisher.PublishBatch(ctx, events)
	require.NoError(t, err)

	// Verify both events were published
	require.Len(t, mock.publishedMessages, 2)
	assert.Equal(t, "position-keeping.transaction-captured.v1", mock.publishedMessages[0].topic)
	assert.Equal(t, "position-keeping.transaction-amended.v1", mock.publishedMessages[1].topic)
}

func TestKafkaEventPublisher_FlushAndClose(t *testing.T) {
	mock := &mockProtoPublisher{}
	topicConfig := messaging.DefaultTopicConfig()
	publisher, err := messaging.NewKafkaEventPublisher(mock, topicConfig)
	require.NoError(t, err)

	// Test Flush
	remaining := publisher.FlushWithTimeout(5000)
	assert.Equal(t, 0, remaining)
	assert.Equal(t, 1, mock.flushCount)

	// Test Close
	publisher.Close()
	assert.True(t, mock.closed)
}

// mockDomainEvent is a test helper for unknown event types
type mockDomainEvent struct {
	eventType   string
	aggregateID string
	occurredAt  time.Time
}

func (m *mockDomainEvent) EventType() string {
	return m.eventType
}

func (m *mockDomainEvent) AggregateID() string {
	return m.aggregateID
}

func (m *mockDomainEvent) OccurredAt() time.Time {
	return m.occurredAt
}

func (m *mockDomainEvent) ToProto() interface{} {
	return nil
}
