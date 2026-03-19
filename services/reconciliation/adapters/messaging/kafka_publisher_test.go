package messaging

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoopPublisher_Publish(t *testing.T) {
	pub := NewNoopPublisher(nil)

	err := pub.Publish(context.Background(), "test.topic", map[string]string{"key": "value"})
	assert.NoError(t, err)
}

func TestTopicConstants_FollowNamingConvention(t *testing.T) {
	// Verify all topic constants follow service-name.event-name.v1 convention
	topics := map[string]string{
		"TopicReconciliationRunStarted":   TopicReconciliationRunStarted,
		"TopicReconciliationRunCompleted": TopicReconciliationRunCompleted,
		"TopicVarianceDetected":           TopicVarianceDetected,
		"TopicPositionLockRequested":      TopicPositionLockRequested,
		"TopicDisputeCreated":             TopicDisputeCreated,
		"TopicDisputeResolved":            TopicDisputeResolved,
	}

	for name, topic := range topics {
		assert.Regexp(t, `^reconciliation\.[a-z-]+\.v1$`, topic,
			"%s should follow service-name.event-name.v1 convention", name)
	}
}

func TestDeprecatedTopicFor(t *testing.T) {
	tests := []struct {
		newTopic        string
		deprecatedTopic string
	}{
		{TopicReconciliationRunStarted, DeprecatedTopicReconciliationRunStarted},
		{TopicReconciliationRunCompleted, DeprecatedTopicReconciliationRunCompleted},
		{TopicVarianceDetected, DeprecatedTopicVarianceDetected},
		{TopicPositionLockRequested, DeprecatedTopicPositionLockRequested},
		{TopicDisputeCreated, DeprecatedTopicDisputeCreated},
		{TopicDisputeResolved, DeprecatedTopicDisputeResolved},
	}

	for _, tt := range tests {
		t.Run(tt.newTopic, func(t *testing.T) {
			got := deprecatedTopicFor(tt.newTopic)
			assert.Equal(t, tt.deprecatedTopic, got)
		})
	}

	// Unknown topic returns empty string
	assert.Empty(t, deprecatedTopicFor("unknown.topic"))
}

// accountIDEvent implements the hasAccountID interface used by extractPartitionKey.
type accountIDEvent struct {
	accountID string
}

func (e accountIDEvent) GetAccountID() string { return e.accountID }

func TestExtractPartitionKey(t *testing.T) {
	tests := []struct {
		name     string
		event    interface{}
		expected string
	}{
		{
			name:     "event implementing hasAccountID interface",
			event:    accountIDEvent{accountID: "acct-direct"},
			expected: "acct-direct",
		},
		{
			name:     "event with account_id in JSON map",
			event:    map[string]string{"account_id": "acct-123", "run_id": "run-456"},
			expected: "acct-123",
		},
		{
			name:     "event with run_id only",
			event:    map[string]string{"run_id": "run-456"},
			expected: "run-456",
		},
		{
			name:     "event with neither field",
			event:    map[string]string{"other": "value"},
			expected: "",
		},
		{
			name:     "nil event",
			event:    nil,
			expected: "",
		},
		{
			name:     "unmarshalable event",
			event:    make(chan int),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := extractPartitionKey(tt.event)
			assert.Equal(t, tt.expected, key)
		})
	}
}
