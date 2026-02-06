// Package kafka provides generic Kafka producer and consumer utilities for Protocol Buffer messages.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrEmptyTopic is returned when topic name is empty.
	ErrEmptyTopic = errors.New("topic cannot be empty")
	// ErrNilMessage is returned when message is nil.
	ErrNilMessage = errors.New("message cannot be nil")
)

// ProtoProducer handles publishing Protocol Buffer messages to Kafka topics.
// It provides synchronous publishing with delivery confirmation, ensuring reliable
// message delivery to Kafka brokers. The producer uses configurable acks, retries,
// and compression for production-grade performance and durability.
type ProtoProducer struct {
	// client is the underlying franz-go client instance
	client *kgo.Client
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

	// Build franz-go options
	opts := []kgo.Opt{
		kgo.SeedBrokers(strings.Split(config.BootstrapServers, ",")...),
		kgo.RecordRetries(config.Retries),
		kgo.ProducerLinger(10 * time.Millisecond),
		kgo.ProducerBatchMaxBytes(16384),
	}

	// Set client ID if provided
	if config.ClientID != "" {
		opts = append(opts, kgo.ClientID(config.ClientID))
	}

	// Set acks level
	switch config.Acks {
	case "all", "-1":
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()))
	case "1":
		opts = append(opts, kgo.RequiredAcks(kgo.LeaderAck()))
	case "0":
		opts = append(opts, kgo.RequiredAcks(kgo.NoAck()))
	default:
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()))
	}

	// Set compression
	switch config.Compression {
	case "snappy":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.SnappyCompression()))
	case "gzip":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.GzipCompression()))
	case "lz4":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.Lz4Compression()))
	case "zstd":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.ZstdCompression()))
	case "none":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.NoCompression()))
	default:
		opts = append(opts, kgo.ProducerBatchCompression(kgo.SnappyCompression()))
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka producer: %w", err)
	}

	return &ProtoProducer{client: client}, nil
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

	// Create Kafka record
	record := &kgo.Record{
		Topic:     topic,
		Key:       []byte(key),
		Value:     data,
		Timestamp: time.Now(),
	}

	// Publish synchronously and wait for confirmation
	results := p.client.ProduceSync(ctx, record)
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("delivery failed: %w", err)
	}

	return nil
}

// Flush waits for all outstanding messages to be delivered using a context with timeout.
// This should be called before shutting down the producer to ensure no messages are lost.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
//
// Returns an error if flushing fails or context is cancelled.
func (p *ProtoProducer) Flush(ctx context.Context) error {
	return p.client.Flush(ctx)
}

// FlushWithTimeout waits for all outstanding messages to be delivered.
// This is a convenience method that creates a context with the specified timeout.
//
// Parameters:
// - timeoutMs: Maximum time to wait in milliseconds
//
// Returns the number of messages still in flight after the timeout.
// A return value of 0 indicates all messages were successfully delivered.
func (p *ProtoProducer) FlushWithTimeout(timeoutMs int) int {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	if err := p.client.Flush(ctx); err != nil {
		// Return 1 to indicate flush didn't complete successfully
		// This maintains backward compatibility with the old Flush(int) int signature
		return 1
	}
	return 0
}

// Close closes the producer and releases resources.
// This method should be called when the producer is no longer needed to free up
// network connections and other system resources. It does not wait for outstanding
// messages - call Flush() first if needed.
func (p *ProtoProducer) Close() {
	p.client.Close()
}

// ProduceRecord sends a raw Kafka record synchronously with delivery confirmation.
// This is used by the event outbox worker to publish pre-serialized event payloads.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
// - record: Pre-built Kafka record with topic, key, value, and optional headers
//
// Returns an error if the message cannot be delivered.
func (p *ProtoProducer) ProduceRecord(ctx context.Context, record *kgo.Record) error {
	results := p.client.ProduceSync(ctx, record)
	return results.FirstErr()
}

// PublishWithTenant sends a protobuf message with tenant context to the specified Kafka topic.
// The tenant ID is extracted from the context and injected as a Kafka header (x-tenant-id).
// This ensures tenant isolation for multi-tenant event processing.
//
// The key is used for partitioning - messages with the same key go to the same partition.
// This method blocks until the message is confirmed by the Kafka broker or the context is cancelled.
//
// Parameters:
// - ctx: Context containing tenant ID (via tenant.WithTenant) for cancellation and timeout
// - topic: Target Kafka topic name (must not be empty)
// - key: Partition key as string (empty key will be null in Kafka)
// - msg: Protocol Buffer message to serialize and send (must not be nil)
//
// Returns an error if:
// - tenant context is missing (tenant.ErrMissingTenantContext)
// - topic is empty
// - msg is nil
// - protobuf marshaling fails
// - message production fails
// - delivery confirmation indicates failure
// - context is cancelled before delivery confirmation
func (p *ProtoProducer) PublishWithTenant(ctx context.Context, topic string, key string, msg proto.Message) error {
	// Extract tenant from context - return error if missing
	orgID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		return fmt.Errorf("cannot publish without tenant context: %w", err)
	}

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

	// Create Kafka record with tenant header
	record := &kgo.Record{
		Topic:     topic,
		Key:       []byte(key),
		Value:     data,
		Timestamp: time.Now(),
		Headers: []kgo.RecordHeader{
			{Key: tenant.TenantIDKey, Value: []byte(orgID.String())},
		},
	}

	// Publish synchronously and wait for confirmation
	results := p.client.ProduceSync(ctx, record)
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("delivery failed: %w", err)
	}

	return nil
}
