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

func TestExtractPartitionKey(t *testing.T) {
	tests := []struct {
		name     string
		event    interface{}
		expected string
	}{
		{
			name:     "event with account_id",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := extractPartitionKey(tt.event)
			assert.Equal(t, tt.expected, key)
		})
	}
}
