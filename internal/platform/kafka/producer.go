// Package kafka provides generic Kafka producer and consumer utilities for Protocol Buffer messages.
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
	// ErrEmptyBootstrapServers is returned when bootstrap servers configuration is empty.
	ErrEmptyBootstrapServers = errors.New("bootstrap servers cannot be empty")
	// ErrEmptyTopic is returned when topic name is empty.
	ErrEmptyTopic = errors.New("topic cannot be empty")
	// ErrNilMessage is returned when message is nil.
	ErrNilMessage = errors.New("message cannot be nil")
)

// ProtoProducer handles publishing Protocol Buffer messages to Kafka topics.
type ProtoProducer struct {
	producer *kafka.Producer
}

// ProducerConfig contains configuration for creating a Kafka producer.
type ProducerConfig struct {
	BootstrapServers string
	ClientID         string
	Acks             string // "all", "1", "0"
	Retries          int
	Compression      string // "none", "gzip", "snappy", "lz4", "zstd"
}

// NewProtoProducer creates a new Kafka producer for protobuf messages.
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
		m := e.(*kafka.Message)
		if m.TopicPartition.Error != nil {
			return fmt.Errorf("delivery failed: %w", m.TopicPartition.Error)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("publish cancelled: %w", ctx.Err())
	}
}

// Flush waits for all outstanding messages to be delivered.
func (p *ProtoProducer) Flush(timeoutMs int) int {
	return p.producer.Flush(timeoutMs)
}

// Close closes the producer and releases resources.
func (p *ProtoProducer) Close() {
	p.producer.Close()
}
