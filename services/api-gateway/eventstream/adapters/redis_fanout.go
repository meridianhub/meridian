// Package adapters provides infrastructure adapters for the eventstream ports.
package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
)

// channelPrefix is the Redis pub/sub channel namespace for tenant events.
const channelPrefix = "meridian:events:"

// ErrNilHandler is returned by Subscribe when a nil handler is provided.
var ErrNilHandler = errors.New("handler cannot be nil")

// subscription holds the state for a single tenant subscription.
type subscription struct {
	cancel  context.CancelFunc
	handler eventstream.EventHandler
}

// RedisFanOut implements eventstream.FanOut using Redis pub/sub.
// Each tenant is assigned a dedicated channel: meridian:events:{tenant_id}.
// Events are JSON-marshaled on publish and unmarshaled on receipt.
//
// Implementations are safe for concurrent use from multiple goroutines.
type RedisFanOut struct {
	client        *redis.Client
	subscriptions map[string]*subscription
	logger        *slog.Logger
	mu            sync.Mutex
}

// Compile-time interface compliance check.
var _ eventstream.FanOut = (*RedisFanOut)(nil)

// NewRedisFanOut creates a new RedisFanOut backed by the provided Redis client.
func NewRedisFanOut(client *redis.Client, logger *slog.Logger) *RedisFanOut {
	return &RedisFanOut{
		client:        client,
		subscriptions: make(map[string]*subscription),
		logger:        logger,
	}
}

// channel returns the Redis pub/sub channel name for a tenant.
func channel(tenantID string) string {
	return channelPrefix + tenantID
}

// Publish marshals event to JSON and publishes it to the tenant's Redis pub/sub channel.
// Returns eventstream.ErrEmptyTenantID if event.TenantID is empty.
func (f *RedisFanOut) Publish(ctx context.Context, event eventstream.DomainEvent) error {
	if event.TenantID == "" {
		return eventstream.ErrEmptyTenantID
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	ch := channel(event.TenantID)
	if err := f.client.Publish(ctx, ch, data).Err(); err != nil {
		return fmt.Errorf("redis publish to %s: %w", ch, err)
	}

	f.logger.Debug("published event", "tenant_id", event.TenantID, "event_id", event.EventID, "channel", ch)
	return nil
}

// Subscribe registers handler to receive events for tenantID via Redis pub/sub.
// If a subscription already exists for tenantID, it is replaced (the prior
// subscription's goroutine is stopped before the new one starts).
// Returns eventstream.ErrEmptyTenantID if tenantID is empty.
// Returns ErrNilHandler if handler is nil.
func (f *RedisFanOut) Subscribe(ctx context.Context, tenantID string, handler eventstream.EventHandler) error {
	if tenantID == "" {
		return eventstream.ErrEmptyTenantID
	}
	if handler == nil {
		return ErrNilHandler
	}

	// Cancel any existing subscription for this tenant.
	f.mu.Lock()
	if existing, ok := f.subscriptions[tenantID]; ok {
		existing.cancel()
	}

	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscription{
		cancel:  cancel,
		handler: handler,
	}
	f.subscriptions[tenantID] = sub
	f.mu.Unlock()

	ch := channel(tenantID)
	pubsub := f.client.Subscribe(subCtx, ch)

	// Wait for Redis to acknowledge the subscription before returning.
	// This prevents messages published immediately after Subscribe from being lost.
	if _, err := pubsub.Receive(subCtx); err != nil {
		cancel()
		_ = pubsub.Close()
		f.mu.Lock()
		if cur, ok := f.subscriptions[tenantID]; ok && cur == sub {
			delete(f.subscriptions, tenantID)
		}
		f.mu.Unlock()
		return fmt.Errorf("redis subscribe to %s: %w", ch, err)
	}

	go f.receiveLoop(subCtx, tenantID, pubsub, sub)

	f.logger.Debug("subscribed", "tenant_id", tenantID, "channel", ch)
	return nil
}

// receiveLoop processes messages from the Redis pub/sub channel until the context is done.
// On exit it closes the pubsub subscription and removes the entry from the subscription map,
// but only if the map entry still points to this subscription (identity check).
func (f *RedisFanOut) receiveLoop(ctx context.Context, tenantID string, pubsub *redis.PubSub, sub *subscription) {
	defer func() {
		_ = pubsub.Close()
		f.mu.Lock()
		if cur, ok := f.subscriptions[tenantID]; ok && cur == sub {
			delete(f.subscriptions, tenantID)
		}
		f.mu.Unlock()

		f.logger.Debug("subscription stopped", "tenant_id", tenantID)
	}()

	msgCh := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			f.handleMessage(ctx, tenantID, msg.Payload, sub.handler)
		}
	}
}

// handleMessage unmarshals a Redis pub/sub message and invokes the handler.
func (f *RedisFanOut) handleMessage(ctx context.Context, tenantID, payload string, handler eventstream.EventHandler) {
	var event eventstream.DomainEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		f.logger.Error("failed to unmarshal event", "tenant_id", tenantID, "error", err)
		return
	}

	if err := handler(ctx, event); err != nil {
		f.logger.Error("event handler returned error", "tenant_id", tenantID, "event_id", event.EventID, "error", err)
	}
}

// Unsubscribe removes the handler registered for tenantID and stops the subscription goroutine.
// It is not an error to call Unsubscribe for a tenantID with no active subscription.
// Returns eventstream.ErrEmptyTenantID if tenantID is empty.
func (f *RedisFanOut) Unsubscribe(_ context.Context, tenantID string) error {
	if tenantID == "" {
		return eventstream.ErrEmptyTenantID
	}

	f.mu.Lock()
	sub, ok := f.subscriptions[tenantID]
	if ok {
		sub.cancel()
		delete(f.subscriptions, tenantID)
	}
	f.mu.Unlock()

	if ok {
		f.logger.Debug("unsubscribed", "tenant_id", tenantID)
	}
	return nil
}
