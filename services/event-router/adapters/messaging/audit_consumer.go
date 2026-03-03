// Package messaging provides Kafka consumer adapters for audit event consumption.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"buf.build/go/protovalidate"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/services/utilization-metering-consumer/domain"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrNilTransformer is returned when the transformer is nil
	ErrNilTransformer = errors.New("transformer cannot be nil")
	// ErrNilPositionKeepingClient is returned when the position keeping client is nil
	ErrNilPositionKeepingClient = errors.New("position keeping client cannot be nil")
	// ErrUnexpectedMessageType is returned when the message is not an AuditEvent
	ErrUnexpectedMessageType = errors.New("unexpected message type")
	// ErrInvalidAuditEvent is returned when the audit event fails validation
	ErrInvalidAuditEvent = errors.New("invalid audit event")
)

// AuditConsumer consumes audit events from Kafka and transforms them
// into utilization measurements for billing.
type AuditConsumer struct {
	consumer    *kafka.ProtoConsumer
	transformer *auditdomain.AuditEventTransformer
	pkClient    domain.PositionKeepingClient
	mdPublisher domain.UtilizationPublisher
	validator   protovalidate.Validator
	logger      *slog.Logger
}

// NewAuditConsumer creates a new Kafka consumer for AuditEvent messages.
// It connects to Kafka using the provided configuration and sets up a handler
// that transforms audit events into utilization measurements and sends them
// to the Position Keeping service and optionally to MDS.
//
// Parameters:
// - config: Kafka consumer configuration (bootstrap servers, group ID, etc.)
// - transformer: Service that transforms audit events into utilization measurements
// - pkClient: Client for communicating with Position Keeping service
// - opts: Optional configuration (e.g., WithMDSPublisher)
//
// Returns an error if the consumer cannot be initialized or if dependencies are nil.
func NewAuditConsumer(
	config kafka.ConsumerConfig,
	transformer *auditdomain.AuditEventTransformer,
	pkClient domain.PositionKeepingClient,
	opts ...AuditConsumerOption,
) (*AuditConsumer, error) {
	if transformer == nil {
		return nil, ErrNilTransformer
	}
	if pkClient == nil {
		return nil, ErrNilPositionKeepingClient
	}

	validator, err := protovalidate.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create validator: %w", err)
	}

	logger := slog.Default().With("component", "audit_consumer")

	ac := &AuditConsumer{
		transformer: transformer,
		pkClient:    pkClient,
		validator:   validator,
		logger:      logger,
	}

	for _, opt := range opts {
		opt(ac)
	}

	// Message factory creates new AuditEvent instances for deserialization
	msgFactory := func() proto.Message {
		return &auditv1.AuditEvent{}
	}

	// Handler converts Kafka messages to utilization measurements
	handler := func(ctx context.Context, _ []byte, msg proto.Message) error {
		event, ok := msg.(*auditv1.AuditEvent)
		if !ok {
			return fmt.Errorf("%w: expected *AuditEvent, got %T", ErrUnexpectedMessageType, msg)
		}
		return ac.handleAuditEvent(ctx, event)
	}

	consumer, err := kafka.NewProtoConsumer(config, msgFactory, handler)
	if err != nil {
		return nil, fmt.Errorf("failed to create audit consumer: %w", err)
	}

	ac.consumer = consumer
	return ac, nil
}

// AuditConsumerOption configures optional behavior of the AuditConsumer.
type AuditConsumerOption func(*AuditConsumer)

// WithMDSPublisher sets the MDS publisher for dual-output fan-out.
// When set, transformed measurements are also published to MDS asynchronously.
// MDS failures are logged but do not block the PK path.
func WithMDSPublisher(pub domain.UtilizationPublisher) AuditConsumerOption {
	return func(ac *AuditConsumer) {
		ac.mdPublisher = pub
	}
}

