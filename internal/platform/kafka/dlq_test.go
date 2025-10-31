package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	errTestProcessing     = errors.New("test processing error")
	errTestFailure        = errors.New("test processing failure")
	errProtobufProcessing = errors.New("protobuf processing failure")
)

func TestDLQMetadata_ToKafkaHeaders(t *testing.T) {
	now := time.Now()
	metadata := DLQMetadata{
		OriginalTopic:     "test-topic",
		OriginalPartition: 5,
		OriginalOffset:    100,
		ErrorMessage:      "test error",
		ErrorStackTrace:   "stack trace here",
		RetryCount:        3,
		FirstFailureTime:  now.Add(-10 * time.Minute),
		LastFailureTime:   now,
		ConsumerGroupID:   "test-group",
		CorrelationID:     "corr-123",
		CausationID:       "cause-456",
	}

	headers := metadata.ToKafkaHeaders()

	// Verify required headers are present
	requiredHeaders := map[string]bool{
		"dlq.original_topic":     false,
		"dlq.original_partition": false,
		"dlq.original_offset":    false,
		"dlq.error_message":      false,
		"dlq.retry_count":        false,
		"dlq.first_failure_time": false,
		"dlq.last_failure_time":  false,
		"dlq.consumer_group_id":  false,
		"dlq.error_stack_trace":  false,
		"dlq.correlation_id":     false,
		"dlq.causation_id":       false,
	}

	for _, header := range headers {
		if _, exists := requiredHeaders[header.Key]; exists {
			requiredHeaders[header.Key] = true
		}
	}

	for key, found := range requiredHeaders {
		if !found {
			t.Errorf("Required header %s not found", key)
		}
	}
}

func TestDLQMetadata_ToKafkaHeaders_OptionalFields(t *testing.T) {
	now := time.Now()
	metadata := DLQMetadata{
		OriginalTopic:     "test-topic",
		OriginalPartition: 5,
		OriginalOffset:    100,
		ErrorMessage:      "test error",
		RetryCount:        3,
		FirstFailureTime:  now,
		LastFailureTime:   now,
		ConsumerGroupID:   "test-group",
		// Optional fields omitted
	}

	headers := metadata.ToKafkaHeaders()

	// Verify optional headers are NOT present when empty
	for _, header := range headers {
		if header.Key == "dlq.error_stack_trace" ||
			header.Key == "dlq.correlation_id" ||
			header.Key == "dlq.causation_id" {
			t.Errorf("Optional header %s should not be present when value is empty", header.Key)
		}
	}
}

func TestDefaultDLQConfig(t *testing.T) {
	groupID := "test-group"
	config := DefaultDLQConfig(groupID)

	if config.DLQTopicSuffix != "-dlq" {
		t.Errorf("Expected DLQTopicSuffix '-dlq', got %s", config.DLQTopicSuffix)
	}
	if config.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries 3, got %d", config.MaxRetries)
	}
	if config.RetryBackoffMs != 1000 {
		t.Errorf("Expected RetryBackoffMs 1000, got %d", config.RetryBackoffMs)
	}
	if config.BackoffMultiplier != 2.0 {
		t.Errorf("Expected BackoffMultiplier 2.0, got %f", config.BackoffMultiplier)
	}
	if config.ConsumerGroupID != groupID {
		t.Errorf("Expected ConsumerGroupID %s, got %s", groupID, config.ConsumerGroupID)
	}
}

func TestDLQConfig_DLQTopicName(t *testing.T) {
	tests := []struct {
		name          string
		suffix        string
		originalTopic string
		expectedDLQ   string
	}{
		{
			name:          "default suffix",
			suffix:        "",
			originalTopic: "user-events",
			expectedDLQ:   "user-events-dlq",
		},
		{
			name:          "custom suffix",
			suffix:        ".dead-letter",
			originalTopic: "orders",
			expectedDLQ:   "orders.dead-letter",
		},
		{
			name:          "standard suffix",
			suffix:        "-dlq",
			originalTopic: "transactions",
			expectedDLQ:   "transactions-dlq",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DLQConfig{DLQTopicSuffix: tt.suffix}
			result := config.DLQTopicName(tt.originalTopic)
			if result != tt.expectedDLQ {
				t.Errorf("Expected DLQ topic %s, got %s", tt.expectedDLQ, result)
			}
		})
	}
}

func TestDLQConfig_CalculateBackoff(t *testing.T) {
	config := DLQConfig{
		RetryBackoffMs:    1000,
		BackoffMultiplier: 2.0,
	}

	tests := []struct {
		attempt         int32
		expectedBackoff time.Duration
	}{
		{1, 1 * time.Second}, // First retry: 1s
		{2, 2 * time.Second}, // Second retry: 2s
		{3, 4 * time.Second}, // Third retry: 4s
		{4, 8 * time.Second}, // Fourth retry: 8s
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			backoff := config.CalculateBackoff(tt.attempt)
			if backoff != tt.expectedBackoff {
				t.Errorf("Attempt %d: expected backoff %v, got %v",
					tt.attempt, tt.expectedBackoff, backoff)
			}
		})
	}
}

