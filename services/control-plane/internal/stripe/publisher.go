package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/twmb/franz-go/pkg/kgo"
)

// KafkaPublisher errors.
var (
	ErrEmptyBootstrapServers = errors.New("bootstrap servers cannot be empty")
	ErrEmptyTopic            = errors.New("topic cannot be empty")
)

// KafkaPublisher publishes payment events to Kafka.
type KafkaPublisher struct {
	client *kgo.Client
	topic  string
}

// KafkaPublisherConfig contains configuration for the Kafka publisher.
type KafkaPublisherConfig struct {
	// BootstrapServers is the comma-separated list of Kafka broker addresses.
	BootstrapServers string
	// Topic is the Kafka topic for payment events.
	Topic string
	// ClientID identifies this producer.
	ClientID string
}

// NewKafkaPublisher creates a new Kafka-based EventPublisher.
func NewKafkaPublisher(cfg KafkaPublisherConfig) (*KafkaPublisher, error) {
	brokers := splitBrokers(cfg.BootstrapServers)
	if len(brokers) == 0 {
		return nil, ErrEmptyBootstrapServers
	}
	if cfg.Topic == "" {
		return nil, ErrEmptyTopic
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordRetries(3),
		kgo.ProducerLinger(10 * time.Millisecond),
	}
	if cfg.ClientID != "" {
		opts = append(opts, kgo.ClientID(cfg.ClientID))
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka producer: %w", err)
	}

	return &KafkaPublisher{
		client: client,
		topic:  cfg.Topic,
	}, nil
}

// PublishPaymentEvent publishes a payment event to the configured Kafka topic.
// The tenant_id is used as the partition key for ordering guarantees.
func (p *KafkaPublisher) PublishPaymentEvent(ctx context.Context, event *PaymentEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal payment event: %w", err)
	}

	record := &kgo.Record{
		Topic:     p.topic,
		Key:       []byte(event.TenantID),
		Value:     data,
		Timestamp: event.Timestamp,
		Headers: []kgo.RecordHeader{
			{Key: tenant.TenantIDKey, Value: []byte(event.TenantID)},
			{Key: "event-type", Value: []byte(event.EventType)},
			{Key: "stripe-event-id", Value: []byte(event.StripeEventID)},
			{Key: "idempotency-key", Value: []byte(event.IdempotencyKey)},
		},
	}

	results := p.client.ProduceSync(ctx, record)
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("kafka delivery failed: %w", err)
	}

	return nil
}

// Close closes the Kafka producer.
func (p *KafkaPublisher) Close() {
	p.client.Close()
}

// splitBrokers splits a comma-separated broker string into individual addresses,
// trimming whitespace and filtering empty entries.
func splitBrokers(s string) []string {
	parts := strings.Split(s, ",")
	brokers := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			brokers = append(brokers, p)
		}
	}
	return brokers
}
