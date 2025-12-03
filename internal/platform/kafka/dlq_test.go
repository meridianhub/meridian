package kafka

import (
	"context"
	"errors"
	"net"
	"os"
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

// skipIfKafkaUnavailable checks if Kafka is available and skips the test if not.
// This is used for integration tests that require a running Kafka broker.
// Tests are skipped when:
// - SKIP_KAFKA_TESTS environment variable is set (useful for CI)
// - Cannot connect to localhost:9092 within 1 second
func skipIfKafkaUnavailable(t *testing.T) {
	t.Helper()

	// Skip if explicitly requested via environment variable
	if os.Getenv("SKIP_KAFKA_TESTS") != "" {
		t.Skip("Skipping Kafka integration test (SKIP_KAFKA_TESTS set)")
	}

	// Try to connect to Kafka with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", "localhost:9092")
	if err != nil {
		t.Skipf("Kafka not available at localhost:9092: %v", err)
	}
	_ = conn.Close()
}

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
	tests := []struct {
		name              string
		retryBackoffMs    int64
		backoffMultiplier float64
		attempt           int32
		expectedBackoff   time.Duration
		rationale         string
	}{
		// Happy path - standard exponential backoff
		{
			name:              "first retry with defaults",
			retryBackoffMs:    1000,
			backoffMultiplier: 2.0,
			attempt:           1,
			expectedBackoff:   1 * time.Second,
			rationale:         "First retry should use base backoff unchanged",
		},
		{
			name:              "second retry doubles",
			retryBackoffMs:    1000,
			backoffMultiplier: 2.0,
			attempt:           2,
			expectedBackoff:   2 * time.Second,
			rationale:         "Exponential backoff should double on second attempt",
		},
		{
			name:              "third retry quadruples",
			retryBackoffMs:    1000,
			backoffMultiplier: 2.0,
			attempt:           3,
			expectedBackoff:   4 * time.Second,
			rationale:         "Exponential backoff continues: 1s → 2s → 4s",
		},
		{
			name:              "fourth retry 8 seconds",
			retryBackoffMs:    1000,
			backoffMultiplier: 2.0,
			attempt:           4,
			expectedBackoff:   8 * time.Second,
			rationale:         "Exponential backoff continues: 1s → 2s → 4s → 8s",
		},

		// Edge cases - zero and boundary values
		{
			name:              "zero retry backoff defaults to 1000ms",
			retryBackoffMs:    0,
			backoffMultiplier: 2.0,
			attempt:           1,
			expectedBackoff:   1 * time.Second,
			rationale:         "Zero backoff should default to 1000ms to prevent tight loops",
		},
		{
			name:              "zero multiplier defaults to 2.0",
			retryBackoffMs:    1000,
			backoffMultiplier: 0,
			attempt:           2,
			expectedBackoff:   2 * time.Second,
			rationale:         "Zero multiplier should default to 2.0 for exponential backoff",
		},
		{
			name:              "attempt number 0 returns base backoff",
			retryBackoffMs:    1000,
			backoffMultiplier: 2.0,
			attempt:           0,
			expectedBackoff:   1 * time.Second,
			rationale:         "Attempt 0 should not multiply (loop doesn't execute)",
		},

		// Edge cases - small multipliers
		{
			name:              "multiplier 1.0 (linear backoff)",
			retryBackoffMs:    1000,
			backoffMultiplier: 1.0,
			attempt:           5,
			expectedBackoff:   1 * time.Second,
			rationale:         "Multiplier of 1.0 keeps backoff constant",
		},
		{
			name:              "multiplier 1.5 (slower exponential)",
			retryBackoffMs:    1000,
			backoffMultiplier: 1.5,
			attempt:           3,
			expectedBackoff:   2250 * time.Millisecond,
			rationale:         "Slower exponential: 1000ms → 1500ms → 2250ms",
		},

		// Edge cases - large multipliers
		{
			name:              "multiplier 10.0 (rapid exponential)",
			retryBackoffMs:    100,
			backoffMultiplier: 10.0,
			attempt:           3,
			expectedBackoff:   10 * time.Second,
			rationale:         "Large multiplier grows quickly: 100ms → 1s → 10s",
		},

		// Edge cases - very large attempt numbers (overflow prevention)
		{
			name:              "attempt 20 hits maximum backoff cap",
			retryBackoffMs:    1000,
			backoffMultiplier: 2.0,
			attempt:           20,
			expectedBackoff:   5 * time.Minute,
			rationale:         "Very large attempt should hit 5-minute cap to prevent overflow",
		},
		{
			name:              "attempt 100 hits maximum backoff cap",
			retryBackoffMs:    1000,
			backoffMultiplier: 2.0,
			attempt:           100,
			expectedBackoff:   5 * time.Minute,
			rationale:         "Extreme attempt number should be capped at 5 minutes",
		},
		{
			name:              "large base with large attempt hits cap",
			retryBackoffMs:    60000, // 1 minute base
			backoffMultiplier: 2.0,
			attempt:           10,
			expectedBackoff:   5 * time.Minute,
			rationale:         "Large base * exponential growth must hit cap to prevent overflow",
		},

		// Negative testing - values that shouldn't occur but might
		{
			name:              "negative attempt number",
			retryBackoffMs:    1000,
			backoffMultiplier: 2.0,
			attempt:           -1,
			expectedBackoff:   1 * time.Second,
			rationale:         "Negative attempt should not multiply (loop doesn't execute)",
		},
		{
			name:              "very small base backoff",
			retryBackoffMs:    1,
			backoffMultiplier: 2.0,
			attempt:           3,
			expectedBackoff:   4 * time.Millisecond,
			rationale:         "Even tiny backoffs should work: 1ms → 2ms → 4ms",
		},
		{
			name:              "fractional multiplier less than 1 (decreasing backoff)",
			retryBackoffMs:    1000,
			backoffMultiplier: 0.5,
			attempt:           3,
			expectedBackoff:   250 * time.Millisecond,
			rationale:         "Fractional multiplier causes decreasing backoff: 1000ms → 500ms → 250ms",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DLQConfig{
				RetryBackoffMs:    tt.retryBackoffMs,
				BackoffMultiplier: tt.backoffMultiplier,
			}

			backoff := config.CalculateBackoff(tt.attempt)

			if backoff != tt.expectedBackoff {
				t.Errorf("%s\nAttempt %d: expected backoff %v, got %v\nRationale: %s",
					tt.name, tt.attempt, tt.expectedBackoff, backoff, tt.rationale)
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

	// Skip Kafka connection tests in short mode (CI)
	if testing.Short() {
		t.Skip("Skipping Kafka integration test in short mode")
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
	// Skip Kafka connection tests in short mode (CI)
	if testing.Short() {
		t.Skip("Skipping Kafka integration test in short mode")
	}

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
	skipIfKafkaUnavailable(t)

	// Setup
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-dlq-producer",
	})
	if err != nil {
		t.Fatalf("Failed to create producer: %v", err)
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
	skipIfKafkaUnavailable(t)

	// Setup
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-dlq-producer",
	})
	if err != nil {
		t.Fatalf("Failed to create producer: %v", err)
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
	skipIfKafkaUnavailable(t)

	// Setup
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-dlq-producer",
	})
	if err != nil {
		t.Fatalf("Failed to create producer: %v", err)
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