func TestNewDLQProducer(t *testing.T) {
	// Test with nil producer
	_, err := NewDLQProducer(nil, DLQConfig{})
	if !errors.Is(err, ErrNilDLQProducer) {
		t.Errorf("Expected ErrNilDLQProducer, got %v", err)
	}

	// Test with valid producer (will fail to connect to Kafka, but that's ok for unit test)
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-dlq-producer",
	})
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer producer.Close()

	dlqProducer, err := NewDLQProducer(producer, DefaultDLQConfig("test-group"))
	if err != nil {
		t.Errorf("Failed to create DLQ producer: %v", err)
	}
	if dlqProducer == nil {
		t.Error("DLQ producer should not be nil")
	}
	defer dlqProducer.Close()
}

func TestDLQProducer_PublishFailedMessage_Validation(t *testing.T) {
	// Setup - skip if Kafka not available
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-dlq-producer",
	})
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer producer.Close()

	dlqProducer, err := NewDLQProducer(producer, DefaultDLQConfig("test-group"))
	if err != nil {
		t.Fatalf("Failed to create DLQ producer: %v", err)
	}
	defer dlqProducer.Close()

	ctx := context.Background()
	now := time.Now()

	tests := []struct {
		name        string
		msg         *kafka.Message
		expectedErr error
	}{
		{
			name:        "nil message",
			msg:         nil,
			expectedErr: ErrNilMessage,
		},
		{
			name: "nil topic",
			msg: &kafka.Message{
				TopicPartition: kafka.TopicPartition{Topic: nil},
			},
			expectedErr: ErrEmptyTopic,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := dlqProducer.PublishFailedMessage(ctx, tt.msg, errTestProcessing, 3, now)
			if !errors.Is(err, tt.expectedErr) {
				t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
			}
		})
	}
}

func TestDLQProducer_PublishFailedProtoMessage_Validation(t *testing.T) {
	// Setup - skip if Kafka not available
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-dlq-producer",
	})
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer producer.Close()

	dlqProducer, err := NewDLQProducer(producer, DefaultDLQConfig("test-group"))
	if err != nil {
		t.Fatalf("Failed to create DLQ producer: %v", err)
	}
	defer dlqProducer.Close()

	ctx := context.Background()
	now := time.Now()

	tests := []struct {
		name        string
		topic       string
		msg         *timestamppb.Timestamp
		expectedErr error
	}{
		{
			name:        "nil message",
			topic:       "test-topic",
			msg:         nil,
			expectedErr: ErrNilMessage,
		},
		{
			name:        "empty topic",
			topic:       "",
			msg:         timestamppb.Now(),
			expectedErr: ErrEmptyTopic,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := dlqProducer.PublishFailedProtoMessage(
				ctx, tt.topic, "test-key", tt.msg, errTestProcessing, 3, now,
			)
			if !errors.Is(err, tt.expectedErr) {
				t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
			}
		})
	}
}

// TestDLQProducer_PublishFailedMessage_Integration tests actual DLQ message publishing.
// This test requires a running Kafka broker.
func TestDLQProducer_PublishFailedMessage_Integration(t *testing.T) {
	// Setup - skip if Kafka not available
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-dlq-producer",
	})
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer producer.Close()

	dlqConfig := DefaultDLQConfig("test-consumer-group")
	dlqProducer, err := NewDLQProducer(producer, dlqConfig)
	if err != nil {
		t.Fatalf("Failed to create DLQ producer: %v", err)
	}
	defer dlqProducer.Close()

	// Create test message
	topic := "test-original-topic"
	originalMsg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: 0,
			Offset:    100,
		},
		Key:   []byte("test-key"),
		Value: []byte("test-value"),
		Headers: []kafka.Header{
			{Key: "correlation_id", Value: []byte("corr-123")},
			{Key: "causation_id", Value: []byte("cause-456")},
		},
	}

	firstFailureTime := time.Now().Add(-5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Publish to DLQ
	err = dlqProducer.PublishFailedMessage(ctx, originalMsg, errTestFailure, 3, firstFailureTime)
	if err != nil {
		t.Fatalf("Failed to publish to DLQ: %v", err)
	}

	// Flush to ensure message is delivered
	remaining := dlqProducer.Flush(5000)
	if remaining > 0 {
		t.Errorf("Failed to flush all messages, %d remaining", remaining)
	}
}

// TestDLQProducer_PublishFailedProtoMessage_Integration tests protobuf message publishing to DLQ.
// This test requires a running Kafka broker.
func TestDLQProducer_PublishFailedProtoMessage_Integration(t *testing.T) {
	// Setup - skip if Kafka not available
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-dlq-producer",
	})
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer producer.Close()

	dlqConfig := DefaultDLQConfig("test-consumer-group")
	dlqProducer, err := NewDLQProducer(producer, dlqConfig)
	if err != nil {
		t.Fatalf("Failed to create DLQ producer: %v", err)
	}
	defer dlqProducer.Close()

	// Create test proto message
	protoMsg := timestamppb.Now()
	firstFailureTime := time.Now().Add(-2 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Publish proto message to DLQ
	err = dlqProducer.PublishFailedProtoMessage(
		ctx,
		"test-proto-topic",
		"proto-key",
		protoMsg,
		errProtobufProcessing,
		3,
		firstFailureTime,
	)
	if err != nil {
		t.Fatalf("Failed to publish proto message to DLQ: %v", err)
	}

	// Flush to ensure message is delivered
	remaining := dlqProducer.Flush(5000)
	if remaining > 0 {
		t.Errorf("Failed to flush all messages, %d remaining", remaining)
	}
}