// handleAuditEvent processes a single AuditEvent by transforming it into
// a utilization measurement and sending it to the Position Keeping service
// for tenant-zero billing, and optionally to MDS for aggregation.
func (ac *AuditConsumer) handleAuditEvent(ctx context.Context, event *auditv1.AuditEvent) error {
	// Start timing for processing duration metric
	startTime := time.Now()

	// Validate proto message
	if err := ac.validator.Validate(event); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAuditEvent, err)
	}

	ac.logger.DebugContext(ctx, "processing audit event",
		"event_id", event.EventId,
		"schema", event.SchemaName,
		"table", event.TableName,
		"operation", event.Operation.String())

	// Derive topic from schema name (format: "<schema-name>.audit.events")
	// This matches the actual Kafka topic naming convention used in production
	topic := event.SchemaName + ".audit.events"

	// Record event consumption metric
	domain.RecordEventConsumed(event.SchemaName, topic)

	// Transform audit event to utilization measurement
	measurement, err := ac.transformer.Transform(event)
	if err != nil {
		domain.RecordTransformationError(event.SchemaName, "transformation_failed")
		return fmt.Errorf("failed to transform audit event %s: %w", event.EventId, err)
	}

	// If transformer returns nil, this event should not be metered (e.g., internal operations)
	if measurement == nil {
		ac.logger.DebugContext(ctx, "audit event not metered (filtered by transformer)",
			"event_id", event.EventId,
			"schema", event.SchemaName)
		return nil
	}

	// Send measurement to Position Keeping service (synchronous, failure short-circuits)
	pkStart := time.Now()
	if err := ac.pkClient.RecordMeasurement(ctx, measurement); err != nil {
		domain.RecordPositionKeepingAPIError("record_measurement_failed")
		domain.RecordDualOutputLatency("pk", time.Since(pkStart).Seconds())
		return fmt.Errorf("failed to record measurement for event %s: %w", event.EventId, err)
	}
	domain.RecordDualOutputLatency("pk", time.Since(pkStart).Seconds())

	// Record successful measurement metric
	domain.RecordMeasurementRecorded(event.SchemaName, measurement.AssetCode)

	// Fan out to MDS (async buffer, failure does not block)
	if ac.mdPublisher != nil {
		mdsStart := time.Now()
		ac.publishToMDS(measurement)
		domain.RecordDualOutputLatency("mds", time.Since(mdsStart).Seconds())
	}

	// Record processing duration metric
	duration := time.Since(startTime).Seconds()
	domain.RecordEventProcessingDuration(event.SchemaName, duration)

	ac.logger.InfoContext(ctx, "successfully recorded utilization measurement",
		"event_id", event.EventId,
		"account_id", measurement.AccountID,
		"asset_code", measurement.AssetCode,
		"service", event.SchemaName,
		"quantity", measurement.Quantity,
		"mds_enabled", ac.mdPublisher != nil)

	return nil
}

// publishToMDS converts the measurement and publishes it to MDS via the buffer.
// Errors are logged but do not propagate (eventual consistency for MDS).
func (ac *AuditConsumer) publishToMDS(measurement *auditdomain.Measurement) {
	defer func() {
		if r := recover(); r != nil {
			ac.logger.Error("panic in MDS publish",
				"error", r,
				"measurement_id", measurement.ID)
			domain.RecordMDSPublish("error")
		}
	}()

	utilMeasurement := domain.MeasurementToUtilization(measurement)
	ac.mdPublisher.Publish(utilMeasurement)
	domain.RecordMDSPublish("success")
}

// Start begins consuming AuditEvent messages from the specified topics.
// This method subscribes to topics and returns immediately. The underlying
// consumer runs in a separate goroutine managed by the platform kafka consumer.
//
// The consumer will:
// - Subscribe to all provided topics (typically 6 service audit topics)
// - Poll for messages (in background)
// - Deserialize protobuf messages
// - Validate using protovalidate
// - Transform to utilization measurements
// - Send to Position Keeping service
// - Optionally buffer to MDS publisher
// - Commit offsets after successful processing
//
// Parameters:
// - topics: List of Kafka topic names to consume from (e.g., ["audit.events.current-account.v1", ...])
//
// Returns an error if subscription fails. Call Stop() to gracefully shutdown consumption.
func (ac *AuditConsumer) Start(topics []string) error {
	ac.logger.Info("starting audit consumer", "topics", topics, "mds_enabled", ac.mdPublisher != nil)
	if err := ac.consumer.Subscribe(topics); err != nil {
		return fmt.Errorf("failed to subscribe to topics: %w", err)
	}
	return nil
}

// Stop gracefully stops the consumer.
func (ac *AuditConsumer) Stop() {
	ac.logger.Info("stopping audit consumer")
	ac.consumer.Stop()
}

// Close closes the consumer and releases resources.
func (ac *AuditConsumer) Close() error {
	if err := ac.consumer.Close(); err != nil {
		return fmt.Errorf("failed to close consumer: %w", err)
	}
	ac.logger.Info("audit consumer closed")
	return nil
}
