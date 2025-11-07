package domain

import (
	"context"
	"errors"
)

// ErrPublisherNotConfigured is returned when event publisher is not set up
var ErrPublisherNotConfigured = errors.New("event publisher not configured")

// EventPublisher defines the interface for publishing domain events to the messaging infrastructure.
// Implementations should handle serialization, delivery, and error handling for events.
type EventPublisher interface {
	// Publish publishes a single domain event to the appropriate topic/stream.
	// The topic is determined based on the event type.
	// Returns an error if publishing fails.
	Publish(ctx context.Context, event DomainEvent) error

	// PublishBatch publishes multiple domain events as a batch for efficiency.
	// All events should succeed or fail together (transactional semantics where possible).
	// Returns an error if any event in the batch fails to publish.
	PublishBatch(ctx context.Context, events []DomainEvent) error
}

// NoOpEventPublisher is a no-operation implementation of EventPublisher.
// Useful for testing and scenarios where event publishing is not required.
type NoOpEventPublisher struct{}

// NewNoOpEventPublisher creates a new no-operation event publisher.
func NewNoOpEventPublisher() *NoOpEventPublisher {
	return &NoOpEventPublisher{}
}

// Publish does nothing and always returns nil.
func (p *NoOpEventPublisher) Publish(_ context.Context, _ DomainEvent) error {
	return nil
}

// PublishBatch does nothing and always returns nil.
func (p *NoOpEventPublisher) PublishBatch(_ context.Context, _ []DomainEvent) error {
	return nil
}

// InMemoryEventPublisher stores events in memory for testing purposes.
// Not suitable for production use - events are lost on restart.
type InMemoryEventPublisher struct {
	events []DomainEvent
}

// NewInMemoryEventPublisher creates a new in-memory event publisher for testing.
func NewInMemoryEventPublisher() *InMemoryEventPublisher {
	return &InMemoryEventPublisher{
		events: make([]DomainEvent, 0),
	}
}

// Publish stores the event in memory.
func (p *InMemoryEventPublisher) Publish(_ context.Context, event DomainEvent) error {
	p.events = append(p.events, event)
	return nil
}

// PublishBatch stores all events in memory.
func (p *InMemoryEventPublisher) PublishBatch(_ context.Context, events []DomainEvent) error {
	p.events = append(p.events, events...)
	return nil
}

// GetPublishedEvents returns all events that have been published (for testing assertions).
func (p *InMemoryEventPublisher) GetPublishedEvents() []DomainEvent {
	return p.events
}

// Clear removes all published events (for test cleanup).
func (p *InMemoryEventPublisher) Clear() {
	p.events = make([]DomainEvent, 0)
}
