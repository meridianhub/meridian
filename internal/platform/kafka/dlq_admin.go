// Package kafka provides DLQ administrative and monitoring utilities.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"google.golang.org/protobuf/proto"
)

// ErrNilInspector is returned when DLQ inspector is nil.
var ErrNilInspector = errors.New("DLQ inspector cannot be nil")

// DLQMessage represents a message in the dead letter queue with parsed metadata.
type DLQMessage struct {
	// Message is the original Kafka message from the DLQ topic
	Message *kafka.Message
	// Metadata contains parsed DLQ headers
	Metadata DLQMetadata
}

// DLQInspectorConfig configures the DLQ inspector behavior.
type DLQInspectorConfig struct {
	// BootstrapServers is the comma-separated list of Kafka broker addresses (required).
	BootstrapServers string
	// ClientID identifies the inspector for logging and metrics (optional).
	ClientID string
	// DLQTopics is the list of DLQ topic names to inspect (required).
	DLQTopics []string
}

// DLQInspector provides utilities for examining and analyzing dead letter queue messages.
type DLQInspector struct {
	consumer *kafka.Consumer
	config   DLQInspectorConfig
}

// NewDLQInspector creates a new DLQ inspector for examining failed messages.
//
// Parameters:
// - config: Inspector configuration with broker addresses and DLQ topics
//
// Returns an error if configuration is invalid or consumer creation fails.
func NewDLQInspector(config DLQInspectorConfig) (*DLQInspector, error) {
	if config.BootstrapServers == "" {
		return nil, ErrEmptyBootstrapServers
	}
	if len(config.DLQTopics) == 0 {
		return nil, ErrEmptyTopics
	}

	// Create consumer with unique group ID for inspection (won't commit offsets)
	consumer, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers":  config.BootstrapServers,
		"group.id":           fmt.Sprintf("dlq-inspector-%d", time.Now().Unix()),
		"client.id":          config.ClientID,
		"auto.offset.reset":  "earliest",
		"enable.auto.commit": false, // Inspector never commits offsets
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create DLQ inspector consumer: %w", err)
	}

	return &DLQInspector{
		consumer: consumer,
		config:   config,
	}, nil
}

// parseDLQMetadata extracts DLQ metadata from Kafka message headers.
func parseDLQMetadata(msg *kafka.Message) DLQMetadata {
	metadata := DLQMetadata{}

	for _, header := range msg.Headers {
		value := string(header.Value)

		switch header.Key {
		case "dlq.original_topic":
			metadata.OriginalTopic = value
		case "dlq.original_partition":
			if partition, err := strconv.ParseInt(value, 10, 32); err == nil {
				metadata.OriginalPartition = int32(partition)
			}
		case "dlq.original_offset":
			if offset, err := strconv.ParseInt(value, 10, 64); err == nil {
				metadata.OriginalOffset = offset
			}
		case "dlq.error_message":
			metadata.ErrorMessage = value
		case "dlq.error_stack_trace":
			metadata.ErrorStackTrace = value
		case "dlq.retry_count":
			if retries, err := strconv.ParseInt(value, 10, 32); err == nil {
				metadata.RetryCount = int32(retries)
			}
		case "dlq.first_failure_time":
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				metadata.FirstFailureTime = t
			}
		case "dlq.last_failure_time":
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				metadata.LastFailureTime = t
			}
		case "dlq.consumer_group_id":
			metadata.ConsumerGroupID = value
		case "dlq.correlation_id":
			metadata.CorrelationID = value
		case "dlq.causation_id":
			metadata.CausationID = value
		}
	}

	return metadata
}

// FilterFunc is a predicate function for filtering DLQ messages.
// Return true to include the message in results, false to exclude it.
type FilterFunc func(msg DLQMessage) bool

// FilterByErrorType creates a filter that matches messages with errors containing the specified text.
func FilterByErrorType(errorText string) FilterFunc {
	return func(msg DLQMessage) bool {
		return strings.Contains(strings.ToLower(msg.Metadata.ErrorMessage), strings.ToLower(errorText))
	}
}

// FilterByOriginalTopic creates a filter that matches messages from a specific original topic.
func FilterByOriginalTopic(topic string) FilterFunc {
	return func(msg DLQMessage) bool {
		return msg.Metadata.OriginalTopic == topic
	}
}

// FilterByTimeRange creates a filter that matches messages that failed within a time range.
func FilterByTimeRange(start, end time.Time) FilterFunc {
	return func(msg DLQMessage) bool {
		return !msg.Metadata.LastFailureTime.Before(start) && !msg.Metadata.LastFailureTime.After(end)
	}
}

// FilterByConsumerGroup creates a filter that matches messages from a specific consumer group.
func FilterByConsumerGroup(groupID string) FilterFunc {
	return func(msg DLQMessage) bool {
		return msg.Metadata.ConsumerGroupID == groupID
	}
}

// CombineFilters combines multiple filters with AND logic.
// All filters must return true for a message to be included.
func CombineFilters(filters ...FilterFunc) FilterFunc {
	return func(msg DLQMessage) bool {
		for _, filter := range filters {
			if !filter(msg) {
				return false
			}
		}
		return true
	}
}

