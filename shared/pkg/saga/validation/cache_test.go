// Package validation provides dry-run validation for Starlark saga scripts.
package validation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/await"
)

func TestValidationCache_GetMiss(t *testing.T) {
	cache := NewCache(time.Hour, 1000)

	result, ok := cache.Get("new script")
	assert.False(t, ok, "expected cache miss for new script")
	assert.Nil(t, result, "expected nil result on cache miss")
}

func TestValidationCache_SetAndGet(t *testing.T) {
	cache := NewCache(time.Hour, 1000)

	expected := &ValidationResult{
		Success: true,
		Errors:  []ValidationError{},
		Metrics: ComplexityMetrics{HandlerCallCount: 5},
	}

	cache.Set("test script", expected)
	result, ok := cache.Get("test script")

	assert.True(t, ok, "expected cache hit")
	require.NotNil(t, result)
	assert.Equal(t, expected.Success, result.Success)
	assert.Equal(t, expected.Metrics.HandlerCallCount, result.Metrics.HandlerCallCount)
}

func TestValidationCache_DifferentScriptsMiss(t *testing.T) {
	cache := NewCache(time.Hour, 1000)

	result1 := &ValidationResult{Success: true}
	cache.Set("script 1", result1)

	result, ok := cache.Get("script 2")
	assert.False(t, ok, "expected cache miss for different script")
	assert.Nil(t, result)
}

func TestValidationCache_SameHashForIdenticalScripts(t *testing.T) {
	cache := NewCache(time.Hour, 1000)

	expected := &ValidationResult{Success: true}
	cache.Set("identical script", expected)

	// Same content should produce same hash and cache hit
	result, ok := cache.Get("identical script")
	assert.True(t, ok, "expected cache hit for identical script")
	require.NotNil(t, result)
	assert.Equal(t, expected.Success, result.Success)
}

func TestValidationCache_TTLExpiration(t *testing.T) {
	// Use very short TTL for testing
	cache := NewCache(50*time.Millisecond, 1000)

	expected := &ValidationResult{Success: true}
	cache.Set("expiring script", expected)

	// Should hit immediately
	result, ok := cache.Get("expiring script")
	assert.True(t, ok, "expected cache hit before TTL")
	require.NotNil(t, result)

	// Wait for TTL to expire, polling until cache misses
	require.NoError(t, await.New().AtMost(500*time.Millisecond).PollInterval(10*time.Millisecond).Until(func() bool {
		result, ok = cache.Get("expiring script")
		return !ok
	}), "expected cache miss after TTL expiration")
	assert.Nil(t, result)
}

func TestValidationCache_LRUEviction(t *testing.T) {
	// Create small cache to test LRU eviction
	cache := NewCache(time.Hour, 3)

	// Add 3 entries
	cache.Set("script1", &ValidationResult{Success: true})
	cache.Set("script2", &ValidationResult{Success: true})
	cache.Set("script3", &ValidationResult{Success: true})

	// All 3 should be present
	_, ok1 := cache.Get("script1")
	_, ok2 := cache.Get("script2")
	_, ok3 := cache.Get("script3")
	assert.True(t, ok1 && ok2 && ok3, "all 3 entries should be present")

	// Access script1 to make it most recently used
	cache.Get("script1")

	// Add 4th entry - should evict script2 (least recently used)
	cache.Set("script4", &ValidationResult{Success: true})

	// script1, script3, script4 should be present; script2 should be evicted
	_, ok1 = cache.Get("script1")
	_, ok2 = cache.Get("script2")
	_, ok3 = cache.Get("script3")
	_, ok4 := cache.Get("script4")

	assert.True(t, ok1, "script1 should still be present (was accessed)")
	assert.False(t, ok2, "script2 should be evicted (LRU)")
	assert.True(t, ok3, "script3 should still be present")
	assert.True(t, ok4, "script4 should be present")
}

func TestValidationCache_EvictExpired(t *testing.T) {
	cache := NewCache(50*time.Millisecond, 1000)

	cache.Set("script1", &ValidationResult{Success: true})
	cache.Set("script2", &ValidationResult{Success: true})

	// Wait for TTL to expire, polling until script1 is gone
	require.NoError(t, await.New().AtMost(500*time.Millisecond).PollInterval(10*time.Millisecond).Until(func() bool {
		_, expired := cache.Get("script1")
		return !expired
	}))

	// Add a new entry that won't be expired
	cache.Set("script3", &ValidationResult{Success: true})

	// Evict expired entries
	cache.EvictExpired()

	// script1 and script2 should be gone, script3 should remain
	_, ok1 := cache.Get("script1")
	_, ok2 := cache.Get("script2")
	_, ok3 := cache.Get("script3")

	assert.False(t, ok1, "script1 should be evicted (expired)")
	assert.False(t, ok2, "script2 should be evicted (expired)")
	assert.True(t, ok3, "script3 should still be present")
}

