// Package service provides application services for the Market Information service.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// ObservationRecordedTopic is the Kafka topic for observation recorded events.
	// References the centralized topic registry in shared/platform/events/topics.
	ObservationRecordedTopic = topics.MarketInformationObservationRecordedV1

	// DeprecatedObservationRecordedTopic is the old topic name retained for
	// dual-publish during migration.
	DeprecatedObservationRecordedTopic = "meridian.market_information.v1.ObservationRecorded"
)

var (
	// ErrNilProducer is returned when the Kafka producer is nil.
	ErrNilProducer = errors.New("kafka producer cannot be nil")
	// ErrNilObservation is returned when the observation is nil.
	ErrNilObservation = errors.New("observation cannot be nil")
	// ErrUnsupportedEventType is returned when Publish receives an unsupported event type.
	ErrUnsupportedEventType = errors.New("unsupported event type")
)

// ObservationEventPublisher defines the interface for publishing observation-related events.
// This interface allows the service layer to remain decoupled from the Kafka implementation.
type ObservationEventPublisher interface {
	// PublishObservationRecorded publishes an event when a new observation is recorded.
	// The tenant context must be set in ctx for multi-tenant event isolation.
	// Returns an error if the event cannot be published.
	PublishObservationRecorded(ctx context.Context, observation domain.MarketPriceObservation) error

	// Close releases resources held by the publisher.
	// Should be called during application shutdown.
	Close()

	// FlushWithTimeout waits for all outstanding messages to be delivered.
	// Returns the number of messages still in flight after the timeout.
	FlushWithTimeout(timeoutMs int) int
}

// protoPublisher is an interface for publishing protobuf messages to Kafka.
// This interface allows the KafkaObservationPublisher to be unit-tested without
// requiring a real Kafka connection.
type protoPublisher interface {
	// PublishWithTenant publishes a protobuf message with tenant context
	// extracted from ctx and injected as a Kafka header (x-tenant-id).
	PublishWithTenant(ctx context.Context, topic, key string, msg proto.Message) error
	// FlushWithTimeout waits for outstanding messages to be delivered with timeout.
	// Returns number of messages still in flight (0 = all delivered).
	FlushWithTimeout(timeoutMs int) int
	// Close closes the producer
	Close()
}

// KafkaObservationPublisher publishes observation domain events to Kafka topics.
// It uses the protoPublisher interface for reliable message delivery with tenant isolation.
type KafkaObservationPublisher struct {
	producer        protoPublisher
	topic           string
	deprecatedTopic string
}

// NewKafkaObservationPublisher creates a new Kafka-based observation event publisher.
// The producer must be configured with appropriate retry and acknowledgment settings
// for production use.
//
// The producer parameter can be any implementation of protoPublisher
// (typically *kafka.ProtoProducer).
func NewKafkaObservationPublisher(producer protoPublisher) (*KafkaObservationPublisher, error) {
	if producer == nil {
		return nil, ErrNilProducer
	}

	return &KafkaObservationPublisher{
		producer:        producer,
		topic:           ObservationRecordedTopic,
		deprecatedTopic: DeprecatedObservationRecordedTopic,
	}, nil
}

// PublishObservationRecorded publishes an ObservationRecorded event when a new
// observation is recorded in the system.
//
// The event is published to the ObservationRecordedTopic with the dataset code as
// the partition key to ensure ordering of events for the same dataset.
//
// The method maps the domain MarketPriceObservation to the proto ObservationRecorded
// event message before publishing.
func (p *KafkaObservationPublisher) PublishObservationRecorded(
	ctx context.Context,
	observation domain.MarketPriceObservation,
) error {
	// Map domain observation to proto event
	event := mapObservationToProtoEvent(observation)

	// Use dataset_code as partition key for ordering within the same dataset
	// This ensures observations for the same dataset are processed in order
	partitionKey := observation.DataSetCode()

	// Publish with tenant headers (extracted from context)
	if err := p.producer.PublishWithTenant(ctx, p.topic, partitionKey, event); err != nil {
		return fmt.Errorf("failed to publish ObservationRecorded event for observation %s: %w",
			observation.ID().String(), err)
	}

	// Dual-publish to deprecated topic for migration backwards compatibility
	if p.deprecatedTopic != "" {
		if err := p.producer.PublishWithTenant(ctx, p.deprecatedTopic, partitionKey, event); err != nil {
			slog.Warn("failed to publish ObservationRecorded to deprecated topic",
				"topic", p.deprecatedTopic,
				"error", err,
			)
		}
	}

	return nil
}

