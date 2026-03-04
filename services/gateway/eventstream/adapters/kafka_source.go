// Package adapters provides EventSource adapters for the gateway event streaming pipeline.
package adapters

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/gateway/eventstream"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Kafka header key constants for event metadata.
const (
	headerEventID       = "event_id"
	headerEventType     = "event_type"
	headerAggregateType = "aggregate_type"
	headerAggregateID   = "aggregate_id"
	headerCorrelationID = "correlation_id"
	headerCausationID   = "causation_id"
	headerChainDepth    = "x-meridian-chain-depth"

	// ConsumerGroupID is the Kafka consumer group used by KafkaEventSource.
	ConsumerGroupID = "ops-console-events"
)

// Sentinel errors returned by KafkaEventSource constructors.
var (
	// ErrEmptyBootstrapServers is returned when bootstrapServers is empty.
	ErrEmptyBootstrapServers = errors.New("bootstrapServers cannot be empty")

	// ErrEmptyTopics is returned when the topics slice is empty.
	ErrEmptyTopics = errors.New("topics cannot be empty")

	// ErrMissingEventTypeHeader is returned when the event_type header is absent.
	ErrMissingEventTypeHeader = errors.New("missing required header \"event_type\"")

	// ErrMissingHeader is returned when a required Kafka record header is absent.
	ErrMissingHeader = errors.New("required header not found")
)

// KafkaEventSource implements EventSource by consuming from one or more Kafka topics.
// It uses a dedicated consumer group ("ops-console-events") and starts consuming
// from the latest offset so only events produced after startup are delivered.
//
// Payload conversion: protobuf bytes are JSON-encoded using base64 when the raw bytes
// are not valid JSON (binary protobuf), or passed through verbatim when already JSON.
// This keeps the gateway format-agnostic while preserving the payload faithfully.
type KafkaEventSource struct {
	client *kgo.Client
	topics []string
	logger *slog.Logger
}

// NewKafkaEventSource creates a KafkaEventSource that consumes the given topics from
// the specified bootstrap servers. The consumer group is fixed to "ops-console-events"
// and offsets start at the end so only new events are delivered to gateway clients.
//
// Returns an error if bootstrapServers is empty, topics is empty, or the underlying
// Kafka client cannot be initialized.
func NewKafkaEventSource(
	bootstrapServers string,
	topics []string,
	logger *slog.Logger,
) (*KafkaEventSource, error) {
	if bootstrapServers == "" {
		return nil, ErrEmptyBootstrapServers
	}
	if len(topics) == 0 {
		return nil, ErrEmptyTopics
	}
	if logger == nil {
		logger = slog.Default()
	}

	rawBrokers := strings.Split(bootstrapServers, ",")
	brokers := make([]string, 0, len(rawBrokers))
	for _, b := range rawBrokers {
		if b = strings.TrimSpace(b); b != "" {
			brokers = append(brokers, b)
		}
	}
	if len(brokers) == 0 {
		return nil, ErrEmptyBootstrapServers
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(ConsumerGroupID),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()),
		kgo.BlockRebalanceOnPoll(),
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka client: %w", err)
	}

	return &KafkaEventSource{
		client: client,
		topics: topics,
		logger: logger,
	}, nil
}

// Start consumes events from the configured Kafka topics and delivers each one to handler.
// It blocks until ctx is cancelled or a fatal error occurs. On context cancellation it
// flushes pending work, closes the client, and returns nil.
//
// handler is called synchronously in the poll goroutine. Handlers that are slow should
// dispatch work asynchronously to avoid blocking the consumer loop.
func (s *KafkaEventSource) Start(ctx context.Context, handler eventstream.EventHandler) error {
	defer s.client.Close()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		fetches := s.client.PollFetches(ctx)

		if errs := fetches.Errors(); len(errs) > 0 {
			for _, fe := range errs {
				if errors.Is(fe.Err, context.Canceled) || errors.Is(fe.Err, context.DeadlineExceeded) {
					continue
				}
				s.logger.Warn("kafka fetch error",
					"topic", fe.Topic,
					"partition", fe.Partition,
					"error", fe.Err)
			}
		}

		fetches.EachRecord(func(record *kgo.Record) {
			event, err := s.recordToDomainEvent(record)
			if err != nil {
				s.logger.Error("failed to convert kafka record to domain event",
					"topic", record.Topic,
					"partition", record.Partition,
					"offset", record.Offset,
					"error", err)
				return
			}

			if err := handler(ctx, event); err != nil {
				s.logger.Error("event handler error",
					"topic", record.Topic,
					"event_id", event.EventID,
					"event_type", event.EventType,
					"error", err)
			}
		})

		s.client.AllowRebalance()
	}
}

