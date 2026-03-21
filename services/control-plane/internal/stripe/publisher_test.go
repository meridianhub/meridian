package stripe

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitBrokers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single broker",
			input:    "localhost:9092",
			expected: []string{"localhost:9092"},
		},
		{
			name:     "multiple brokers",
			input:    "broker1:9092,broker2:9092,broker3:9092",
			expected: []string{"broker1:9092", "broker2:9092", "broker3:9092"},
		},
		{
			name:     "brokers with whitespace",
			input:    " broker1:9092 , broker2:9092 , broker3:9092 ",
			expected: []string{"broker1:9092", "broker2:9092", "broker3:9092"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "only whitespace",
			input:    "  ,  ,  ",
			expected: []string{},
		},
		{
			name:     "trailing comma",
			input:    "broker1:9092,",
			expected: []string{"broker1:9092"},
		},
		{
			name:     "leading comma",
			input:    ",broker1:9092",
			expected: []string{"broker1:9092"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitBrokers(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewKafkaPublisher_EmptyBootstrapServers(t *testing.T) {
	_, err := NewKafkaPublisher(KafkaPublisherConfig{
		BootstrapServers: "",
		Topic:            "payments",
	})
	assert.ErrorIs(t, err, ErrEmptyBootstrapServers)
}

func TestNewKafkaPublisher_WhitespaceOnlyBootstrapServers(t *testing.T) {
	_, err := NewKafkaPublisher(KafkaPublisherConfig{
		BootstrapServers: "  ,  ,  ",
		Topic:            "payments",
	})
	assert.ErrorIs(t, err, ErrEmptyBootstrapServers)
}

func TestNewKafkaPublisher_EmptyTopic(t *testing.T) {
	_, err := NewKafkaPublisher(KafkaPublisherConfig{
		BootstrapServers: "localhost:9092",
		Topic:            "",
	})
	assert.ErrorIs(t, err, ErrEmptyTopic)
}

func TestNewKafkaPublisher_ValidConfig(t *testing.T) {
	pub, err := NewKafkaPublisher(KafkaPublisherConfig{
		BootstrapServers: "localhost:9092",
		Topic:            "payments",
		ClientID:         "test-producer",
	})
	assert.NoError(t, err)
	assert.NotNil(t, pub)
	pub.Close()
}

func TestNewKafkaPublisher_ValidConfigNoClientID(t *testing.T) {
	pub, err := NewKafkaPublisher(KafkaPublisherConfig{
		BootstrapServers: "localhost:9092",
		Topic:            "payments",
	})
	assert.NoError(t, err)
	assert.NotNil(t, pub)
	pub.Close()
}
