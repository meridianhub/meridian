// Package kafka provides generic Kafka producer and consumer utilities for Protocol Buffer messages.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/meridianhub/meridian/shared/platform/organization"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrEmptyTopic is returned when topic name is empty.
	ErrEmptyTopic = errors.New("topic cannot be empty")
	// ErrNilMessage is returned when message is nil.
	ErrNilMessage = errors.New("message cannot be nil")
	// ErrUnexpectedEvent is returned when delivery channel receives unexpected event type.
	ErrUnexpectedEvent = errors.New("unexpected event type from delivery channel")
)

// ProtoProducer handles publishing Protocol Buffer messages to Kafka topics.
// It provides synchronous publishing with delivery confirmation, ensuring reliable
// message delivery to Kafka brokers. The producer uses configurable acks, retries,
// and compression for production-grade performance and durability.
type ProtoProducer struct {
	// producer is the underlying confluent-kafka-go producer instance
	producer *kafka.Producer
}

// ProducerConfig contains configuration for creating a Kafka producer.
// Use sensible defaults or customize for specific workloads.
type ProducerConfig struct {
	// BootstrapServers is the comma-separated list of Kafka broker addresses (required).
	BootstrapServers string
	// ClientID identifies the producer for logging and metrics (optional).
	ClientID string
	// Acks controls durability: "all" (default) waits for all replicas, "1" waits for leader,
	// "0" sends without confirmation.
	Acks string
	// Retries configures how many times to retry failed sends (default: 3).
	Retries int
	// Compression algorithm: "snappy" (default), "gzip", "lz4", "zstd", or "none".
	Compression string
}

// NewProtoProducer creates a new Kafka producer for protobuf messages.
// It validates the configuration and applies sensible defaults for production use:
// - Acks: "all" (wait for full replication)
// - Retries: 3
// - Compression: "snappy"
// - Linger: 10ms (batching window)
// - Batch size: 16KB
//
// Returns an error if BootstrapServers is empty or if the underlying Kafka producer
// fails to initialize.
func NewProtoProducer(config ProducerConfig) (*ProtoProducer, error) {
	if config.BootstrapServers == "" {
		return nil, ErrEmptyBootstrapServers
	}

	// Set defaults
	if config.Acks == "" {
		config.Acks = "all"
	}
	if config.Retries == 0 {
		config.Retries = 3
	}
	if config.Compression == "" {
		config.Compression = "snappy"
	}

	producer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers": config.BootstrapServers,
		"client.id":         config.ClientID,
		"acks":              config.Acks,
		"retries":           config.Retries,
		"compression.type":  config.Compression,
		"linger.ms":         10, // Batch messages for 10ms to improve throughput
		"batch.size":        16384,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka producer: %w", err)
	}

	return &ProtoProducer{producer: producer}, nil
}

// Publish sends a protobuf message to the specified Kafka topic.
// The key is used for partitioning - messages with the same key go to the same partition.
// This method blocks until the message is confirmed by the Kafka broker or the context is cancelled.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
// - topic: Target Kafka topic name (must not be empty)
// - key: Partition key as string (empty key will be null in Kafka)
// - msg: Protocol Buffer message to serialize and send (must not be nil)
//
// Returns an error if:
// - topic is empty
// - msg is nil
// - protobuf marshaling fails
// - message production fails
// - delivery confirmation indicates failure
// - context is cancelled before delivery confirmation
func (p *ProtoProducer) Publish(ctx context.Context, topic string, key string, msg proto.Message) error {
	if topic == "" {
		return ErrEmptyTopic
	}
	if msg == nil {
		return ErrNilMessage
	}

	// Serialize protobuf message to bytes
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}

	// Create Kafka message
	kafkaMsg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: kafka.PartitionAny},
		Key:            []byte(key),
		Value:          data,
		Timestamp:      time.Now(),
	}

	// Publish with delivery report channel
	deliveryChan := make(chan kafka.Event, 1)
	err = p.producer.Produce(kafkaMsg, deliveryChan)
	if err != nil {
		return fmt.Errorf("failed to produce message: %w", err)
	}

	// Wait for delivery confirmation or context cancellation
	select {
	case e := <-deliveryChan:
		m, ok := e.(*kafka.Message)
		if !ok {
			return fmt.Errorf("%w: %T", ErrUnexpectedEvent, e)
		}
		if m.TopicPartition.Error != nil {
			return fmt.Errorf("delivery failed: %w", m.TopicPartition.Error)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("publish cancelled: %w", ctx.Err())
	}
}

// Flush waits for all outstanding messages to be delivered.
// This should be called before shutting down the producer to ensure no messages are lost.
//
// Parameters:
// - timeoutMs: Maximum time to wait in milliseconds
//
// Returns the number of messages still in flight after the timeout.
// A return value of 0 indicates all messages were successfully delivered.
func (p *ProtoProducer) Flush(timeoutMs int) int {
	return p.producer.Flush(timeoutMs)
}

// Close closes the producer and releases resources.
// This method should be called when the producer is no longer needed to free up
// network connections and other system resources. It does not wait for outstanding
// messages - call Flush() first if needed.
func (p *ProtoProducer) Close() {
	p.producer.Close()
}

// PublishWithOrganization sends a protobuf message with organization context to the specified Kafka topic.
// The organization ID is extracted from the context and injected as a Kafka header (x-org-id).
// This ensures organization isolation for multi-tenant event processing.
//
// The key is used for partitioning - messages with the same key go to the same partition.
// This method blocks until the message is confirmed by the Kafka broker or the context is cancelled.
//
// Parameters:
// - ctx: Context containing organization ID (via organization.WithOrganization) for cancellation and timeout
// - topic: Target Kafka topic name (must not be empty)
// - key: Partition key as string (empty key will be null in Kafka)
// - msg: Protocol Buffer message to serialize and send (must not be nil)
//
// Panics if the organization context is missing - this is a fail-fast strategy to prevent
// events without organization attribution from being published.
//
// Returns an error if:
// - topic is empty
// - msg is nil
// - protobuf marshaling fails
// - message production fails
// - delivery confirmation indicates failure
// - context is cancelled before delivery confirmation
func (p *ProtoProducer) PublishWithOrganization(ctx context.Context, topic string, key string, msg proto.Message) error {
	// Extract organization from context - panic if missing (fail-fast)
	orgID := organization.MustFromContext(ctx)

	if topic == "" {
		return ErrEmptyTopic
	}
	if msg == nil {
		return ErrNilMessage
	}

	// Serialize protobuf message to bytes
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}

	// Create Kafka message with organization header
	kafkaMsg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: kafka.PartitionAny},
		Key:            []byte(key),
		Value:          data,
		Headers: []kafka.Header{
			{Key: organization.OrgIDKey, Value: []byte(orgID.String())},
		},
		Timestamp: time.Now(),
	}

	// Publish with delivery report channel
	deliveryChan := make(chan kafka.Event, 1)
	err = p.producer.Produce(kafkaMsg, deliveryChan)
	if err != nil {
		return fmt.Errorf("failed to produce message: %w", err)
	}

	// Wait for delivery confirmation or context cancellation
	select {
	case e := <-deliveryChan:
		m, ok := e.(*kafka.Message)
		if !ok {
			return fmt.Errorf("%w: %T", ErrUnexpectedEvent, e)
		}
		if m.TopicPartition.Error != nil {
			return fmt.Errorf("delivery failed: %w", m.TopicPartition.Error)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("publish cancelled: %w", ctx.Err())
	}
}