// InspectOptions configures the behavior of the Inspect method.
type InspectOptions struct {
	// Filter is an optional predicate to filter messages. If nil, all messages are returned.
	Filter FilterFunc
	// MaxMessages limits the number of messages to return. 0 means no limit.
	MaxMessages int
	// Timeout is the maximum duration to wait for messages.
	Timeout time.Duration
}

// Inspect reads messages from the DLQ topics and returns them according to the filter criteria.
// This method does not commit offsets - it's read-only for inspection purposes.
//
// Parameters:
// - ctx: Context for cancellation
// - options: Configuration for filtering and limiting results
//
// Returns a slice of DLQ messages that match the filter criteria.
func (i *DLQInspector) Inspect(ctx context.Context, options InspectOptions) ([]DLQMessage, error) {
	// Assign partitions manually for full control (no group coordination)
	partitions := make([]kafka.TopicPartition, 0)
	for _, topic := range i.config.DLQTopics {
		// Get partition metadata
		metadata, err := i.consumer.GetMetadata(&topic, false, 5000)
		if err != nil {
			return nil, fmt.Errorf("failed to get metadata for topic %s: %w", topic, err)
		}

		topicMetadata := metadata.Topics[topic]
		for _, partition := range topicMetadata.Partitions {
			partitions = append(partitions, kafka.TopicPartition{
				Topic:     &topic,
				Partition: partition.ID,
				Offset:    kafka.OffsetBeginning,
			})
		}
	}

	// Assign partitions (seek to beginning)
	if err := i.consumer.Assign(partitions); err != nil {
		return nil, fmt.Errorf("failed to assign partitions: %w", err)
	}

	// Set default timeout if not specified
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// Create timeout context
	inspectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results := make([]DLQMessage, 0)
	consecutiveTimeouts := 0
	maxConsecutiveTimeouts := 5 // Stop after 5 consecutive timeouts

	for {
		// Check for context cancellation
		select {
		case <-inspectCtx.Done():
			return results, fmt.Errorf("inspect context canceled: %w", inspectCtx.Err())
		default:
		}

		// Check if we've hit the message limit
		if options.MaxMessages > 0 && len(results) >= options.MaxMessages {
			return results, nil
		}

		// Poll for messages
		msg, err := i.consumer.ReadMessage(100 * time.Millisecond)
		if err != nil {
			// Timeout is expected when no more messages
			var kafkaErr kafka.Error
			if errors.As(err, &kafkaErr) && kafkaErr.Code() == kafka.ErrTimedOut {
				consecutiveTimeouts++
				if consecutiveTimeouts >= maxConsecutiveTimeouts {
					// No more messages available
					return results, nil
				}
				continue
			}
			return results, fmt.Errorf("error reading DLQ message: %w", err)
		}

		// Reset timeout counter on successful read
		consecutiveTimeouts = 0

		// Parse DLQ metadata
		metadata := parseDLQMetadata(msg)

		dlqMsg := DLQMessage{
			Message:  msg,
			Metadata: metadata,
		}

		// Apply filter if specified
		if options.Filter != nil && !options.Filter(dlqMsg) {
			continue
		}

		results = append(results, dlqMsg)
	}
}

// DLQStatistics contains aggregate statistics about DLQ messages.
type DLQStatistics struct {
	// TotalMessages is the total number of messages in the DLQ
	TotalMessages int
	// MessagesByTopic maps original topic names to message counts
	MessagesByTopic map[string]int
	// MessagesByErrorType maps error types to message counts
	MessagesByErrorType map[string]int
	// MessagesByConsumerGroup maps consumer group IDs to message counts
	MessagesByConsumerGroup map[string]int
	// OldestFailure is the timestamp of the oldest message in DLQ
	OldestFailure time.Time
	// NewestFailure is the timestamp of the newest message in DLQ
	NewestFailure time.Time
}

// GetStatistics analyzes all messages in the DLQ and returns aggregate statistics.
func (i *DLQInspector) GetStatistics(ctx context.Context, timeout time.Duration) (DLQStatistics, error) {
	messages, err := i.Inspect(ctx, InspectOptions{
		Filter:      nil, // No filter - get all messages
		MaxMessages: 0,   // No limit
		Timeout:     timeout,
	})
	if err != nil {
		return DLQStatistics{}, err
	}

	stats := DLQStatistics{
		TotalMessages:           len(messages),
		MessagesByTopic:         make(map[string]int),
		MessagesByErrorType:     make(map[string]int),
		MessagesByConsumerGroup: make(map[string]int),
	}

	for _, msg := range messages {
		// Count by original topic
		stats.MessagesByTopic[msg.Metadata.OriginalTopic]++

		// Count by error type (extract first line of error message)
		errorType := strings.Split(msg.Metadata.ErrorMessage, "\n")[0]
		if len(errorType) > 100 {
			errorType = errorType[:100] + "..."
		}
		stats.MessagesByErrorType[errorType]++

		// Count by consumer group
		stats.MessagesByConsumerGroup[msg.Metadata.ConsumerGroupID]++

		// Track oldest/newest failures
		if stats.OldestFailure.IsZero() || msg.Metadata.FirstFailureTime.Before(stats.OldestFailure) {
			stats.OldestFailure = msg.Metadata.FirstFailureTime
		}
		if msg.Metadata.LastFailureTime.After(stats.NewestFailure) {
			stats.NewestFailure = msg.Metadata.LastFailureTime
		}
	}

	return stats, nil
}