// Close closes the underlying Kafka producer and releases resources.
// Should be called during application shutdown. This does not wait for
// outstanding messages - call FlushWithTimeout first if needed.
func (p *KafkaObservationPublisher) Close() {
	p.producer.Close()
}

// FlushWithTimeout waits for all outstanding messages to be delivered.
// Returns the number of messages still in flight after the timeout.
func (p *KafkaObservationPublisher) FlushWithTimeout(timeoutMs int) int {
	return p.producer.FlushWithTimeout(timeoutMs)
}

// Publish implements the EventPublisher interface by type-switching on the event
// and delegating to the appropriate publish method.
// Currently supports *marketinformationv1.ObservationRecorded events.
// Returns ErrUnsupportedEventType for unsupported event types.
func (p *KafkaObservationPublisher) Publish(ctx context.Context, event any) error {
	switch e := event.(type) {
	case *marketinformationv1.ObservationRecorded:
		// Use dataset_code as partition key for ordering within the same dataset
		partitionKey := e.DatasetCode
		if err := p.producer.PublishWithTenant(ctx, p.topic, partitionKey, e); err != nil {
			return fmt.Errorf("failed to publish ObservationRecorded event: %w", err)
		}
		// Dual-publish to deprecated topic for migration backwards compatibility
		if p.deprecatedTopic != "" {
			if err := p.producer.PublishWithTenant(ctx, p.deprecatedTopic, partitionKey, e); err != nil {
				slog.Warn("failed to publish ObservationRecorded to deprecated topic",
					"topic", p.deprecatedTopic,
					"error", err,
				)
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedEventType, event)
	}
}

// mapObservationToProtoEvent converts a domain MarketPriceObservation to a proto
// ObservationRecorded event message.
func mapObservationToProtoEvent(obs domain.MarketPriceObservation) *marketinformationv1.ObservationRecorded {
	event := &marketinformationv1.ObservationRecorded{
		ObservationId:      obs.ID().String(),
		DatasetCode:        obs.DataSetCode(),
		ResolutionKeyValue: obs.ResolutionKey(),
		ObservedAt:         timestamppb.New(obs.ObservedAt()),
		Quality:            mapQualityLevelToProto(obs.QualityLevel()),
		Value:              obs.Value().String(),
		SourceId:           obs.SourceID().String(),
		RecordedAt:         timestamppb.New(obs.CreatedAt()),
	}

	// Note: The proto event has supersedes_observation_id to track what this observation
	// supersedes. However, the domain model tracks the inverse (SupersededBy - what replaced
	// this observation). If the caller needs to set supersedes_observation_id, they should
	// handle it at the application layer where this relationship is known from the request.

	return event
}

// mapQualityLevelToProto converts a domain QualityLevel to a proto QualityLevel.
func mapQualityLevelToProto(level domain.QualityLevel) marketinformationv1.QualityLevel {
	switch level {
	case domain.QualityLevelEstimate:
		return marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE
	case domain.QualityLevelActual:
		return marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL
	case domain.QualityLevelVerified:
		// Map Verified to ACTUAL since proto has ESTIMATE, PROVISIONAL, ACTUAL, REVISED
		// The domain QualityLevelVerified is semantically closest to ACTUAL
		return marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL
	default:
		return marketinformationv1.QualityLevel_QUALITY_LEVEL_UNSPECIFIED
	}
}
