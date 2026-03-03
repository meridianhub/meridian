// Package messaging provides Kafka consumer adapters for event routing.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/services/event-router/domain"
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

// AuditConsumer consumes audit events from Kafka and delegates processing
// to the PlatformMeteringHandler for utilization measurement.
type AuditConsumer struct {
	consumer     *kafka.ProtoConsumer
	handler      *PlatformMeteringHandler
	mdsPublisher domain.UtilizationPublisher // held temporarily for deferred handler construction
	logger       *slog.Logger
}

// AuditConsumerOption configures optional behavior of the AuditConsumer.
type AuditConsumerOption func(*AuditConsumer)

// WithMDSPublisher sets the MDS publisher for dual-output fan-out.
// When set, transformed measurements are also published to MDS asynchronously.
// MDS failures are logged but do not block the PK path.
func WithMDSPublisher(pub domain.UtilizationPublisher) AuditConsumerOption {
	return func(ac *AuditConsumer) {
		ac.mdsPublisher = pub
	}
}

// NewAuditConsumer creates a new Kafka consumer for AuditEvent messages.
// It connects to Kafka using the provided configuration and sets up a
// PlatformMeteringHandler that transforms audit events into utilization
// measurements and sends them to Position Keeping and optionally to MDS.
func NewAuditConsumer(
	config kafka.ConsumerConfig,
	transformer *auditdomain.AuditEventTransformer,
	pkClient domain.PositionKeepingClient,
	opts ...AuditConsumerOption,
) (*AuditConsumer, error) {
	logger := slog.Default().With("component", "audit_consumer")

	ac := &AuditConsumer{
		logger: logger,
	}

	for _, opt := range opts {
		opt(ac)
	}

	// Build handler options from consumer options
	var handlerOpts []PlatformMeteringOption
	if ac.mdsPublisher != nil {
		handlerOpts = append(handlerOpts, WithMeteringMDSPublisher(ac.mdsPublisher))
	}

	handler, err := NewPlatformMeteringHandler(transformer, pkClient, handlerOpts...)
	if err != nil {
		return nil, err
	}
	ac.handler = handler

	// Message factory creates new AuditEvent instances for deserialization
	msgFactory := func() proto.Message {
		return &auditv1.AuditEvent{}
	}

	// Handler delegates to the PlatformMeteringHandler via EventHandler interface.
	// Channel is set to "audit.events" as this consumer always handles audit event topics.
	kafkaHandler := func(ctx context.Context, _ []byte, msg proto.Message) error {
		return ac.handler.Handle(ctx, "audit.events", msg, nil)
	}

	consumer, err := kafka.NewProtoConsumer(config, msgFactory, kafkaHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to create audit consumer: %w", err)
	}

	ac.consumer = consumer
	return ac, nil
}

// ErrNoTopics is returned when Start is called with an empty topics list.
var ErrNoTopics = errors.New("at least one topic is required")

// Start begins consuming AuditEvent messages from the specified topics.
func (ac *AuditConsumer) Start(topics []string) error {
	if len(topics) == 0 {
		return ErrNoTopics
	}
	ac.logger.Info("starting audit consumer",
		"topics", topics,
		"mds_enabled", ac.handler.HasMDSPublisher())
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
