// Package domain contains the core domain models for utilization metering.
package domain

import (
	"errors"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
)

// Transformer errors
var (
	// ErrInvalidAuditEvent is returned when an audit event cannot be transformed
	ErrInvalidAuditEvent = errors.New("invalid audit event")
)

// AuditEventTransformer transforms audit events into utilization measurements.
// This is the core business logic that determines what gets billed and how.
type AuditEventTransformer struct {
	// TODO: Add pricing rules, rate limits, and billing policies
}

// NewAuditEventTransformer creates a new transformer instance.
func NewAuditEventTransformer() *AuditEventTransformer {
	return &AuditEventTransformer{}
}

// Transform converts an audit event into a utilization measurement.
// Returns nil if the event should not be metered (e.g., internal operations).
func (t *AuditEventTransformer) Transform(event *auditv1.AuditEvent) (*UtilizationMeasurement, error) {
	if event == nil {
		return nil, ErrInvalidAuditEvent
	}

	// TODO: Implement transformation logic in subsequent subtasks
	// This is a stub implementation that will be filled in later
	// For now, extract tenant ID from metadata if available, otherwise use "unknown"
	tenantID := "unknown"
	if event.Metadata != nil {
		if tid, ok := event.Metadata["tenant_id"]; ok {
			tenantID = tid
		}
	}

	// Convert operation enum to string
	operationType := event.Operation.String()

	return &UtilizationMeasurement{
		TenantID:      tenantID,
		ServiceName:   event.SchemaName,
		OperationType: operationType,
		Quantity:      1, // Simple count for now
		UnitOfMeasure: "operation",
		Timestamp:     event.Timestamp.AsTime(), // Use event's timestamp for billing accuracy
		CorrelationID: event.CorrelationId,
	}, nil
}
