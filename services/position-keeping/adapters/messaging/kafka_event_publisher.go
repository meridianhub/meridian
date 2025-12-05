// Package messaging provides adapters for event-driven communication.
package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"google.golang.org/protobuf/proto"
)

// protoPublisher is an interface for publishing protobuf messages to Kafka.
// This interface allows the KafkaEventPublisher to be unit-tested without
// requiring a real Kafka connection.
type protoPublisher interface {
	// Publish publishes a protobuf message to a topic with a partition key
	Publish(ctx context.Context, topic, key string, msg proto.Message) error
	// Flush waits for outstanding messages to be delivered
	Flush(timeoutMs int) int
	// Close closes the producer
	Close()
}

var (
	// ErrNilProducer is returned when producer is nil
	ErrNilProducer = errors.New("kafka producer cannot be nil")
	// ErrNilEvent is returned when event is nil
	ErrNilEvent = errors.New("domain event cannot be nil")
	// ErrInvalidProtoEvent is returned when event cannot be converted to proto
	ErrInvalidProtoEvent = errors.New("event does not implement proto.Message conversion")
	// ErrUnknownEventType is returned when event type is not recognized
	ErrUnknownEventType = errors.New("unknown event type")
)

// TopicConfig defines the Kafka topics for position keeping events.
type TopicConfig struct {
	// TransactionCapturedTopic is the topic for transaction captured events
	TransactionCapturedTopic string
	// TransactionAmendedTopic is the topic for transaction amended events
	TransactionAmendedTopic string
	// TransactionReconciledTopic is the topic for transaction reconciled events
	TransactionReconciledTopic string
	// TransactionPostedTopic is the topic for transaction posted events
	TransactionPostedTopic string
	// TransactionRejectedTopic is the topic for transaction rejected events
	TransactionRejectedTopic string
	// TransactionFailedTopic is the topic for transaction failed events
	TransactionFailedTopic string
	// TransactionCancelledTopic is the topic for transaction cancelled events
	TransactionCancelledTopic string
	// BulkTransactionCapturedTopic is the topic for bulk transaction captured events
	BulkTransactionCapturedTopic string
}

// DefaultTopicConfig returns the default topic configuration for position keeping events.
func DefaultTopicConfig() TopicConfig {
	return TopicConfig{
		TransactionCapturedTopic:     "position-keeping.transaction-captured.v1",
		TransactionAmendedTopic:      "position-keeping.transaction-amended.v1",
		TransactionReconciledTopic:   "position-keeping.transaction-reconciled.v1",
		TransactionPostedTopic:       "position-keeping.transaction-posted.v1",
		TransactionRejectedTopic:     "position-keeping.transaction-rejected.v1",
		TransactionFailedTopic:       "position-keeping.transaction-failed.v1",
		TransactionCancelledTopic:    "position-keeping.transaction-cancelled.v1",
		BulkTransactionCapturedTopic: "position-keeping.bulk-transaction-captured.v1",
	}
}

// KafkaEventPublisher publishes position keeping domain events to Kafka topics.
// It uses the protoPublisher interface for reliable message delivery.
type KafkaEventPublisher struct {
	producer    protoPublisher
	topicConfig TopicConfig
	topicMap    map[string]string // Pre-built map for O(1) topic lookup
}

// NewKafkaEventPublisher creates a new Kafka-based event publisher.
// The producer must be configured with appropriate retry and acknowledgment settings
// for production use. Use DefaultTopicConfig() for standard topic naming.
// The producer parameter can be any implementation of protoPublisher (typically *kafka.ProtoProducer).
func NewKafkaEventPublisher(producer protoPublisher, topicConfig TopicConfig) (*KafkaEventPublisher, error) {
	if producer == nil {
		return nil, ErrNilProducer
	}

	// Pre-build topic routing map for O(1) lookups instead of O(n) switch statements
	topicMap := map[string]string{
		"position_keeping.transaction_captured.v1":      topicConfig.TransactionCapturedTopic,
		"position_keeping.transaction_amended.v1":       topicConfig.TransactionAmendedTopic,
		"position_keeping.transaction_reconciled.v1":    topicConfig.TransactionReconciledTopic,
		"position_keeping.transaction_posted.v1":        topicConfig.TransactionPostedTopic,
		"position_keeping.transaction_rejected.v1":      topicConfig.TransactionRejectedTopic,
		"position_keeping.transaction_failed.v1":        topicConfig.TransactionFailedTopic,
		"position_keeping.transaction_cancelled.v1":     topicConfig.TransactionCancelledTopic,
		"position_keeping.bulk_transaction_captured.v1": topicConfig.BulkTransactionCapturedTopic,
	}

	return &KafkaEventPublisher{
		producer:    producer,
		topicConfig: topicConfig,
		topicMap:    topicMap,
	}, nil
}

// Publish publishes a single domain event to the appropriate Kafka topic.
// The topic is selected based on the event type. The aggregate ID is used as the
// partition key to ensure ordering of events for the same aggregate.
func (p *KafkaEventPublisher) Publish(ctx context.Context, event domain.DomainEvent) error {
	if event == nil {
		return ErrNilEvent
	}

	// Determine topic based on event type
	topic := p.getTopicForEvent(event)
	if topic == "" {
		return fmt.Errorf("%w: %s", ErrUnknownEventType, event.EventType())
	}

	// Convert to proto message
	protoEvent := event.ToProto()
	protoMsg, ok := protoEvent.(proto.Message)
	if !ok {
		return fmt.Errorf("%w: event type %s", ErrInvalidProtoEvent, event.EventType())
	}

	// Use aggregate ID as partition key for ordering
	partitionKey := event.AggregateID()

	// Publish to Kafka
	if err := p.producer.Publish(ctx, topic, partitionKey, protoMsg); err != nil {
		return fmt.Errorf("failed to publish event %s to topic %s: %w", event.EventType(), topic, err)
	}

	return nil
}

// PublishBatch publishes multiple domain events to Kafka.
// Each event is published individually with its own error handling.
// If any event fails, the method returns an error but previous events remain published.
// For true transactional semantics, use Kafka transactions (not implemented here).
func (p *KafkaEventPublisher) PublishBatch(ctx context.Context, events []domain.DomainEvent) error {
	if len(events) == 0 {
		return nil
	}

	for i, event := range events {
		if err := p.Publish(ctx, event); err != nil {
			return fmt.Errorf("failed to publish event at index %d: %w", i, err)
		}
	}

	return nil
}

// getTopicForEvent maps event types to their corresponding Kafka topics.
// Uses a pre-built map for O(1) lookup performance, making it scalable
// for adding new event types without degrading performance.
func (p *KafkaEventPublisher) getTopicForEvent(event domain.DomainEvent) string {
	topic, exists := p.topicMap[event.EventType()]
	if !exists {
		return ""
	}
	return topic
}

// Close closes the underlying Kafka producer and releases resources.
// Should be called during application shutdown. This does not wait for
// outstanding messages - call Flush() first if needed.
func (p *KafkaEventPublisher) Close() {
	p.producer.Close()
}

// Flush waits for all outstanding messages to be delivered.
// Returns the number of messages still in flight after the timeout.
func (p *KafkaEventPublisher) Flush(timeoutMs int) int {
	return p.producer.Flush(timeoutMs)
}
