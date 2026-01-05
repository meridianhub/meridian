package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// newTestContext creates a context with a test tenant.
func newTestContext(tenantID string) context.Context {
	return tenant.WithTenant(context.Background(), tenant.MustNewTenantID(tenantID))
}

// newTestInstrument creates a test CachedInstrument with the given code and version.
func newTestInstrument(code string, version int) *CachedInstrument {
	return &CachedInstrument{
		Definition: &registry.InstrumentDefinition{
			ID:        uuid.New(),
			Code:      code,
			Version:   version,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
			Status:    registry.StatusActive,
		},
		// ValidationProgram and BucketKeyProgram left nil for basic tests
	}
}

func TestInstrumentCache_Get_Hit(t *testing.T) {
	cache := NewInstrumentCache()
	ctx := newTestContext("tenant1")

	// Put an entry
	instrument := newTestInstrument("USD", 1)
	cache.Put(ctx, "USD", 1, instrument)

	// Get should return the entry
	result := cache.Get(ctx, "USD", 1)
	require.NotNil(t, result)
	assert.Equal(t, "USD", result.Definition.Code)
	assert.Equal(t, 1, result.Definition.Version)
	assert.Equal(t, registry.DimensionMonetary, result.Definition.Dimension)
	assert.Equal(t, 2, result.Definition.Precision)
}

func TestInstrumentCache_Get_Miss(t *testing.T) {
	cache := NewInstrumentCache()
	ctx := newTestContext("tenant1")

	// Get without Put should return nil
	result := cache.Get(ctx, "USD", 1)
	assert.Nil(t, result)
}

func TestInstrumentCache_Get_MissingTenant(t *testing.T) {
	cache := NewInstrumentCache()
	ctx := context.Background() // No tenant

	// Should return nil when tenant context is missing
	result := cache.Get(ctx, "USD", 1)
	assert.Nil(t, result)
}

func TestInstrumentCache_Put_MissingTenant(t *testing.T) {
	cache := NewInstrumentCache()
	ctx := context.Background() // No tenant

	// Put should be a no-op when tenant context is missing
	instrument := newTestInstrument("USD", 1)
	cache.Put(ctx, "USD", 1, instrument)

	// Verify nothing was cached by trying with a valid tenant
	validCtx := newTestContext("tenant1")
	result := cache.Get(validCtx, "USD", 1)
	assert.Nil(t, result)
}

func TestInstrumentCache_TenantIsolation(t *testing.T) {
	cache := NewInstrumentCache()
	ctx1 := newTestContext("tenant1")
	ctx2 := newTestContext("tenant2")

	// Put entry for tenant1
	instrument1 := newTestInstrument("USD", 1)
	cache.Put(ctx1, "USD", 1, instrument1)

	// Put different entry for tenant2
	instrument2 := newTestInstrument("EUR", 1)
	cache.Put(ctx2, "EUR", 1, instrument2)

	// tenant1 should only see USD
	result1USD := cache.Get(ctx1, "USD", 1)
	require.NotNil(t, result1USD)
	assert.Equal(t, "USD", result1USD.Definition.Code)

	result1EUR := cache.Get(ctx1, "EUR", 1)
	assert.Nil(t, result1EUR, "tenant1 should not see tenant2's EUR")

	// tenant2 should only see EUR
	result2EUR := cache.Get(ctx2, "EUR", 1)
	require.NotNil(t, result2EUR)
	assert.Equal(t, "EUR", result2EUR.Definition.Code)

	result2USD := cache.Get(ctx2, "USD", 1)
	assert.Nil(t, result2USD, "tenant2 should not see tenant1's USD")
}

func TestInstrumentCache_TTLExpiration(t *testing.T) {
	// Use very short TTL for testing
	cache := NewInstrumentCache(
		WithTTL(50*time.Millisecond, 0), // No jitter for predictable tests
	)
	ctx := newTestContext("tenant1")

	// Put an entry
	instrument := newTestInstrument("USD", 1)
	cache.Put(ctx, "USD", 1, instrument)

	// Should be retrievable immediately
	result := cache.Get(ctx, "USD", 1)
	require.NotNil(t, result)

	// Wait for expiration
	time.Sleep(60 * time.Millisecond)

	// Should be expired now
	result = cache.Get(ctx, "USD", 1)
	assert.Nil(t, result, "entry should be expired")
}

