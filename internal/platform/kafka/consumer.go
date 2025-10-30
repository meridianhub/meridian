package kafka

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"google.golang.org/protobuf/proto"
)

// MessageHandler is called for each message received from Kafka.
// The handler should return an error if the message cannot be processed.
// Errors are logged but do not stop the consumer - implement dead letter queue
// or retry logic in production for better error handling.
//
// Parameters:
// - ctx: Context with timeout (default 30s) for processing the message
// - key: Kafka message key as bytes (may be nil)
// - msg: Deserialized protobuf message
//
// Returns an error if message processing fails.
type MessageHandler func(ctx context.Context, key []byte, msg proto.Message) error

// ProtoConsumer handles consuming Protocol Buffer messages from Kafka topics.
// It provides automatic deserialization, error recovery, and graceful shutdown.
// The consumer runs in a blocking loop until Stop() is called or an unrecoverable error occurs.
type ProtoConsumer struct {
	// consumer is the underlying confluent-kafka-go consumer instance
	consumer *kafka.Consumer
	// msgFactory creates new instances of the protobuf message type for deserialization
	msgFactory func() proto.Message
	// handler processes each consumed message
	handler MessageHandler
	// pollTimeout is the duration to wait for messages before checking shutdown signal
	pollTimeout time.Duration
	// handlerTimeout is the maximum duration for processing a single message
	handlerTimeout time.Duration
	// enableAutoCommit indicates whether Kafka handles commits automatically
	enableAutoCommit bool
	// wg tracks the Subscribe goroutine for graceful shutdown
	wg sync.WaitGroup
	// ctx provides cancellation signal for graceful shutdown
	ctx context.Context
	// cancel triggers shutdown of the consumer loop
	cancel context.CancelFunc
}

// ConsumerConfig contains configuration for creating a Kafka consumer.
// All fields except EnableAutoCommit have defaults applied if empty.
type ConsumerConfig struct {
	// BootstrapServers is the comma-separated list of Kafka broker addresses (required).
	BootstrapServers string
	// GroupID identifies the consumer group for coordinated consumption (required).
	GroupID string
	// ClientID identifies the consumer for logging and metrics (optional).
	ClientID string
	// AutoOffsetReset determines where to start consuming if no offset exists:
	// "earliest" (default) starts from beginning, "latest" starts from end.
	AutoOffsetReset string
	// EnableAutoCommit when true enables automatic offset commits, when false
	// offsets are committed manually after successful message processing.
	EnableAutoCommit bool
	// PollTimeout is the duration to wait for new messages before checking shutdown signal.
	// Default: 100ms. Lower values improve shutdown responsiveness, higher values reduce CPU.
	PollTimeout time.Duration
	// HandlerTimeout is the maximum duration for processing a single message.
	// Default: 30s. Handlers exceeding this timeout will be cancelled.
	HandlerTimeout time.Duration
}

var (
	// ErrEmptyGroupID is returned when group ID is empty.
	ErrEmptyGroupID = errors.New("group ID cannot be empty")
	// ErrNilMsgFactory is returned when message factory is nil.
	ErrNilMsgFactory = errors.New("message factory cannot be nil")
	// ErrNilHandler is returned when message handler is nil.
	ErrNilHandler = errors.New("message handler cannot be nil")
	// ErrEmptyTopics is returned when topics list is empty.
	ErrEmptyTopics = errors.New("topics cannot be empty")
)

// NewProtoConsumer creates a new Kafka consumer for protobuf messages.
// The consumer requires a message factory to create typed protobuf instances for deserialization,
// and a handler to process each consumed message.
//
// Parameters:
// - config: Consumer configuration (BootstrapServers and GroupID are required)
// - msgFactory: Function that creates a new instance of the proto message type to deserialize into
// - handler: Function called for each consumed message
//
// Returns an error if:
// - BootstrapServers is empty
// - GroupID is empty
// - msgFactory is nil
// - handler is nil
// - underlying Kafka consumer fails to initialize
func NewProtoConsumer(config ConsumerConfig, msgFactory func() proto.Message, handler MessageHandler) (*ProtoConsumer, error) {
	if config.BootstrapServers == "" {
		return nil, ErrEmptyBootstrapServers
	}
	if config.GroupID == "" {
		return nil, ErrEmptyGroupID
	}
	if msgFactory == nil {
		return nil, ErrNilMsgFactory
	}
	if handler == nil {
		return nil, ErrNilHandler
	}

	// Set defaults
	if config.AutoOffsetReset == "" {
		config.AutoOffsetReset = "earliest"
	}
	if config.PollTimeout == 0 {
		config.PollTimeout = 100 * time.Millisecond
	}
	if config.HandlerTimeout == 0 {
		config.HandlerTimeout = 30 * time.Second
	}

	consumer, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers":  config.BootstrapServers,
		"group.id":           config.GroupID,
		"client.id":          config.ClientID,
		"auto.offset.reset":  config.AutoOffsetReset,
		"enable.auto.commit": config.EnableAutoCommit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka consumer: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &ProtoConsumer{
		consumer:         consumer,
		msgFactory:       msgFactory,
		handler:          handler,
		pollTimeout:      config.PollTimeout,
		handlerTimeout:   config.HandlerTimeout,
		enableAutoCommit: config.EnableAutoCommit,
		ctx:              ctx,
		cancel:           cancel,
	}, nil
}