func TestValidationCache_ConcurrentAccess(_ *testing.T) {
	cache := NewCache(time.Hour, 1000)

	const numGoroutines = 50
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				script := "script content " + string(rune('A'+(id+j)%26))
				cache.Set(script, &ValidationResult{Success: true})
				cache.Get(script)
			}
		}(i)
	}

	wg.Wait()
	// If we get here without deadlock or panic, concurrent access is safe
}

func TestValidationCache_ConcurrentMixedOperations(_ *testing.T) {
	cache := NewCache(100*time.Millisecond, 100)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start background eviction
	cache.Start(ctx)

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				script := "mixed script " + string(rune('A'+(id+j)%26))
				cache.Set(script, &ValidationResult{Success: true})
				cache.Get(script)
				time.Sleep(5 * time.Millisecond) //nolint:forbidigo // simulates realistic concurrent access timing between operations
			}
		}(i)
	}

	wg.Wait()
	// Test passes if no deadlock or race condition
}

func TestValidationCache_StartStop(t *testing.T) {
	cache := NewCache(50*time.Millisecond, 1000)

	ctx, cancel := context.WithCancel(context.Background())

	cache.Set("script1", &ValidationResult{Success: true})

	// Start background eviction
	cache.Start(ctx)

	// Wait for background eviction to remove the expired entry
	require.NoError(t, await.New().AtMost(1*time.Second).PollInterval(20*time.Millisecond).Until(func() bool {
		_, ok := cache.Get("script1")
		return !ok
	}))

	// Cancel context to stop background eviction
	cancel()

	// Give goroutine time to stop
	time.Sleep(50 * time.Millisecond) //nolint:forbidigo // goroutine lifecycle: brief wait for background goroutine to observe context cancellation

	// Cache should still work after stop
	cache.Set("script2", &ValidationResult{Success: true})
	result, ok := cache.Get("script2")
	assert.True(t, ok, "cache should work after background eviction stopped")
	require.NotNil(t, result)
}

func TestValidationCache_Size(t *testing.T) {
	cache := NewCache(time.Hour, 1000)

	assert.Equal(t, 0, cache.Size(), "empty cache should have size 0")

	cache.Set("script1", &ValidationResult{Success: true})
	assert.Equal(t, 1, cache.Size())

	cache.Set("script2", &ValidationResult{Success: true})
	assert.Equal(t, 2, cache.Size())

	// Same script should not increase size
	cache.Set("script1", &ValidationResult{Success: false})
	assert.Equal(t, 2, cache.Size())
}

func TestValidationCache_Clear(t *testing.T) {
	cache := NewCache(time.Hour, 1000)

	cache.Set("script1", &ValidationResult{Success: true})
	cache.Set("script2", &ValidationResult{Success: true})

	cache.Clear()

	assert.Equal(t, 0, cache.Size())
	_, ok := cache.Get("script1")
	assert.False(t, ok, "cache should be empty after clear")
}

func TestValidationCache_ZeroTTL(t *testing.T) {
	// Zero TTL means no expiration
	cache := NewCache(0, 1000)

	cache.Set("script", &ValidationResult{Success: true})

	time.Sleep(10 * time.Millisecond) //nolint:forbidigo // intentional: verifies zero-TTL entries do NOT expire (no condition to poll against)

	result, ok := cache.Get("script")
	assert.True(t, ok, "entry should not expire with zero TTL")
	require.NotNil(t, result)
}

func TestValidationCache_UnlimitedSize(t *testing.T) {
	// Zero maxSize means unlimited
	cache := NewCache(time.Hour, 0)

	for i := 0; i < 100; i++ {
		cache.Set("script "+string(rune('A'+i%26))+string(rune('0'+i/26)), &ValidationResult{Success: true})
	}

	assert.Equal(t, 100, cache.Size(), "unlimited cache should hold all entries")
}

func BenchmarkValidationCache_Get(b *testing.B) {
	cache := NewCache(time.Hour, 10000)

	// Pre-populate cache
	for i := 0; i < 1000; i++ {
		cache.Set("benchmark script "+string(rune(i)), &ValidationResult{Success: true})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get("benchmark script " + string(rune(i%1000)))
	}
}

func BenchmarkValidationCache_Set(b *testing.B) {
	cache := NewCache(time.Hour, 10000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set("benchmark script "+string(rune(i%1000)), &ValidationResult{Success: true})
	}
}

func BenchmarkValidationCache_ConcurrentAccess(b *testing.B) {
	cache := NewCache(time.Hour, 10000)

	// Pre-populate cache
	for i := 0; i < 1000; i++ {
		cache.Set("benchmark script "+string(rune(i)), &ValidationResult{Success: true})
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			script := "benchmark script " + string(rune(i%1000))
			cache.Get(script)
			cache.Set(script, &ValidationResult{Success: true})
			i++
		}
	})
}