// Close closes the inspector and releases resources.
func (i *DLQInspector) Close() error {
	if err := i.consumer.Close(); err != nil {
		return fmt.Errorf("failed to close DLQ inspector: %w", err)
	}
	return nil
}

// DLQReplayConfig configures DLQ message replay behavior.
type DLQReplayConfig struct {
	// BootstrapServers is the comma-separated list of Kafka broker addresses (required).
	BootstrapServers string
	// ClientID identifies the replay producer for logging and metrics (optional).
	ClientID string
}

// DLQReplay provides utilities for reprocessing messages from the dead letter queue.
type DLQReplay struct {
	producer *ProtoProducer
	config   DLQReplayConfig
}

// NewDLQReplay creates a new DLQ replay utility for reprocessing failed messages.
//
// Parameters:
// - config: Replay configuration with broker addresses
//
// Returns an error if configuration is invalid or producer creation fails.
func NewDLQReplay(config DLQReplayConfig) (*DLQReplay, error) {
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: config.BootstrapServers,
		ClientID:         config.ClientID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create DLQ replay producer: %w", err)
	}

	return &DLQReplay{
		producer: producer,
		config:   config,
	}, nil
}

// ReplayMessage sends a DLQ message back to its original topic for reprocessing.
// The message is sent exactly as it was originally produced (same key and value).
// DLQ headers are removed so the message appears as a new message to consumers.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
// - dlqMsg: The DLQ message to replay
//
// Returns an error if publishing fails.
func (r *DLQReplay) ReplayMessage(ctx context.Context, dlqMsg DLQMessage) error {
	// Create new message without DLQ headers
	// Preserve original timestamp and timestamp type to maintain event-time semantics
	originalTopic := dlqMsg.Metadata.OriginalTopic
	replayMsg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &originalTopic, Partition: kafka.PartitionAny},
		Key:            dlqMsg.Message.Key,
		Value:          dlqMsg.Message.Value,
		Timestamp:      dlqMsg.Message.Timestamp,
		TimestampType:  dlqMsg.Message.TimestampType,
	}

	// Preserve non-DLQ headers
	for _, header := range dlqMsg.Message.Headers {
		if !strings.HasPrefix(header.Key, "dlq.") {
			replayMsg.Headers = append(replayMsg.Headers, header)
		}
	}

	// Publish with delivery report channel
	deliveryChan := make(chan kafka.Event, 1)
	if err := r.producer.producer.Produce(replayMsg, deliveryChan); err != nil {
		return fmt.Errorf("failed to produce replay message: %w", err)
	}

	// Wait for delivery confirmation or context cancellation
	select {
	case e := <-deliveryChan:
		m, ok := e.(*kafka.Message)
		if !ok {
			return fmt.Errorf("%w: %T", ErrUnexpectedEvent, e)
		}
		if m.TopicPartition.Error != nil {
			return fmt.Errorf("replay delivery failed: %w", m.TopicPartition.Error)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("replay cancelled: %w", ctx.Err())
	}
}

// ReplayMessages replays multiple DLQ messages back to their original topics.
// Messages are sent in order. If any message fails, the method returns the error
// and stops processing remaining messages.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
// - messages: The DLQ messages to replay
//
// Returns the number of messages successfully replayed and any error encountered.
func (r *DLQReplay) ReplayMessages(ctx context.Context, messages []DLQMessage) (int, error) {
	successCount := 0
	for i, msg := range messages {
		if err := r.ReplayMessage(ctx, msg); err != nil {
			return successCount, fmt.Errorf("failed to replay message %d/%d: %w", i+1, len(messages), err)
		}
		successCount++
	}
	return successCount, nil
}

// Close closes the replay producer and releases resources.
func (r *DLQReplay) Close() {
	r.producer.Close()
}

// ReplayProtoMessage is a convenience method for replaying a protobuf message.
// This deserializes the DLQ message value using the provided factory and
// re-serializes it before sending to the original topic.
//
// Note: This is primarily useful for transformation scenarios. For simple replay,
// use ReplayMessage to preserve the exact original bytes.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
// - dlqMsg: The DLQ message to replay
// - msgFactory: Function to create a new proto message instance
//
// Returns the deserialized proto message and any error encountered.
func (r *DLQReplay) ReplayProtoMessage(
	ctx context.Context,
	dlqMsg DLQMessage,
	msgFactory func() proto.Message,
) (proto.Message, error) {
	// Deserialize the message
	protoMsg := msgFactory()
	if err := proto.Unmarshal(dlqMsg.Message.Value, protoMsg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DLQ message: %w", err)
	}

	// Use standard replay (preserves original bytes)
	if err := r.ReplayMessage(ctx, dlqMsg); err != nil {
		return nil, err
	}

	return protoMsg, nil
}
