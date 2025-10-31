// Package kafka provides dead letter queue (DLQ) support for failed message processing.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrEmptyDLQTopic is returned when DLQ topic name is empty.
	ErrEmptyDLQTopic = errors.New("DLQ topic cannot be empty")
	// ErrNilDLQProducer is returned when DLQ producer is nil.
	ErrNilDLQProducer = errors.New("DLQ producer cannot be nil")
)

// DLQMetadata contains comprehensive metadata for messages sent to dead letter queue.
// This metadata provides complete context for debugging and reprocessing failed messages.
type DLQMetadata struct {
	// OriginalTopic is the source topic where the message was originally consumed from.
	OriginalTopic string
	// OriginalPartition is the partition number from the original topic.
	OriginalPartition int32
	// OriginalOffset is the offset in the original partition.
	OriginalOffset int64
	// ErrorMessage is the error description from the failed processing attempt.
	ErrorMessage string
	// ErrorStackTrace provides detailed error context (if available).
	ErrorStackTrace string
	// RetryCount tracks how many times processing was attempted before sending to DLQ.
	RetryCount int32
	// FirstFailureTime is when the first processing attempt failed.
	FirstFailureTime time.Time
	// LastFailureTime is when the final processing attempt failed before DLQ.
	LastFailureTime time.Time
	// ConsumerGroupID identifies which consumer group failed to process this message.
	ConsumerGroupID string
	// CorrelationID provides end-to-end traceability across services.
	CorrelationID string
	// CausationID links to the message that caused this message to be created.
	CausationID string
}

// ToKafkaHeaders converts DLQ metadata to Kafka message headers.
// Headers use string encoding for compatibility and ease of debugging.
// All timestamps are formatted as RFC3339 for standard parsing.
func (m *DLQMetadata) ToKafkaHeaders() []kafka.Header {
	headers := []kafka.Header{
		{Key: "dlq.original_topic", Value: []byte(m.OriginalTopic)},
		{Key: "dlq.original_partition", Value: []byte(fmt.Sprintf("%d", m.OriginalPartition))},
		{Key: "dlq.original_offset", Value: []byte(fmt.Sprintf("%d", m.OriginalOffset))},
		{Key: "dlq.error_message", Value: []byte(m.ErrorMessage)},
		{Key: "dlq.retry_count", Value: []byte(fmt.Sprintf("%d", m.RetryCount))},
		{Key: "dlq.first_failure_time", Value: []byte(m.FirstFailureTime.Format(time.RFC3339))},
		{Key: "dlq.last_failure_time", Value: []byte(m.LastFailureTime.Format(time.RFC3339))},
		{Key: "dlq.consumer_group_id", Value: []byte(m.ConsumerGroupID)},
	}

	// Add optional fields if present
	if m.ErrorStackTrace != "" {
		headers = append(headers, kafka.Header{
			Key:   "dlq.error_stack_trace",
			Value: []byte(m.ErrorStackTrace),
		})
	}
	if m.CorrelationID != "" {
		headers = append(headers, kafka.Header{
			Key:   "dlq.correlation_id",
			Value: []byte(m.CorrelationID),
		})
	}
	if m.CausationID != "" {
		headers = append(headers, kafka.Header{
			Key:   "dlq.causation_id",
			Value: []byte(m.CausationID),
		})
	}

	return headers
}

// DLQConfig configures dead letter queue behavior.
type DLQConfig struct {
	// DLQTopicSuffix is appended to the original topic name to create DLQ topic name.
	// Default: "-dlq"
	// Example: "user-events" becomes "user-events-dlq"
	DLQTopicSuffix string
	// MaxRetries is the number of processing attempts before sending to DLQ.
	// Default: 3
	MaxRetries int32
	// RetryBackoffMs is the delay between retry attempts in milliseconds.
	// Default: 1000 (1 second)
	RetryBackoffMs int64
	// BackoffMultiplier for exponential backoff between retries.
	// Default: 2.0 (doubles delay each retry)
	BackoffMultiplier float64
	// ConsumerGroupID identifies the consumer group for DLQ metadata.
	ConsumerGroupID string
}

