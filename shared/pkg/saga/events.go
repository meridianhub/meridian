// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EventType identifies the type of saga event.
//
type EventType string

const (
	// EventTypeProgress indicates a progress update event.
	EventTypeProgress EventType = "saga.progress.v1"

	// EventTypeStepCompleted indicates a step completed successfully.
	EventTypeStepCompleted EventType = "saga.step_completed.v1"

	// EventTypeStepFailed indicates a step failed.
	EventTypeStepFailed EventType = "saga.step_failed.v1"

	// EventTypeSagaCompleted indicates the entire saga completed.
	EventTypeSagaCompleted EventType = "saga.completed.v1"

	// EventTypeSagaFailed indicates the entire saga failed.
	EventTypeSagaFailed EventType = "saga.failed.v1"
)

// Event is the interface for all saga events.
//
type Event interface {
	// EventType returns the type identifier for this event.
	EventType() EventType

	// SagaID returns the saga instance ID.
	SagaID() uuid.UUID

	// GetCorrelationID returns the correlation ID for distributed tracing.
	GetCorrelationID() uuid.UUID
}

// ProgressEvent represents a progress update during saga execution.
// These events are emitted to provide visibility into long-running operations.
//
type ProgressEvent struct {
	SagaInstanceID uuid.UUID `json:"saga_instance_id"`
	CorrelationID  uuid.UUID `json:"correlation_id"`
	StepIndex      int       `json:"step_index"`
	StepName       string    `json:"step_name"`
	Percentage     int       `json:"percentage"`
	Message        string    `json:"message"`
	Timestamp      time.Time `json:"timestamp"`
}

// EventType implements Event.
func (e *ProgressEvent) EventType() EventType {
	return EventTypeProgress
}

// SagaID implements Event.
func (e *ProgressEvent) SagaID() uuid.UUID {
	return e.SagaInstanceID
}

// GetCorrelationID implements Event.
func (e *ProgressEvent) GetCorrelationID() uuid.UUID {
	return e.CorrelationID
}

// NewProgressEvent creates a new progress event.
func NewProgressEvent(
	sagaID uuid.UUID,
	correlationID uuid.UUID,
	stepIndex int,
	stepName string,
	percentage int,
	message string,
) *ProgressEvent {
	return &ProgressEvent{
		SagaInstanceID: sagaID,
		CorrelationID:  correlationID,
		StepIndex:      stepIndex,
		StepName:       stepName,
		Percentage:     percentage,
		Message:        message,
		Timestamp:      time.Now(),
	}
}

