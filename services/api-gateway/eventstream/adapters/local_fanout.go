// Package adapters provides concrete implementations of the eventstream port
// interfaces defined in the parent package.
package adapters

import (
	"context"
	"errors"
	"sync"

	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
)

// ErrNegativeBufferSize is returned when NewLocalFanOutE is called with a
// negative buffer size.
var ErrNegativeBufferSize = errors.New("buffer size must be non-negative")

// LocalFanOut is an in-process FanOut implementation that routes DomainEvents to
// subscribed handlers via buffered channels. It is used as the default fan-out
// backend when no external pub/sub system (e.g. Redis) is configured.
//
// Each tenantID maps to exactly one subscriber. Registering a new subscriber for a
// tenantID that already has one replaces the previous subscription by closing its
// channel, which causes its event loop to return.
//
// Publish is non-blocking: if the subscriber's channel is full the event is dropped.
// LocalFanOut is safe for concurrent use from multiple goroutines.
type LocalFanOut struct {
	mu          sync.RWMutex
	subscribers map[string]chan eventstream.DomainEvent
	bufferSize  int
}

// NewLocalFanOut returns a LocalFanOut with the given per-subscriber channel
// buffer size. It panics if bufferSize is negative. Use NewLocalFanOutE when
// the buffer size is derived from user-supplied configuration.
func NewLocalFanOut(bufferSize int) *LocalFanOut {
	fo, err := NewLocalFanOutE(bufferSize)
	if err != nil {
		panic(err)
	}
	return fo
}

// NewLocalFanOutE returns a LocalFanOut with the given per-subscriber channel
// buffer size, or ErrNegativeBufferSize if bufferSize is negative.
// A bufferSize of 0 creates unbuffered channels (every Publish blocks until
// the handler processes the event — suitable only for tests).
func NewLocalFanOutE(bufferSize int) (*LocalFanOut, error) {
	if bufferSize < 0 {
		return nil, ErrNegativeBufferSize
	}
	return &LocalFanOut{
		subscribers: make(map[string]chan eventstream.DomainEvent),
		bufferSize:  bufferSize,
	}, nil
}

// Publish delivers event to the handler subscribed for event.TenantID.
// If no subscriber is registered the event is silently dropped.
// If the subscriber's buffer is full the event is dropped without blocking.
// Returns ErrEmptyTenantID if event.TenantID is empty.
func (f *LocalFanOut) Publish(_ context.Context, event eventstream.DomainEvent) error {
	if event.TenantID == "" {
		return eventstream.ErrEmptyTenantID
	}

	// Hold the read lock for the entire send to prevent concurrent Unsubscribe
	// from closing the channel while we are trying to send into it.
	f.mu.RLock()
	ch, ok := f.subscribers[event.TenantID]
	if ok {
		// Non-blocking send: drop on full buffer.
		select {
		case ch <- event:
		default:
		}
	}
	f.mu.RUnlock()

	return nil
}

// Subscribe registers handler to receive events for tenantID and blocks until
// ctx is cancelled or the subscription is replaced by a subsequent call to
// Subscribe with the same tenantID or Unsubscribe is called.
//
// If a handler is already registered for tenantID it is replaced: the previous
// event loop is terminated by closing its channel.
//
// Returns ErrEmptyTenantID if tenantID is empty.
// Returns nil when the event loop exits cleanly.
func (f *LocalFanOut) Subscribe(ctx context.Context, tenantID string, handler eventstream.EventHandler) error {
	if tenantID == "" {
		return eventstream.ErrEmptyTenantID
	}

	ch := make(chan eventstream.DomainEvent, f.bufferSize)

	f.mu.Lock()
	if existing, ok := f.subscribers[tenantID]; ok {
		close(existing)
	}
	f.subscribers[tenantID] = ch
	f.mu.Unlock()

	// Event loop: forward channel events to handler until channel is closed or
	// context is cancelled.
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				// Channel was closed (replaced or unsubscribed).
				return nil
			}
			// Handler errors are intentionally ignored at this layer; callers
			// that need error propagation should wrap handler with their own
			// error-handling decorator.
			_ = handler(ctx, event)
		case <-ctx.Done():
			// Remove our channel from the map if it is still registered.
			f.mu.Lock()
			if current, ok := f.subscribers[tenantID]; ok && current == ch {
				delete(f.subscribers, tenantID)
			}
			f.mu.Unlock()
			return nil
		}
	}
}

// Unsubscribe removes the handler registered for tenantID and causes the blocked
// Subscribe call (if any) to return nil.
//
// It is not an error to call Unsubscribe for a tenantID that has no registered
// handler.
//
// Returns ErrEmptyTenantID if tenantID is empty.
func (f *LocalFanOut) Unsubscribe(_ context.Context, tenantID string) error {
	if tenantID == "" {
		return eventstream.ErrEmptyTenantID
	}

	f.mu.Lock()
	ch, ok := f.subscribers[tenantID]
	if ok {
		delete(f.subscribers, tenantID)
		close(ch)
	}
	f.mu.Unlock()

	return nil
}