// recordToDomainEvent converts a raw Kafka record to a DomainEvent by:
//  1. Extracting the tenant ID from the "x-tenant-id" header.
//  2. Extracting event metadata from well-known headers.
//  3. Converting the protobuf payload to a JSON-safe representation.
func (s *KafkaEventSource) recordToDomainEvent(record *kgo.Record) (eventstream.DomainEvent, error) {
	// Extract tenant ID — required for downstream routing.
	tenantID, err := extractHeader(record, tenant.TenantIDKey)
	if err != nil {
		return eventstream.DomainEvent{}, fmt.Errorf("missing required header %q: %w", tenant.TenantIDKey, err)
	}

	eventType := extractHeaderValue(record, headerEventType)
	if eventType == "" {
		return eventstream.DomainEvent{}, ErrMissingEventTypeHeader
	}

	eventID := extractHeaderValue(record, headerEventID)
	aggregateType := extractHeaderValue(record, headerAggregateType)
	aggregateID := extractHeaderValue(record, headerAggregateID)
	correlationID := extractHeaderValue(record, headerCorrelationID)
	causationID := extractHeaderValue(record, headerCausationID)
	chainDepth := extractChainDepth(record)

	payload := encodePayload(record.Value)

	ts := record.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	channel, err := eventstream.DeriveChannel(record.Topic)
	if err != nil {
		return eventstream.DomainEvent{}, fmt.Errorf("failed to derive channel from topic %q: %w", record.Topic, err)
	}

	// Build the event ID from the header if present; otherwise leave it for
	// NewDomainEvent to generate a fresh UUID via NewDomainEvent.
	// We prefer the original event_id from the producer for idempotency.
	event, err := eventstream.NewDomainEvent(
		eventType,
		record.Topic,
		aggregateID,
		aggregateType,
		tenantID,
		correlationID,
		causationID,
		payload,
	)
	if err != nil {
		return eventstream.DomainEvent{}, fmt.Errorf("failed to build domain event: %w", err)
	}

	// Override EventID and Channel with values from the record.
	// NewDomainEvent generates a new UUID; we want the producer's original ID.
	if eventID != "" {
		event.EventID = eventID
	}
	event.Channel = channel
	event.Timestamp = ts.UTC()
	event.ChainDepth = chainDepth

	return event, nil
}

// extractHeader returns the value of the first header matching key, or ErrMissingHeader if absent.
func extractHeader(record *kgo.Record, key string) (string, error) {
	v := extractHeaderValue(record, key)
	if v == "" {
		return "", fmt.Errorf("%w: %q", ErrMissingHeader, key)
	}
	return v, nil
}

// extractHeaderValue returns the value of the first header matching key, or "" if absent.
func extractHeaderValue(record *kgo.Record, key string) string {
	for _, h := range record.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// extractChainDepth reads the x-meridian-chain-depth header from the Kafka record.
// Returns 0 if the header is absent or cannot be parsed as an integer.
func extractChainDepth(record *kgo.Record) int {
	v := extractHeaderValue(record, headerChainDepth)
	if v == "" {
		return 0
	}
	depth, err := strconv.Atoi(v)
	if err != nil || depth < 0 {
		return 0
	}
	return depth
}

// incrementChainDepth returns depth+1 for use when publishing saga-triggered events.
// It is exported so that saga dispatch components can propagate the chain depth
// without importing kafka internals.
func incrementChainDepth(depth int) int {
	return depth + 1
}

// encodePayload converts raw bytes to a JSON-safe representation.
// If the bytes are valid JSON they are embedded verbatim; otherwise they are
// base64-encoded and wrapped in a JSON string to preserve the binary content.
func encodePayload(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}

	// If already valid JSON, embed as-is (services that serialize to JSON directly).
	if json.Valid(raw) {
		return raw
	}

	// Binary protobuf: encode as base64 JSON string so the gateway can forward
	// without losing data. Clients that understand the proto schema can decode.
	encoded := base64.StdEncoding.EncodeToString(raw)
	quoted, _ := json.Marshal(encoded)
	return quoted
}