// StepCompletedEvent represents successful step completion.
//
type StepCompletedEvent struct {
	SagaInstanceID uuid.UUID `json:"saga_instance_id"`
	CorrelationID  uuid.UUID `json:"correlation_id"`
	CausationID    uuid.UUID `json:"causation_id"`
	StepIndex      int       `json:"step_index"`
	StepName       string    `json:"step_name"`
	Result         any       `json:"result,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
}

// EventType implements Event.
func (e *StepCompletedEvent) EventType() EventType {
	return EventTypeStepCompleted
}

// SagaID implements Event.
func (e *StepCompletedEvent) SagaID() uuid.UUID {
	return e.SagaInstanceID
}

// GetCorrelationID implements Event.
func (e *StepCompletedEvent) GetCorrelationID() uuid.UUID {
	return e.CorrelationID
}

// NewStepCompletedEvent creates a new step completed event.
func NewStepCompletedEvent(
	sagaID uuid.UUID,
	correlationID uuid.UUID,
	causationID uuid.UUID,
	stepIndex int,
	stepName string,
	result any,
) *StepCompletedEvent {
	return &StepCompletedEvent{
		SagaInstanceID: sagaID,
		CorrelationID:  correlationID,
		CausationID:    causationID,
		StepIndex:      stepIndex,
		StepName:       stepName,
		Result:         result,
		Timestamp:      time.Now(),
	}
}

// StepFailedEvent represents step failure.
//
type StepFailedEvent struct {
	SagaInstanceID uuid.UUID     `json:"saga_instance_id"`
	CorrelationID  uuid.UUID     `json:"correlation_id"`
	CausationID    uuid.UUID     `json:"causation_id"`
	StepIndex      int           `json:"step_index"`
	StepName       string        `json:"step_name"`
	ErrorMessage   string        `json:"error_message"`
	ErrorCategory  ErrorCategory `json:"error_category"`
	Timestamp      time.Time     `json:"timestamp"`
}

// EventType implements Event.
func (e *StepFailedEvent) EventType() EventType {
	return EventTypeStepFailed
}

// SagaID implements Event.
func (e *StepFailedEvent) SagaID() uuid.UUID {
	return e.SagaInstanceID
}

// GetCorrelationID implements Event.
func (e *StepFailedEvent) GetCorrelationID() uuid.UUID {
	return e.CorrelationID
}

// NewStepFailedEvent creates a new step failed event.
func NewStepFailedEvent(
	sagaID uuid.UUID,
	correlationID uuid.UUID,
	causationID uuid.UUID,
	stepIndex int,
	stepName string,
	errorMessage string,
	errorCategory ErrorCategory,
) *StepFailedEvent {
	return &StepFailedEvent{
		SagaInstanceID: sagaID,
		CorrelationID:  correlationID,
		CausationID:    causationID,
		StepIndex:      stepIndex,
		StepName:       stepName,
		ErrorMessage:   errorMessage,
		ErrorCategory:  errorCategory,
		Timestamp:      time.Now(),
	}
}

// EventPublisher publishes saga events to the event bus.
//
type EventPublisher interface {
	// Publish sends a saga event to the event bus.
	Publish(ctx context.Context, event Event) error
}

// OutboxEntry represents an entry in the event outbox for transactional publishing.
// This is a saga-specific wrapper around the platform outbox pattern.
type OutboxEntry struct {
	ID            uuid.UUID `json:"id"`
	EventType     string    `json:"event_type"`
	AggregateID   string    `json:"aggregate_id"`
	AggregateType string    `json:"aggregate_type"`
	EventPayload  []byte    `json:"event_payload"`
	CorrelationID string    `json:"correlation_id"`
	CausationID   string    `json:"causation_id,omitempty"`
	Topic         string    `json:"topic"`
	ServiceName   string    `json:"service_name"`
}

// OutboxWriter writes entries to the event outbox.
type OutboxWriter interface {
	// Write adds an entry to the outbox.
	Write(ctx context.Context, entry *OutboxEntry) error
}

// OutboxEventPublisher publishes saga events via the transactional outbox pattern.
// Events are written to the outbox table within the same transaction as the saga state change,
// ensuring exactly-once delivery semantics.
//
type OutboxEventPublisher struct {
	writer      OutboxWriter
	topic       string
	serviceName string
}

// NewOutboxEventPublisher creates a new outbox-based saga event publisher.
func NewOutboxEventPublisher(writer OutboxWriter, topic, serviceName string) *OutboxEventPublisher {
	return &OutboxEventPublisher{
		writer:      writer,
		topic:       topic,
		serviceName: serviceName,
	}
}

// Publish implements EventPublisher using the outbox pattern.
func (p *OutboxEventPublisher) Publish(ctx context.Context, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal saga event: %w", err)
	}

	entry := &OutboxEntry{
		ID:            uuid.New(),
		EventType:     string(event.EventType()),
		AggregateID:   event.SagaID().String(),
		AggregateType: "SagaInstance",
		EventPayload:  payload,
		CorrelationID: event.GetCorrelationID().String(),
		Topic:         p.topic,
		ServiceName:   p.serviceName,
	}

	// Set causation ID for step events
	if completed, ok := event.(*StepCompletedEvent); ok {
		entry.CausationID = completed.CausationID.String()
	}
	if failed, ok := event.(*StepFailedEvent); ok {
		entry.CausationID = failed.CausationID.String()
	}

	return p.writer.Write(ctx, entry)
}

// TxContextWithOutbox extends TxContext with outbox writing capability.
// This allows atomic writing of step results and saga events in the same transaction.
type TxContextWithOutbox interface {
	TxContext

	// WriteOutboxEntry writes an event to the outbox within this transaction.
	WriteOutboxEntry(ctx context.Context, entry *OutboxEntry) error
}