// DefaultDLQConfig returns DLQ configuration with sensible production defaults.
func DefaultDLQConfig(consumerGroupID string) DLQConfig {
	return DLQConfig{
		DLQTopicSuffix:    "-dlq",
		MaxRetries:        3,
		RetryBackoffMs:    1000,
		BackoffMultiplier: 2.0,
		ConsumerGroupID:   consumerGroupID,
	}
}

// DLQTopicName generates the DLQ topic name from the original topic name.
// Uses the configured suffix to create a consistent naming pattern.
func (c *DLQConfig) DLQTopicName(originalTopic string) string {
	if c.DLQTopicSuffix == "" {
		c.DLQTopicSuffix = "-dlq"
	}
	return originalTopic + c.DLQTopicSuffix
}

// CalculateBackoff calculates the backoff duration for a given retry attempt.
// Uses exponential backoff with the configured multiplier.
// attemptNumber is 1-based (first retry is attempt 1).
// Maximum backoff is capped at 5 minutes to prevent overflow and excessive delays.
func (c *DLQConfig) CalculateBackoff(attemptNumber int32) time.Duration {
	if c.BackoffMultiplier == 0 {
		c.BackoffMultiplier = 2.0
	}
	if c.RetryBackoffMs == 0 {
		c.RetryBackoffMs = 1000
	}

	const maxBackoffMs = 5 * 60 * 1000 // 5 minutes in milliseconds

	backoffMs := float64(c.RetryBackoffMs)
	for i := int32(1); i < attemptNumber; i++ {
		backoffMs *= c.BackoffMultiplier
		// Cap at maximum to prevent overflow
		if backoffMs > maxBackoffMs {
			backoffMs = maxBackoffMs
			break
		}
	}
	return time.Duration(backoffMs) * time.Millisecond
}

// DLQProducer wraps ProtoProducer to provide specialized dead letter queue functionality.
// It enriches failed messages with comprehensive metadata for debugging and reprocessing.
type DLQProducer struct {
	// producer is the underlying Kafka producer
	producer *ProtoProducer
	// config contains DLQ-specific configuration
	config DLQConfig
}

// NewDLQProducer creates a new dead letter queue producer.
// The DLQ producer wraps a standard ProtoProducer and adds DLQ-specific metadata enrichment.
//
// Parameters:
// - producer: The underlying Kafka producer (must not be nil)
// - config: DLQ configuration including retry settings and topic naming
//
// Returns an error if producer is nil.
func NewDLQProducer(producer *ProtoProducer, config DLQConfig) (*DLQProducer, error) {
	if producer == nil {
		return nil, ErrNilDLQProducer
	}

	return &DLQProducer{
		producer: producer,
		config:   config,
	}, nil
}

