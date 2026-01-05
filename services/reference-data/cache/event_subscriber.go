// Package cache provides caching infrastructure for instrument definitions
// with tenant isolation and TTL-based expiration.
package cache

import (
	"context"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// InstrumentUpdatedEvent represents a Kafka event for instrument updates.
// This event is published when an instrument is created, modified, or deleted
// in the registry.
type InstrumentUpdatedEvent struct {
	// TenantID identifies which tenant's instrument was updated.
	TenantID string

	// Code is the instrument code that was updated.
	Code string

	// Version is the specific version of the instrument that changed.
	// Used for logging/tracing; cache invalidates all versions of the code.
	Version int
}

// Invalidator defines the interface for cache invalidation operations.
// Both InstrumentCache (L1 only) and TieredInstrumentCache (L1+L2) implement
// this interface, allowing the EventSubscriber to work with either.
type Invalidator interface {
	// InvalidateCode removes all versions of an instrument code from the cache.
	// For tiered caches, this propagates to all cache layers (L1 and L2).
	InvalidateCode(ctx context.Context, code string)
}

// Verify that our cache implementations satisfy the Invalidator interface.
var (
	_ Invalidator = (*InstrumentCache)(nil)
	_ Invalidator = (*TieredInstrumentCache)(nil)
)

// EventSubscriber handles Kafka events for distributed cache invalidation.
// The actual Kafka subscription is wired up by the service layer - this
// component provides the handler interface.
//
// When configured with a TieredInstrumentCache, invalidation events will
// propagate to both L1 (in-memory) and L2 (Redis) cache layers.
type EventSubscriber struct {
	cache Invalidator
}

// NewEventSubscriber creates a new EventSubscriber that will invalidate
// cache entries when instrument update events are received.
//
// The cache parameter can be either:
// - *InstrumentCache for L1-only invalidation
// - *TieredInstrumentCache for dual-layer (L1+L2) invalidation
func NewEventSubscriber(cache Invalidator) *EventSubscriber {
	return &EventSubscriber{
		cache: cache,
	}
}

// HandleInstrumentUpdated processes an instrument.updated Kafka event
// by invalidating all cached versions of the instrument for the tenant.
//
// This method uses MustNewTenantID since events should have valid tenant IDs.
// If the tenant ID is invalid, it will panic - this is fail-fast behavior
// to catch malformed events in development/testing rather than silently
// ignoring them.
func (s *EventSubscriber) HandleInstrumentUpdated(event InstrumentUpdatedEvent) {
	// Construct context with tenant from event payload
	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID(event.TenantID))

	// Invalidate all versions of this instrument code
	// The cache.InvalidateCode method already handles the case where
	// the tenant cache doesn't exist (no-op)
	s.cache.InvalidateCode(ctx, event.Code)
}
