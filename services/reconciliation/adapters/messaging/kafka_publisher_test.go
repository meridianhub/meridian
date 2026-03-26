package messaging

import (
	"context"
	"log/slog"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeprecatedTopicFor(t *testing.T) {
	tests := []struct {
		name     string
		topic    string
		expected string
	}{
		{"run started", TopicReconciliationRunStarted, DeprecatedTopicReconciliationRunStarted},
		{"run completed", TopicReconciliationRunCompleted, DeprecatedTopicReconciliationRunCompleted},
		{"variance detected", TopicVarianceDetected, DeprecatedTopicVarianceDetected},
		{"position lock", TopicPositionLockRequested, DeprecatedTopicPositionLockRequested},
		{"dispute created", TopicDisputeCreated, DeprecatedTopicDisputeCreated},
		{"dispute resolved", TopicDisputeResolved, DeprecatedTopicDisputeResolved},
		{"unknown topic", "some.unknown.topic", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deprecatedTopicFor(tt.topic)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTopicAliasesMatchRegistry(t *testing.T) {
	assert.Equal(t, topics.ReconciliationRunStartedV1, TopicReconciliationRunStarted)
	assert.Equal(t, topics.ReconciliationRunCompletedV1, TopicReconciliationRunCompleted)
	assert.Equal(t, topics.ReconciliationVarianceDetectedV1, TopicVarianceDetected)
	assert.Equal(t, topics.ReconciliationPositionLockRequestedV1, TopicPositionLockRequested)
	assert.Equal(t, topics.ReconciliationDisputeCreatedV1, TopicDisputeCreated)
	assert.Equal(t, topics.ReconciliationDisputeResolvedV1, TopicDisputeResolved)
}

type hasAccountIDImpl struct {
	accountID string
}

func (h hasAccountIDImpl) GetAccountID() string { return h.accountID }

func TestExtractPartitionKey(t *testing.T) {
	t.Run("event implementing GetAccountID", func(t *testing.T) {
		event := hasAccountIDImpl{accountID: "ACC-123"}
		key := extractPartitionKey(event)
		assert.Equal(t, "ACC-123", key)
	})

	t.Run("struct with account_id field", func(t *testing.T) {
		event := struct {
			AccountID string `json:"account_id"`
			RunID     string `json:"run_id"`
		}{AccountID: "ACC-456", RunID: "RUN-789"}
		key := extractPartitionKey(event)
		assert.Equal(t, "ACC-456", key)
	})

	t.Run("struct with run_id fallback", func(t *testing.T) {
		event := struct {
			RunID string `json:"run_id"`
		}{RunID: "RUN-789"}
		key := extractPartitionKey(event)
		assert.Equal(t, "RUN-789", key)
	})

	t.Run("struct with no extractable key", func(t *testing.T) {
		event := struct {
			Name string `json:"name"`
		}{Name: "test"}
		key := extractPartitionKey(event)
		assert.Equal(t, "", key)
	})

	t.Run("nil event", func(t *testing.T) {
		key := extractPartitionKey(nil)
		assert.Equal(t, "", key)
	})

	t.Run("map with account_id", func(t *testing.T) {
		event := map[string]interface{}{
			"account_id": "ACC-MAP",
			"run_id":     "RUN-MAP",
		}
		key := extractPartitionKey(event)
		assert.Equal(t, "ACC-MAP", key)
	})

	t.Run("non-string account_id ignored", func(t *testing.T) {
		event := map[string]interface{}{
			"account_id": 12345,
			"run_id":     "RUN-FALLBACK",
		}
		key := extractPartitionKey(event)
		assert.Equal(t, "RUN-FALLBACK", key)
	})
}

func TestNoopPublisher_Publish(t *testing.T) {
	publisher := NewNoopPublisher(nil)
	require.NotNil(t, publisher)

	err := publisher.Publish(context.Background(), "test.topic", map[string]string{"key": "value"})
	assert.NoError(t, err)
}

func TestNoopPublisher_WithNilLogger(t *testing.T) {
	publisher := NewNoopPublisher(nil)
	require.NotNil(t, publisher)
	assert.NotNil(t, publisher.logger)
}

func TestNewKafkaPublisher_EmptyBrokers(t *testing.T) {
	_, err := NewKafkaPublisher("", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kafka producer")
}

func TestNewKafkaPublisher_ValidBrokers(t *testing.T) {
	// kgo.NewClient connects lazily so this succeeds even without a running broker.
	publisher, err := NewKafkaPublisher("localhost:9092", nil)
	require.NoError(t, err)
	require.NotNil(t, publisher)
	// Flush has nothing to flush (no pending records) and returns quickly.
	publisher.Close()
}

func TestNewKafkaPublisher_NilLoggerUsesDefault(t *testing.T) {
	publisher, err := NewKafkaPublisher("localhost:9092", nil)
	require.NoError(t, err)
	require.NotNil(t, publisher)
	assert.NotNil(t, publisher.logger)
	publisher.Close()
}

func TestKafkaPublisher_Close(t *testing.T) {
	publisher, err := NewKafkaPublisher("localhost:9092", slog.Default())
	require.NoError(t, err)
	// Should not panic; Flush returns immediately with no pending records.
	publisher.Close()
}

func TestKafkaPublisher_PublishToTopic_MarshalError(t *testing.T) {
	publisher, err := NewKafkaPublisher("localhost:9092", slog.Default())
	require.NoError(t, err)
	defer publisher.Close()

	ctx := context.Background()
	// Channels cannot be marshaled to JSON.
	err = publisher.publishToTopic(ctx, TopicReconciliationRunStarted, make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal event")
}

func TestKafkaPublisher_Publish_MarshalError(t *testing.T) {
	publisher, err := NewKafkaPublisher("localhost:9092", slog.Default())
	require.NoError(t, err)
	defer publisher.Close()

	ctx := context.Background()
	// Channels cannot be marshaled to JSON; Publish returns the marshal error.
	err = publisher.Publish(ctx, TopicReconciliationRunStarted, make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal event")
}

func TestKafkaPublisher_Publish_MarshalError_UnknownTopic(t *testing.T) {
	publisher, err := NewKafkaPublisher("localhost:9092", slog.Default())
	require.NoError(t, err)
	defer publisher.Close()

	ctx := context.Background()
	// Marshal error is returned even for topics without deprecated mappings.
	err = publisher.Publish(ctx, "unknown.topic", make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal event")
}
