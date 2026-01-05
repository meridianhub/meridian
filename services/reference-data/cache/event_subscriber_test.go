package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventSubscriber_HandleInstrumentUpdated_InvalidatesCorrectEntry(t *testing.T) {
	cache := NewInstrumentCache()
	subscriber := NewEventSubscriber(cache)

	ctx := newTestContext("tenant1")

	// Pre-populate cache with multiple versions of USD and EUR
	cache.Put(ctx, "USD", 1, newTestInstrument("USD", 1))
	cache.Put(ctx, "USD", 2, newTestInstrument("USD", 2))
	cache.Put(ctx, "EUR", 1, newTestInstrument("EUR", 1))

	// Verify all are cached
	require.NotNil(t, cache.Get(ctx, "USD", 1))
	require.NotNil(t, cache.Get(ctx, "USD", 2))
	require.NotNil(t, cache.Get(ctx, "EUR", 1))

	// Handle update event for USD
	event := InstrumentUpdatedEvent{
		TenantID: "tenant1",
		Code:     "USD",
		Version:  1, // Version is for tracing, all versions are invalidated
	}
	subscriber.HandleInstrumentUpdated(event)

	// All USD versions should be invalidated
	assert.Nil(t, cache.Get(ctx, "USD", 1), "USD v1 should be invalidated")
	assert.Nil(t, cache.Get(ctx, "USD", 2), "USD v2 should be invalidated")

	// EUR should still be cached
	assert.NotNil(t, cache.Get(ctx, "EUR", 1), "EUR should not be affected")
}

func TestEventSubscriber_HandleInstrumentUpdated_TenantIsolation(t *testing.T) {
	cache := NewInstrumentCache()
	subscriber := NewEventSubscriber(cache)

	ctxA := newTestContext("tenantA")
	ctxB := newTestContext("tenantB")

	// Pre-populate cache for both tenants with same instrument code
	cache.Put(ctxA, "USD", 1, newTestInstrument("USD", 1))
	cache.Put(ctxA, "USD", 2, newTestInstrument("USD", 2))
	cache.Put(ctxB, "USD", 1, newTestInstrument("USD", 1))
	cache.Put(ctxB, "USD", 2, newTestInstrument("USD", 2))

	// Verify all are cached
	require.NotNil(t, cache.Get(ctxA, "USD", 1))
	require.NotNil(t, cache.Get(ctxA, "USD", 2))
	require.NotNil(t, cache.Get(ctxB, "USD", 1))
	require.NotNil(t, cache.Get(ctxB, "USD", 2))

	// Handle update event for tenantA only
	event := InstrumentUpdatedEvent{
		TenantID: "tenantA",
		Code:     "USD",
		Version:  1,
	}
	subscriber.HandleInstrumentUpdated(event)

	// TenantA's USD should be invalidated
	assert.Nil(t, cache.Get(ctxA, "USD", 1), "tenantA USD v1 should be invalidated")
	assert.Nil(t, cache.Get(ctxA, "USD", 2), "tenantA USD v2 should be invalidated")

	// TenantB's USD should still be cached
	assert.NotNil(t, cache.Get(ctxB, "USD", 1), "tenantB USD v1 should not be affected")
	assert.NotNil(t, cache.Get(ctxB, "USD", 2), "tenantB USD v2 should not be affected")
}

func TestEventSubscriber_HandleInstrumentUpdated_NonExistentCache(t *testing.T) {
	cache := NewInstrumentCache()
	subscriber := NewEventSubscriber(cache)

	// Handle event for a tenant that has no cached data
	// This should not panic or error
	event := InstrumentUpdatedEvent{
		TenantID: "nonexistent_tenant",
		Code:     "USD",
		Version:  1,
	}

	// Should not panic - if we reach this point without panic, the test passes
	subscriber.HandleInstrumentUpdated(event)

	// Verify the tenant cache was not created (should return 0,0)
	ctx := newTestContext("nonexistent_tenant")
	size, _ := cache.Stats(ctx)
	assert.Equal(t, 0, size, "cache should be empty for nonexistent tenant")
}

func TestEventSubscriber_HandleInstrumentUpdated_MultipleCodes(t *testing.T) {
	cache := NewInstrumentCache()
	subscriber := NewEventSubscriber(cache)

	ctx := newTestContext("tenant1")

	// Pre-populate cache with multiple instruments
	cache.Put(ctx, "USD", 1, newTestInstrument("USD", 1))
	cache.Put(ctx, "EUR", 1, newTestInstrument("EUR", 1))
	cache.Put(ctx, "GBP", 1, newTestInstrument("GBP", 1))
	cache.Put(ctx, "JPY", 1, newTestInstrument("JPY", 1))

	// Invalidate EUR
	subscriber.HandleInstrumentUpdated(InstrumentUpdatedEvent{
		TenantID: "tenant1",
		Code:     "EUR",
		Version:  1,
	})

	// EUR should be invalidated
	assert.Nil(t, cache.Get(ctx, "EUR", 1), "EUR should be invalidated")

	// Others should still be cached
	assert.NotNil(t, cache.Get(ctx, "USD", 1), "USD should not be affected")
	assert.NotNil(t, cache.Get(ctx, "GBP", 1), "GBP should not be affected")
	assert.NotNil(t, cache.Get(ctx, "JPY", 1), "JPY should not be affected")

	// Now invalidate GBP
	subscriber.HandleInstrumentUpdated(InstrumentUpdatedEvent{
		TenantID: "tenant1",
		Code:     "GBP",
		Version:  1,
	})

	// GBP should now also be invalidated
	assert.Nil(t, cache.Get(ctx, "GBP", 1), "GBP should be invalidated")

	// USD and JPY should still be cached
	assert.NotNil(t, cache.Get(ctx, "USD", 1), "USD should not be affected")
	assert.NotNil(t, cache.Get(ctx, "JPY", 1), "JPY should not be affected")
}

func TestNewEventSubscriber(t *testing.T) {
	cache := NewInstrumentCache()
	subscriber := NewEventSubscriber(cache)

	assert.NotNil(t, subscriber)
	assert.NotNil(t, subscriber.cache)
}