func TestInstrumentCache_JitterVariance(t *testing.T) {
	// Use TTL with jitter
	cache := NewInstrumentCache(
		WithTTL(100*time.Millisecond, 50*time.Millisecond), // 100ms ± 50ms
	)
	ctx := newTestContext("tenant1")

	// Put multiple entries and collect their expiration times
	var expirations []time.Duration
	for i := 0; i < 20; i++ {
		instrument := newTestInstrument("INS", i)
		cache.Put(ctx, "INS", i, instrument)

		result := cache.Get(ctx, "INS", i)
		require.NotNil(t, result)
		expirations = append(expirations, result.ExpiresAt().Sub(result.CachedAt()))
	}

	// Verify we have variance in expiration times
	// With jitter of ±50ms, we expect some variation
	minExp := expirations[0]
	maxExp := expirations[0]
	for _, exp := range expirations[1:] {
		if exp < minExp {
			minExp = exp
		}
		if exp > maxExp {
			maxExp = exp
		}
	}

	// The difference between min and max should be non-zero if jitter is working
	// Note: There's a small chance this could fail due to random chance,
	// but with 20 samples it's very unlikely
	diff := maxExp - minExp
	assert.Greater(t, diff.Milliseconds(), int64(0), "jitter should produce variance in TTL")
}

func TestInstrumentCache_Invalidate(t *testing.T) {
	cache := NewInstrumentCache()
	ctx := newTestContext("tenant1")

	// Put an entry
	instrument := newTestInstrument("USD", 1)
	cache.Put(ctx, "USD", 1, instrument)

	// Verify it's cached
	result := cache.Get(ctx, "USD", 1)
	require.NotNil(t, result)

	// Invalidate it
	cache.Invalidate(ctx, "USD", 1)

	// Should be gone
	result = cache.Get(ctx, "USD", 1)
	assert.Nil(t, result)
}

func TestInstrumentCache_InvalidateAll(t *testing.T) {
	cache := NewInstrumentCache()
	ctx := newTestContext("tenant1")

	// Put multiple entries
	cache.Put(ctx, "USD", 1, newTestInstrument("USD", 1))
	cache.Put(ctx, "EUR", 1, newTestInstrument("EUR", 1))
	cache.Put(ctx, "GBP", 1, newTestInstrument("GBP", 1))

	// Verify they're cached
	assert.NotNil(t, cache.Get(ctx, "USD", 1))
	assert.NotNil(t, cache.Get(ctx, "EUR", 1))
	assert.NotNil(t, cache.Get(ctx, "GBP", 1))

	// Invalidate all
	cache.InvalidateAll(ctx)

	// All should be gone
	assert.Nil(t, cache.Get(ctx, "USD", 1))
	assert.Nil(t, cache.Get(ctx, "EUR", 1))
	assert.Nil(t, cache.Get(ctx, "GBP", 1))
}

func TestInstrumentCache_InvalidateAll_TenantIsolation(t *testing.T) {
	cache := NewInstrumentCache()
	ctx1 := newTestContext("tenant1")
	ctx2 := newTestContext("tenant2")

	// Put entries for both tenants
	cache.Put(ctx1, "USD", 1, newTestInstrument("USD", 1))
	cache.Put(ctx2, "EUR", 1, newTestInstrument("EUR", 1))

	// Invalidate all for tenant1 only
	cache.InvalidateAll(ctx1)

	// tenant1's cache should be empty
	assert.Nil(t, cache.Get(ctx1, "USD", 1))

	// tenant2's cache should be unaffected
	assert.NotNil(t, cache.Get(ctx2, "EUR", 1))
}

func TestInstrumentCache_EvictionUnderPressure(t *testing.T) {
	// Create cache with very small size
	cache := NewInstrumentCache(
		WithCacheSize(5),
		WithTTL(1*time.Hour, 0), // Long TTL to avoid expiration
	)
	ctx := newTestContext("tenant1")

	// Add more entries than cache size
	for i := 0; i < 10; i++ {
		cache.Put(ctx, "INS", i, newTestInstrument("INS", i))
	}

	// Check stats - should not exceed capacity
	size, capacity := cache.Stats(ctx)
	assert.Equal(t, 5, capacity)
	assert.LessOrEqual(t, size, capacity)

	// Recent entries (higher versions) should still be in cache
	// Older entries may have been evicted by LRU
	foundCount := 0
	for i := 0; i < 10; i++ {
		if cache.Get(ctx, "INS", i) != nil {
			foundCount++
		}
	}
	assert.Equal(t, 5, foundCount, "should have exactly capacity entries")
}

func TestInstrumentCache_Stats(t *testing.T) {
	cache := NewInstrumentCache(WithCacheSize(100))
	ctx := newTestContext("tenant1")

	// Initially empty - no tenant cache exists yet, so returns 0,0
	size, capacity := cache.Stats(ctx)
	assert.Equal(t, 0, size)
	assert.Equal(t, 0, capacity, "capacity is 0 until tenant cache is created")

	// Add some entries - this creates the tenant cache
	cache.Put(ctx, "USD", 1, newTestInstrument("USD", 1))
	cache.Put(ctx, "EUR", 1, newTestInstrument("EUR", 1))

	// Now we have a tenant cache with configured capacity
	size, capacity = cache.Stats(ctx)
	assert.Equal(t, 2, size)
	assert.Equal(t, 100, capacity)
}

func TestInstrumentCache_Stats_MissingTenant(t *testing.T) {
	cache := NewInstrumentCache()
	ctx := context.Background() // No tenant

	size, capacity := cache.Stats(ctx)
	assert.Equal(t, 0, size)
	assert.Equal(t, 0, capacity)
}