// PublishFailedMessage sends a failed message to the dead letter queue with enriched metadata.
// This method should be called after all retry attempts have been exhausted.
//
// The original message is preserved exactly as received, with metadata added as Kafka headers.
// This allows for debugging the original message and potential reprocessing.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
// - originalMsg: The original Kafka message that failed processing
// - err: The error that caused the failure
// - retryCount: Number of times processing was attempted
// - firstFailureTime: When the first processing attempt failed
//
// Returns an error if:
// - DLQ topic generation fails
// - message publishing fails
// - context is cancelled
func (d *DLQProducer) PublishFailedMessage(
	ctx context.Context,
	originalMsg *kafka.Message,
	err error,
	retryCount int32,
	firstFailureTime time.Time,
) error {
	if originalMsg == nil {
		return ErrNilMessage
	}
	if originalMsg.TopicPartition.Topic == nil {
		return ErrEmptyTopic
	}

	// Generate DLQ topic name
	originalTopic := *originalMsg.TopicPartition.Topic
	dlqTopic := d.config.DLQTopicName(originalTopic)

	if dlqTopic == "" {
		return ErrEmptyDLQTopic
	}

	// Create comprehensive metadata
	metadata := DLQMetadata{
		OriginalTopic:     originalTopic,
		OriginalPartition: originalMsg.TopicPartition.Partition,
		OriginalOffset:    int64(originalMsg.TopicPartition.Offset),
		ErrorMessage:      err.Error(),
		ErrorStackTrace:   fmt.Sprintf("%+v", err), // Capture full error context
		RetryCount:        retryCount,
		FirstFailureTime:  firstFailureTime,
		LastFailureTime:   time.Now(),
		ConsumerGroupID:   d.config.ConsumerGroupID,
	}

	// Extract correlation/causation IDs from original message headers if present
	for _, header := range originalMsg.Headers {
		switch header.Key {
		case "correlation_id", "x-correlation-id":
			metadata.CorrelationID = string(header.Value)
		case "causation_id", "x-causation-id":
			metadata.CausationID = string(header.Value)
		}
	}

	// Create Kafka message with enriched headers
	dlqMsg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &dlqTopic, Partition: kafka.PartitionAny},
		Key:            originalMsg.Key,
		Value:          originalMsg.Value, // Preserve original message bytes exactly
		Headers:        metadata.ToKafkaHeaders(),
		Timestamp:      time.Now(),
	}

	// Publish with delivery report channel
	deliveryChan := make(chan kafka.Event, 1)
	if err := d.producer.producer.Produce(dlqMsg, deliveryChan); err != nil {
		return fmt.Errorf("failed to produce DLQ message: %w", err)
	}

	// Wait for delivery confirmation or context cancellation
	select {
	case e := <-deliveryChan:
		m, ok := e.(*kafka.Message)
		if !ok {
			return fmt.Errorf("%w: %T", ErrUnexpectedEvent, e)
		}
		if m.TopicPartition.Error != nil {
			return fmt.Errorf("DLQ delivery failed: %w", m.TopicPartition.Error)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("DLQ publish cancelled: %w", ctx.Err())
	}
}

// PublishFailedProtoMessage sends a failed protobuf message to the dead letter queue.
// This is a convenience method for cases where you have the deserialized proto message
// and need to re-serialize it before sending to DLQ.
//
// Note: When possible, use PublishFailedMessage with the original Kafka message bytes
// to preserve the exact message that failed processing.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
// - originalTopic: The source topic name
// - key: The message key
// - msg: The protobuf message that failed processing
// - err: The error that caused the failure
// - retryCount: Number of processing attempts
// - firstFailureTime: When the first failure occurred
//
// Returns an error if serialization or publishing fails.
func (d *DLQProducer) PublishFailedProtoMessage(
	ctx context.Context,
	originalTopic string,
	key string,
	msg proto.Message,
	err error,
	retryCount int32,
	firstFailureTime time.Time,
) error {
	if msg == nil {
		return ErrNilMessage
	}
	if originalTopic == "" {
		return ErrEmptyTopic
	}

	// Serialize protobuf message
	data, marshalErr := proto.Marshal(msg)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal protobuf message for DLQ: %w", marshalErr)
	}

	// Create pseudo Kafka message for DLQ processing
	originalMsg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &originalTopic,
			Partition: kafka.PartitionAny,
			Offset:    kafka.OffsetInvalid,
		},
		Key:   []byte(key),
		Value: data,
	}

	return d.PublishFailedMessage(ctx, originalMsg, err, retryCount, firstFailureTime)
}

// Close closes the underlying producer.
// This is a convenience method to avoid exposing the wrapped producer.
func (d *DLQProducer) Close() {
	d.producer.Close()
}

// Flush flushes the underlying producer.
// This is a convenience method to ensure DLQ messages are delivered before shutdown.
func (d *DLQProducer) Flush(timeoutMs int) int {
	return d.producer.Flush(timeoutMs)
}