// Subscribe starts consuming from the specified topics.
// This method blocks until Stop() is called or an unrecoverable error occurs.
// The consumer will:
// - Join the consumer group
// - Poll for messages with 100ms timeout
// - Deserialize protobuf messages using the factory
// - Call the handler with a 30s timeout
// - Commit offsets after successful processing (if auto-commit disabled)
// - Continue consuming even if handler returns error (errors are logged)
// - Exit gracefully when Stop() is called
//
// Parameters:
// - topics: List of Kafka topic names to consume from (must not be empty)
//
// Returns an error if:
// - topics list is empty
// - subscription fails
// - unrecoverable Kafka error occurs (timeouts are handled internally)
func (c *ProtoConsumer) Subscribe(topics []string) error {
	if len(topics) == 0 {
		return ErrEmptyTopics
	}

	err := c.consumer.SubscribeTopics(topics, nil)
	if err != nil {
		return fmt.Errorf("failed to subscribe to topics: %w", err)
	}

	// Track this goroutine for graceful shutdown
	c.wg.Add(1)
	defer c.wg.Done()

	// Start consuming loop
	for {
		select {
		case <-c.ctx.Done():
			return nil
		default:
			msg, err := c.consumer.ReadMessage(c.pollTimeout)
			if err != nil {
				// Timeout is expected, continue polling
				var kafkaErr kafka.Error
				if errors.As(err, &kafkaErr) && kafkaErr.Code() == kafka.ErrTimedOut {
					continue
				}
				return fmt.Errorf("consumer error: %w", err)
			}

			// Process message
			if err := c.processMessage(msg); err != nil {
				// Log error with full context for debugging and monitoring
				log.Printf("ERROR: Failed to process message from topic=%s partition=%d offset=%d: %v",
					*msg.TopicPartition.Topic,
					msg.TopicPartition.Partition,
					msg.TopicPartition.Offset,
					err)
				// Continue consuming - consider implementing:
				// - Dead letter queue for poison messages
				// - Exponential backoff retry policy
				// - Circuit breaker for downstream service failures
				// - Metrics/alerting for failure rates
				continue
			}

			// Commit offset manually if auto-commit is disabled
			if !c.enableAutoCommit {
				_, err = c.consumer.CommitMessage(msg)
				if err != nil {
					// Log commit failures - may indicate broker issues
					log.Printf("WARN: Failed to commit offset for topic=%s partition=%d offset=%d: %v",
						*msg.TopicPartition.Topic,
						msg.TopicPartition.Partition,
						msg.TopicPartition.Offset,
						err)
					// Continue consuming - offset will be reprocessed on restart
					continue
				}
			}
		}
	}
}

// processMessage deserializes and handles a Kafka message.
// This is an internal method that:
// 1. Creates a new protobuf message instance using the factory
// 2. Deserializes the Kafka message value into the proto message
// 3. Calls the handler with configured timeout context
//
// Returns an error if deserialization or handler execution fails.
func (c *ProtoConsumer) processMessage(kafkaMsg *kafka.Message) error {
	// Create new proto message instance
	protoMsg := c.msgFactory()

	// Deserialize from bytes
	if err := proto.Unmarshal(kafkaMsg.Value, protoMsg); err != nil {
		return fmt.Errorf("failed to unmarshal protobuf message: %w", err)
	}

	// Call handler with configured timeout
	ctx, cancel := context.WithTimeout(c.ctx, c.handlerTimeout)
	defer cancel()

	if err := c.handler(ctx, kafkaMsg.Key, protoMsg); err != nil {
		return fmt.Errorf("handler error: %w", err)
	}

	return nil
}

// Stop stops the consumer gracefully.
// This triggers the Subscribe() loop to exit and waits for it to finish.
// Safe to call multiple times.
func (c *ProtoConsumer) Stop() {
	c.cancel()
	c.wg.Wait()
}

// Close closes the consumer and releases resources.
// This calls Stop() to trigger graceful shutdown, waits for Subscribe() to exit,
// then closes the underlying Kafka consumer. Always call this when finished with
// the consumer to free network connections and other system resources.
//
// Returns an error if the underlying consumer close fails.
func (c *ProtoConsumer) Close() error {
	c.Stop()
	if err := c.consumer.Close(); err != nil {
		return fmt.Errorf("failed to close consumer: %w", err)
	}
	return nil
}
