package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"google.golang.org/protobuf/proto"
)

// MessageHandler is called for each message received from Kafka.
// The handler should return an error if the message cannot be processed.
type MessageHandler func(ctx context.Context, key []byte, msg proto.Message) error

// ProtoConsumer handles consuming Protocol Buffer messages from Kafka topics.
type ProtoConsumer struct {
	consumer    *kafka.Consumer
	msgFactory  func() proto.Message
	handler     MessageHandler
	pollTimeout time.Duration
	ctx         context.Context
	cancel      context.CancelFunc
}

// ConsumerConfig contains configuration for creating a Kafka consumer.
type ConsumerConfig struct {
	BootstrapServers string
	GroupID          string
	ClientID         string
	AutoOffsetReset  string // "earliest", "latest"
	EnableAutoCommit bool
}

// NewProtoConsumer creates a new Kafka consumer for protobuf messages.
// msgFactory is a function that creates a new instance of the proto message type to deserialize into.
func NewProtoConsumer(config ConsumerConfig, msgFactory func() proto.Message, handler MessageHandler) (*ProtoConsumer, error) {
	if config.BootstrapServers == "" {
		return nil, fmt.Errorf("bootstrap servers cannot be empty")
	}
	if config.GroupID == "" {
		return nil, fmt.Errorf("group ID cannot be empty")
	}
	if msgFactory == nil {
		return nil, fmt.Errorf("message factory cannot be nil")
	}
	if handler == nil {
		return nil, fmt.Errorf("message handler cannot be nil")
	}

	// Set defaults
	if config.AutoOffsetReset == "" {
		config.AutoOffsetReset = "earliest"
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
		consumer:    consumer,
		msgFactory:  msgFactory,
		handler:     handler,
		pollTimeout: 100 * time.Millisecond,
		ctx:         ctx,
		cancel:      cancel,
	}, nil
}

// Subscribe starts consuming from the specified topics.
// This method blocks until Stop() is called or an unrecoverable error occurs.
func (c *ProtoConsumer) Subscribe(topics []string) error {
	if len(topics) == 0 {
		return fmt.Errorf("topics cannot be empty")
	}

	err := c.consumer.SubscribeTopics(topics, nil)
	if err != nil {
		return fmt.Errorf("failed to subscribe to topics: %w", err)
	}

	// Start consuming loop
	for {
		select {
		case <-c.ctx.Done():
			return nil
		default:
			msg, err := c.consumer.ReadMessage(c.pollTimeout)
			if err != nil {
				// Timeout is expected, continue polling
				if err.(kafka.Error).Code() == kafka.ErrTimedOut {
					continue
				}
				return fmt.Errorf("consumer error: %w", err)
			}

			// Process message
			if err := c.processMessage(msg); err != nil {
				// Log error but continue consuming
				// In production, implement dead letter queue or retry logic
				continue
			}

			// Commit offset if auto-commit is disabled
			_, err = c.consumer.CommitMessage(msg)
			if err != nil {
				// Log error but continue
				continue
			}
		}
	}
}

// processMessage deserializes and handles a Kafka message.
func (c *ProtoConsumer) processMessage(kafkaMsg *kafka.Message) error {
	// Create new proto message instance
	protoMsg := c.msgFactory()

	// Deserialize from bytes
	if err := proto.Unmarshal(kafkaMsg.Value, protoMsg); err != nil {
		return fmt.Errorf("failed to unmarshal protobuf message: %w", err)
	}

	// Call handler with context
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()

	if err := c.handler(ctx, kafkaMsg.Key, protoMsg); err != nil {
		return fmt.Errorf("handler error: %w", err)
	}

	return nil
}

// Stop stops the consumer gracefully.
func (c *ProtoConsumer) Stop() {
	c.cancel()
}

// Close closes the consumer and releases resources.
func (c *ProtoConsumer) Close() error {
	c.Stop()
	return c.consumer.Close()
}
