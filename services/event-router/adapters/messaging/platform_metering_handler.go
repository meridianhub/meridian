// Package messaging provides Kafka consumer adapters for event routing.
package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"buf.build/go/protovalidate"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/services/event-router/domain"
	"google.golang.org/protobuf/proto"
)

// PlatformMeteringHandler processes audit events by transforming them into
// utilization measurements for billing via Position Keeping and optionally MDS.
// It implements the domain.EventHandler interface.
type PlatformMeteringHandler struct {
	transformer *auditdomain.AuditEventTransformer
	pkClient    domain.PositionKeepingClient
	mdPublisher domain.UtilizationPublisher
	validator   protovalidate.Validator
	logger      *slog.Logger
}

// PlatformMeteringOption configures optional behavior of the PlatformMeteringHandler.
type PlatformMeteringOption func(*PlatformMeteringHandler)

// WithMeteringMDSPublisher sets the MDS publisher for dual-output fan-out.
// When set, transformed measurements are also published to MDS asynchronously.
// MDS failures are logged but do not block the PK path.
func WithMeteringMDSPublisher(pub domain.UtilizationPublisher) PlatformMeteringOption {
	return func(h *PlatformMeteringHandler) {
		h.mdPublisher = pub
	}
}

// NewPlatformMeteringHandler creates a handler that processes audit events into
// utilization measurements and sends them to Position Keeping and optionally MDS.
func NewPlatformMeteringHandler(
	transformer *auditdomain.AuditEventTransformer,
	pkClient domain.PositionKeepingClient,
	opts ...PlatformMeteringOption,
) (*PlatformMeteringHandler, error) {
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

	h := &PlatformMeteringHandler{
		transformer: transformer,
		pkClient:    pkClient,
		validator:   validator,
		logger:      slog.Default().With("component", "platform_metering_handler"),
	}

	for _, opt := range opts {
		opt(h)
	}

	return h, nil
}

// Handle processes a single event from the given channel.
// It expects the event to be an *auditv1.AuditEvent proto message.
func (h *PlatformMeteringHandler) Handle(ctx context.Context, channel string, event proto.Message, _ map[string]string) error {
	auditEvent, ok := event.(*auditv1.AuditEvent)
	if !ok {
		return fmt.Errorf("%w: expected *AuditEvent, got %T", ErrUnexpectedMessageType, event)
	}
	return h.handleAuditEvent(ctx, channel, auditEvent)
}

// handleAuditEvent processes a single AuditEvent by transforming it into
// a utilization measurement and sending it to the Position Keeping service
// for tenant-zero billing, and optionally to MDS for aggregation.
func (h *PlatformMeteringHandler) handleAuditEvent(ctx context.Context, channel string, event *auditv1.AuditEvent) error {
	startTime := time.Now()

	if err := h.validator.Validate(event); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAuditEvent, err)
	}

	h.logger.DebugContext(ctx, "processing audit event",
		"event_id", event.EventId,
		"schema", event.SchemaName,
		"table", event.TableName,
		"operation", event.Operation.String(),
		"channel", channel)

	// Use channel as topic label when available, otherwise derive from schema name
	topic := channel
	if topic == "" {
		topic = event.SchemaName + ".audit.events"
	}

	domain.RecordEventConsumed(event.SchemaName, topic)

	measurement, err := h.transformer.Transform(event)
	if err != nil {
		domain.RecordTransformationError(event.SchemaName, "transformation_failed")
		return fmt.Errorf("failed to transform audit event %s: %w", event.EventId, err)
	}

	if measurement == nil {
		h.logger.DebugContext(ctx, "audit event not metered (filtered by transformer)",
			"event_id", event.EventId,
			"schema", event.SchemaName)
		return nil
	}

	pkStart := time.Now()
	if err := h.pkClient.RecordMeasurement(ctx, measurement); err != nil {
		domain.RecordPositionKeepingAPIError("record_measurement_failed")
		domain.RecordDualOutputLatency("pk", time.Since(pkStart).Seconds())
		return fmt.Errorf("failed to record measurement for event %s: %w", event.EventId, err)
	}
	domain.RecordDualOutputLatency("pk", time.Since(pkStart).Seconds())

	domain.RecordMeasurementRecorded(event.SchemaName, measurement.AssetCode)

	if h.mdPublisher != nil {
		mdsStart := time.Now()
		h.publishToMDS(measurement)
		domain.RecordDualOutputLatency("mds", time.Since(mdsStart).Seconds())
	}

	duration := time.Since(startTime).Seconds()
	domain.RecordEventProcessingDuration(event.SchemaName, duration)

	h.logger.InfoContext(ctx, "successfully recorded utilization measurement",
		"event_id", event.EventId,
		"account_id", measurement.AccountID,
		"asset_code", measurement.AssetCode,
		"service", event.SchemaName,
		"quantity", measurement.Quantity,
		"mds_enabled", h.mdPublisher != nil)

	return nil
}

// publishToMDS converts the measurement and publishes it to MDS via the buffer.
func (h *PlatformMeteringHandler) publishToMDS(measurement *auditdomain.Measurement) {
	defer func() {
		if r := recover(); r != nil {
			h.logger.Error("panic in MDS publish",
				"error", r,
				"measurement_id", measurement.ID)
			domain.RecordMDSPublish("error")
		}
	}()

	utilMeasurement := domain.MeasurementToUtilization(measurement)
	h.mdPublisher.Publish(utilMeasurement)
	domain.RecordMDSPublish("success")
}

// HasMDSPublisher returns whether the handler has an MDS publisher configured.
func (h *PlatformMeteringHandler) HasMDSPublisher() bool {
	return h.mdPublisher != nil
}