func TestInstrumentCache_ConcurrentAccess(t *testing.T) {
	cache := NewInstrumentCache()
	ctx := newTestContext("tenant1")

	// Pre-populate with some entries
	for i := 0; i < 100; i++ {
		cache.Put(ctx, "INS", i, newTestInstrument("INS", i))
	}

	// Run concurrent reads and writes
	var wg sync.WaitGroup
	errors := make(chan error, 100)

	// Readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = cache.Get(ctx, "INS", j%100)
			}
		}()
	}

	// Writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				version := workerID*100 + j
				cache.Put(ctx, "NEW", version, newTestInstrument("NEW", version))
			}
		}(i)
	}

	// Invalidators
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				cache.Invalidate(ctx, "INS", j)
			}
		}()
	}

	wg.Wait()
	close(errors)

	// No errors should have occurred (race conditions, panics, etc.)
	for err := range errors {
		t.Errorf("concurrent access error: %v", err)
	}
}

func TestInstrumentCache_ConcurrentTenants(t *testing.T) {
	cache := NewInstrumentCache()

	var wg sync.WaitGroup

	// Multiple tenants operating concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(tenantNum int) {
			defer wg.Done()
			ctx := newTestContext("tenant" + string(rune('A'+tenantNum)))

			// Each tenant does its own operations
			for j := 0; j < 100; j++ {
				cache.Put(ctx, "INS", j, newTestInstrument("INS", j))
				_ = cache.Get(ctx, "INS", j)
			}
		}(i)
	}

	wg.Wait()

	// Verify each tenant has their own cached entries
	for i := 0; i < 10; i++ {
		ctx := newTestContext("tenant" + string(rune('A'+i)))
		size, _ := cache.Stats(ctx)
		assert.Equal(t, 100, size, "tenant %d should have 100 cached entries", i)
	}
}

func TestCachedInstrument_Timestamps(t *testing.T) {
	cache := NewInstrumentCache(
		WithTTL(5*time.Minute, 30*time.Second),
	)
	ctx := newTestContext("tenant1")

	before := time.Now()
	cache.Put(ctx, "USD", 1, newTestInstrument("USD", 1))
	after := time.Now()

	result := cache.Get(ctx, "USD", 1)
	require.NotNil(t, result)

	// CachedAt should be between before and after
	assert.True(t, result.CachedAt().After(before) || result.CachedAt().Equal(before))
	assert.True(t, result.CachedAt().Before(after) || result.CachedAt().Equal(after))

	// ExpiresAt should be cachedAt + TTL (within jitter range)
	expectedMinExpiry := result.CachedAt().Add(5*time.Minute - 30*time.Second)
	expectedMaxExpiry := result.CachedAt().Add(5*time.Minute + 30*time.Second)

	assert.True(t, result.ExpiresAt().After(expectedMinExpiry) || result.ExpiresAt().Equal(expectedMinExpiry))
	assert.True(t, result.ExpiresAt().Before(expectedMaxExpiry) || result.ExpiresAt().Equal(expectedMaxExpiry))
}

func TestKey_Equality(t *testing.T) {
	// Key should work correctly as a map key
	key1 := Key{Code: "USD", Version: 1}
	key2 := Key{Code: "USD", Version: 1}
	key3 := Key{Code: "USD", Version: 2}
	key4 := Key{Code: "EUR", Version: 1}

	assert.Equal(t, key1, key2, "same code and version should be equal")
	assert.NotEqual(t, key1, key3, "different version should not be equal")
	assert.NotEqual(t, key1, key4, "different code should not be equal")
}

// Benchmark tests

func BenchmarkInstrumentCache_Get_Hit(b *testing.B) {
	cache := NewInstrumentCache()
	ctx := newTestContext("tenant1")

	// Pre-populate
	cache.Put(ctx, "USD", 1, newTestInstrument("USD", 1))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.Get(ctx, "USD", 1)
	}
}

func BenchmarkInstrumentCache_Get_Miss(b *testing.B) {
	cache := NewInstrumentCache()
	ctx := newTestContext("tenant1")

	// Ensure tenant cache exists but entry doesn't
	cache.Put(ctx, "EUR", 1, newTestInstrument("EUR", 1))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.Get(ctx, "USD", 1) // Misses
	}
}

func BenchmarkInstrumentCache_Put(b *testing.B) {
	cache := NewInstrumentCache()
	ctx := newTestContext("tenant1")
	instrument := newTestInstrument("USD", 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Put(ctx, "USD", i%1000, instrument)
	}
}

func BenchmarkInstrumentCache_Get_Parallel(b *testing.B) {
	cache := NewInstrumentCache()
	ctx := newTestContext("tenant1")

	// Pre-populate
	for i := 0; i < 1000; i++ {
		cache.Put(ctx, "INS", i, newTestInstrument("INS", i))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = cache.Get(ctx, "INS", i%1000)
			i++
		}
	})
}
