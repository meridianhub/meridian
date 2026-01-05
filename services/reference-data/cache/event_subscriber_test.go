package cache

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
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

// TestEventSubscriber_WithTieredCache_InvalidatesBothL1AndL2 tests that when
// the EventSubscriber is configured with a TieredInstrumentCache, invalidation
// events propagate to both L1 (in-memory) and L2 (Redis) cache layers.
func TestEventSubscriber_WithTieredCache_InvalidatesBothL1AndL2(t *testing.T) {
	// Setup miniredis for L2
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	l1 := NewInstrumentCache()
	l2, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	source := newMockSource()
	tiered := NewTieredInstrumentCache(l1, l2, source, nil)

	// Create subscriber with tiered cache
	subscriber := NewEventSubscriber(tiered)

	ctx := newTestContext("tenant1")

	// Pre-populate source with multiple versions
	source.addDefinition("tenant1", newTieredTestDefinition("USD", 1))
	source.addDefinition("tenant1", newTieredTestDefinition("USD", 2))
	source.addDefinition("tenant1", newTieredTestDefinition("EUR", 1))

	// Load into both caches via tiered.Get
	for _, code := range []string{"USD", "EUR"} {
		for v := 1; v <= 2; v++ {
			if code == "EUR" && v == 2 {
				continue // EUR only has v1
			}
			_, err := tiered.Get(ctx, code, v)
			require.NoError(t, err)
		}
	}

	// Verify L1 has all entries
	require.NotNil(t, l1.Get(ctx, "USD", 1), "L1 should have USD v1")
	require.NotNil(t, l1.Get(ctx, "USD", 2), "L1 should have USD v2")
	require.NotNil(t, l1.Get(ctx, "EUR", 1), "L1 should have EUR v1")

	// Verify L2 has all entries
	require.NotNil(t, l2.Get(ctx, "USD", 1), "L2 should have USD v1")
	require.NotNil(t, l2.Get(ctx, "USD", 2), "L2 should have USD v2")
	require.NotNil(t, l2.Get(ctx, "EUR", 1), "L2 should have EUR v1")

	// Handle update event for USD
	event := InstrumentUpdatedEvent{
		TenantID: "tenant1",
		Code:     "USD",
		Version:  1,
	}
	subscriber.HandleInstrumentUpdated(event)

	// All USD versions should be invalidated from BOTH L1 and L2
	assert.Nil(t, l1.Get(ctx, "USD", 1), "L1 should not have USD v1 after invalidation")
	assert.Nil(t, l1.Get(ctx, "USD", 2), "L1 should not have USD v2 after invalidation")
	assert.Nil(t, l2.Get(ctx, "USD", 1), "L2 should not have USD v1 after invalidation")
	assert.Nil(t, l2.Get(ctx, "USD", 2), "L2 should not have USD v2 after invalidation")

	// EUR should still be cached in both tiers
	assert.NotNil(t, l1.Get(ctx, "EUR", 1), "L1 should still have EUR v1")
	assert.NotNil(t, l2.Get(ctx, "EUR", 1), "L2 should still have EUR v1")
}

// TestEventSubscriber_WithTieredCache_TenantIsolation tests that invalidation
// events for one tenant do not affect another tenant's cached data in either
// L1 or L2 cache layers.
func TestEventSubscriber_WithTieredCache_TenantIsolation(t *testing.T) {
	// Setup miniredis for L2
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	l1 := NewInstrumentCache()
	l2, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	source := newMockSource()
	tiered := NewTieredInstrumentCache(l1, l2, source, nil)

	subscriber := NewEventSubscriber(tiered)

	ctxA := newTestContext("tenantA")
	ctxB := newTestContext("tenantB")

	// Pre-populate source for both tenants
	source.addDefinition("tenantA", newTieredTestDefinition("USD", 1))
	source.addDefinition("tenantB", newTieredTestDefinition("USD", 1))

	// Load into caches for both tenants
	_, err = tiered.Get(ctxA, "USD", 1)
	require.NoError(t, err)
	_, err = tiered.Get(ctxB, "USD", 1)
	require.NoError(t, err)

	// Verify both tenants have data in L1 and L2
	require.NotNil(t, l1.Get(ctxA, "USD", 1))
	require.NotNil(t, l1.Get(ctxB, "USD", 1))
	require.NotNil(t, l2.Get(ctxA, "USD", 1))
	require.NotNil(t, l2.Get(ctxB, "USD", 1))

	// Handle update event for tenantA only
	event := InstrumentUpdatedEvent{
		TenantID: "tenantA",
		Code:     "USD",
		Version:  1,
	}
	subscriber.HandleInstrumentUpdated(event)

	// TenantA's data should be invalidated from both L1 and L2
	assert.Nil(t, l1.Get(ctxA, "USD", 1), "tenantA L1 should be invalidated")
	assert.Nil(t, l2.Get(ctxA, "USD", 1), "tenantA L2 should be invalidated")

	// TenantB's data should be unaffected in both L1 and L2
	assert.NotNil(t, l1.Get(ctxB, "USD", 1), "tenantB L1 should not be affected")
	assert.NotNil(t, l2.Get(ctxB, "USD", 1), "tenantB L2 should not be affected")
}

// TestEventSubscriber_AcceptsBothCacheTypes verifies that the EventSubscriber
// can be constructed with either an InstrumentCache (L1 only) or a
// TieredInstrumentCache (L1+L2).
func TestEventSubscriber_AcceptsBothCacheTypes(t *testing.T) {
	t.Run("with InstrumentCache (L1 only)", func(t *testing.T) {
		l1 := NewInstrumentCache()
		subscriber := NewEventSubscriber(l1)
		require.NotNil(t, subscriber)

		// Should work without panicking
		ctx := newTestContext("tenant1")
		l1.Put(ctx, "USD", 1, newTestInstrument("USD", 1))
		require.NotNil(t, l1.Get(ctx, "USD", 1))

		subscriber.HandleInstrumentUpdated(InstrumentUpdatedEvent{
			TenantID: "tenant1",
			Code:     "USD",
			Version:  1,
		})
		assert.Nil(t, l1.Get(ctx, "USD", 1))
	})

	t.Run("with TieredInstrumentCache (L1+L2)", func(t *testing.T) {
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		defer client.Close()

		l1 := NewInstrumentCache()
		l2, err := NewRedisL2Cache(client)
		require.NoError(t, err)

		source := newMockSource()
		source.addDefinition("tenant1", newTieredTestDefinition("USD", 1))

		tiered := NewTieredInstrumentCache(l1, l2, source, nil)
		subscriber := NewEventSubscriber(tiered)
		require.NotNil(t, subscriber)

		// Load data into both caches
		ctx := newTestContext("tenant1")
		_, err = tiered.Get(ctx, "USD", 1)
		require.NoError(t, err)
		require.NotNil(t, l1.Get(ctx, "USD", 1))
		require.NotNil(t, l2.Get(ctx, "USD", 1))

		// Invalidate should clear both
		subscriber.HandleInstrumentUpdated(InstrumentUpdatedEvent{
			TenantID: "tenant1",
			Code:     "USD",
			Version:  1,
		})
		assert.Nil(t, l1.Get(ctx, "USD", 1))
		assert.Nil(t, l2.Get(ctx, "USD", 1))
	})
}
