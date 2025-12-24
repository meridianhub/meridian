// Package messaging provides Kafka consumer adapters for audit event consumption.
package messaging

import (
	"context"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/services/utilization-metering-consumer/domain"
	"github.com/meridianhub/meridian/shared/platform/kafka"
)

// AuditConsumer consumes audit events from Kafka and transforms them
// into utilization measurements for billing.
type AuditConsumer struct {
	// consumer will be initialized in subtask 18.2
	// consumer    *kafka.ProtoConsumer
	transformer *domain.AuditEventTransformer
	pkClient    domain.PositionKeepingClient
}

// NewAuditConsumer creates a new audit event consumer.
// TODO: Implement in subsequent subtask (18.2)
func NewAuditConsumer(
	_ kafka.ConsumerConfig,
	transformer *domain.AuditEventTransformer,
	pkClient domain.PositionKeepingClient,
) (*AuditConsumer, error) {
	// Stub implementation - will be filled in subtask 18.2
	return &AuditConsumer{
		transformer: transformer,
		pkClient:    pkClient,
	}, nil
}

// HandleAuditEvent processes a single audit event.
// TODO: Implement in subsequent subtask (18.2)
func (ac *AuditConsumer) HandleAuditEvent(_ context.Context, _ *auditv1.AuditEvent) error {
	// Stub implementation - will be filled in subtask 18.2
	return nil
}

// Start begins consuming audit events from the specified topics.
// TODO: Implement in subsequent subtask (18.2)
func (ac *AuditConsumer) Start(_ []string) error {
	// Stub implementation - will be filled in subtask 18.2
	return nil
}

// Stop gracefully stops the consumer.
// TODO: Implement in subsequent subtask (18.2)
func (ac *AuditConsumer) Stop() {
	// Stub implementation - will be filled in subtask 18.2
}

// Close closes the consumer and releases resources.
// TODO: Implement in subsequent subtask (18.2)
func (ac *AuditConsumer) Close() error {
	// Stub implementation - will be filled in subtask 18.2
	return nil
}
