package messaging

import (
	"context"
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
