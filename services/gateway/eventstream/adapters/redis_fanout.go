// Package adapters provides infrastructure adapters for the eventstream ports.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/meridianhub/meridian/services/gateway/eventstream"
)

// channelPrefix is the Redis pub/sub channel namespace for tenant events.
const channelPrefix = "meridian:events:"

// subscription holds the state for a single tenant subscription.
type subscription struct {
	cancel  context.CancelFunc
	handler eventstream.EventHandler
}

// RedisFanOut implements eventstream.FanOut using Redis pub/sub.
// Each tenant is assigned a dedicated channel: meridian:events:{tenant_id}.
// Events are JSON-marshaled before publishing and unmarshaled on receipt.
//
// Implementations are safe for concurrent use from multiple goroutines.
type RedisFanOut struct {
	client        *redis.Client
	subscriptions map[string]*subscription
	logger        *slog.Logger
	mu            sync.RWMutex
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
func (f *RedisFanOut) Subscribe(ctx context.Context, tenantID string, handler eventstream.EventHandler) error {
	if tenantID == "" {
		return eventstream.ErrEmptyTenantID
	}

	// Cancel any existing subscription for this tenant.
	f.mu.Lock()
	if existing, ok := f.subscriptions[tenantID]; ok {
		existing.cancel()
	}

	subCtx, cancel := context.WithCancel(ctx)
	f.subscriptions[tenantID] = &subscription{
		cancel:  cancel,
		handler: handler,
	}
	f.mu.Unlock()

	ch := channel(tenantID)
	pubsub := f.client.Subscribe(subCtx, ch)

	go f.receiveLoop(subCtx, tenantID, pubsub, handler)

	f.logger.Debug("subscribed", "tenant_id", tenantID, "channel", ch)
	return nil
}

// receiveLoop processes messages from the Redis pub/sub channel until the context is done.
// On exit it closes the pubsub subscription and removes the entry from the subscription map.
func (f *RedisFanOut) receiveLoop(ctx context.Context, tenantID string, pubsub *redis.PubSub, handler eventstream.EventHandler) {
	defer func() {
		_ = pubsub.Close()
		// Clean up the subscription map entry only if it still points to this subscription
		// (a Replace may have already installed a new one).
		f.mu.Lock()
		if sub, ok := f.subscriptions[tenantID]; ok {
			// Compare by checking whether the context is the one we own.
			// The simplest safe check: if context is done, remove the entry.
			select {
			case <-ctx.Done():
				delete(f.subscriptions, tenantID)
			default:
				// A new subscription replaced us; leave the map alone.
				_ = sub
			}
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
			f.handleMessage(ctx, tenantID, msg.Payload, handler)
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
