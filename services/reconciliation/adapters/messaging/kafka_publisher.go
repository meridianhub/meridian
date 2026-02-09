// Package messaging provides Kafka-based event publishing for the reconciliation service.
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/services/reconciliation/observability"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Topic constants for reconciliation domain events.
const (
	TopicReconciliationRunStarted   = "reconciliation.run.started"
	TopicReconciliationRunCompleted = "reconciliation.run.completed"
	TopicVarianceDetected           = "reconciliation.variance.detected"
	TopicPositionLockRequested      = "reconciliation.position.lock.requested"
	TopicDisputeCreated             = "reconciliation.dispute.created"
	TopicDisputeResolved            = "reconciliation.dispute.resolved"
)

// KafkaPublisher publishes reconciliation domain events to Kafka topics.
// It wraps the shared platform ProtoProducer with JSON serialization and
// reconciliation-specific topic routing.
type KafkaPublisher struct {
	producer *kafka.ProtoProducer
	logger   *slog.Logger
}

// NewKafkaPublisher creates a new KafkaPublisher.
// The brokers parameter is a comma-separated list of Kafka broker addresses.
func NewKafkaPublisher(brokers string, logger *slog.Logger) (*KafkaPublisher, error) {
	if logger == nil {
		logger = slog.Default()
	}

	producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: brokers,
		ClientID:         "reconciliation-service",
		Acks:             "all",
		Retries:          3,
		Compression:      "snappy",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka producer: %w", err)
	}

	return &KafkaPublisher{
		producer: producer,
		logger:   logger,
	}, nil
}

// Publish sends a domain event to the specified topic with the tenant ID as
// partition key for cross-tenant isolation.
func (p *KafkaPublisher) Publish(ctx context.Context, topic string, event interface{}) error {
	start := time.Now()

	data, err := json.Marshal(event)
	if err != nil {
		observability.RecordKafkaPublish(topic, "marshal_error", time.Since(start))
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Extract partition key from event if it has a tenant/account ID
	key := extractPartitionKey(event)

	record := &kgo.Record{
		Topic:     topic,
		Key:       []byte(key),
		Value:     data,
		Timestamp: time.Now(),
	}

	if err := p.producer.ProduceRecord(ctx, record); err != nil {
		observability.RecordKafkaPublish(topic, "error", time.Since(start))
		p.logger.WarnContext(ctx, "failed to publish event to kafka",
			"topic", topic,
			"error", err,
		)
		return fmt.Errorf("failed to publish to %s: %w", topic, err)
	}

	observability.RecordKafkaPublish(topic, "success", time.Since(start))

	p.logger.DebugContext(ctx, "event published to kafka",
		"topic", topic,
		"key", key,
	)

	return nil
}

// Close flushes pending messages and closes the producer.
func (p *KafkaPublisher) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := p.producer.Flush(ctx); err != nil {
		p.logger.Warn("failed to flush kafka producer", "error", err)
	}
	p.producer.Close()
}

// extractPartitionKey extracts a partition key from the event for tenant isolation.
// It looks for common fields like AccountID or RunID.
func extractPartitionKey(event interface{}) string {
	type hasAccountID interface{ GetAccountID() string }
	if e, ok := event.(hasAccountID); ok {
		return e.GetAccountID()
	}

	// Try to extract from a map or struct with AccountID field via JSON round-trip
	data, err := json.Marshal(event)
	if err != nil {
		return ""
	}

	var fields map[string]interface{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return ""
	}

	// Prefer account_id for tenant isolation
	if v, ok := fields["account_id"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}

	// Fall back to run_id for run-scoped events
	if v, ok := fields["run_id"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}

	return ""
}

// NoopPublisher is a no-op implementation of EventPublisher for when Kafka is disabled.
type NoopPublisher struct {
	logger *slog.Logger
}

// NewNoopPublisher creates a new NoopPublisher that logs events instead of publishing.
func NewNoopPublisher(logger *slog.Logger) *NoopPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &NoopPublisher{logger: logger}
}

// Publish logs the event instead of publishing to Kafka.
func (p *NoopPublisher) Publish(_ context.Context, topic string, _ interface{}) error {
	p.logger.Debug("noop publisher: event discarded",
		"topic", topic,
	)
	return nil
}
