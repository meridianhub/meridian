package eventstream

import "context"

// EventHandler is a callback invoked for each event delivered from an EventSource or FanOut.
// Implementations must be safe to call concurrently. Returning a non-nil error signals
// that the event could not be processed; the adapter decides whether to retry or dead-letter.
type EventHandler func(ctx context.Context, event DomainEvent) error

// EventSource is the inbound port that abstracts event ingestion from an external
// messaging system such as Kafka. Adapters implement this interface to feed events
// into the gateway streaming pipeline.
//
// Start blocks until the context is cancelled or a fatal error occurs. Each consumed
// event is delivered to handler synchronously in the same goroutine; handlers should
// return quickly or dispatch to a worker pool.
//
// Adapters must guarantee that Start returns when ctx is done, and must not invoke
// handler after Start returns.
type EventSource interface {
	// Start begins consuming events from the underlying source and delivers each
	// event to handler. It blocks until ctx is cancelled or a fatal error occurs.
	Start(ctx context.Context, handler EventHandler) error
}

// FanOut is the distribution port that coordinates real-time event delivery across
// multiple gateway instances or in-process subscribers.
//
// A typical flow:
//  1. An EventSource adapter calls Publish for every received event.
//  2. FanOut matches the event against all active Subscriptions registered for the tenant.
//  3. Matching handlers are called, delivering the event to connected SSE or WebSocket clients.
//
// Implementations must be safe for concurrent use from multiple goroutines.
type FanOut interface {
	// Publish broadcasts event to all handlers currently subscribed for tenantID.
	// Returns ErrEmptyTenantID if tenantID is empty.
	Publish(ctx context.Context, tenantID string, event DomainEvent) error

	// Subscribe registers handler to receive events for tenantID.
	// If a handler is already registered for tenantID, it is replaced.
	// Returns ErrEmptyTenantID if tenantID is empty.
	Subscribe(ctx context.Context, tenantID string, handler EventHandler) error

	// Unsubscribe removes the handler registered for tenantID.
	// It is not an error to unsubscribe a tenantID that has no registered handler.
	// Returns ErrEmptyTenantID if tenantID is empty.
	Unsubscribe(ctx context.Context, tenantID string) error
}
