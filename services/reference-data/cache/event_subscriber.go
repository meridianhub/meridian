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

// EventSubscriber handles Kafka events for distributed cache invalidation.
// The actual Kafka subscription is wired up by the service layer - this
// component provides the handler interface.
type EventSubscriber struct {
	cache *InstrumentCache
}

// NewEventSubscriber creates a new EventSubscriber that will invalidate
// cache entries when instrument update events are received.
func NewEventSubscriber(cache *InstrumentCache) *EventSubscriber {
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
