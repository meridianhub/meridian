package gateway

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewInMemorySlugCache(t *testing.T) {
	t.Run("creates cache with default settings", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		assert.NotNil(t, cache)
		assert.Equal(t, DefaultCacheTTL, cache.ttl)
		assert.Equal(t, DefaultCleanupInterval, cache.cleanupInterval)
	})

	t.Run("creates cache with custom TTL", func(t *testing.T) {
		customTTL := 10 * time.Minute
		cache := NewInMemorySlugCache(WithTTL(customTTL))
		defer cache.Stop()

		assert.Equal(t, customTTL, cache.ttl)
	})

	t.Run("creates cache with custom cleanup interval", func(t *testing.T) {
		customInterval := 30 * time.Second
		cache := NewInMemorySlugCache(WithCleanupInterval(customInterval))
		defer cache.Stop()

		assert.Equal(t, customInterval, cache.cleanupInterval)
	})

	t.Run("ignores invalid TTL", func(t *testing.T) {
		cache := NewInMemorySlugCache(WithTTL(0))
		defer cache.Stop()

		assert.Equal(t, DefaultCacheTTL, cache.ttl)
	})

	t.Run("ignores invalid cleanup interval", func(t *testing.T) {
		cache := NewInMemorySlugCache(WithCleanupInterval(-1 * time.Second))
		defer cache.Stop()

		assert.Equal(t, DefaultCleanupInterval, cache.cleanupInterval)
	})
}

func TestInMemorySlugCache_Get(t *testing.T) {
	ctx := context.Background()

	t.Run("returns empty TenantID for cache miss", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		tenantID, err := cache.Get(ctx, "nonexistent")

		require.NoError(t, err)
		assert.True(t, tenantID.IsEmpty(), "should return empty TenantID for cache miss")
	})

	t.Run("returns cached tenant ID on hit", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		expectedTenantID := tenant.MustNewTenantID("tenant_123")
		err := cache.Set(ctx, "acme", expectedTenantID)
		require.NoError(t, err)

		tenantID, err := cache.Get(ctx, "acme")

		require.NoError(t, err)
		assert.Equal(t, expectedTenantID, tenantID)
	})

	t.Run("returns empty TenantID for empty slug", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		tenantID, err := cache.Get(ctx, "")

		require.NoError(t, err)
		assert.True(t, tenantID.IsEmpty())
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		tenantID, err := cache.Get(cancelledCtx, "acme")

		assert.ErrorIs(t, err, context.Canceled)
		assert.True(t, tenantID.IsEmpty())
	})
}

func TestInMemorySlugCache_Set(t *testing.T) {
	ctx := context.Background()

	t.Run("stores tenant ID successfully", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		expectedTenantID := tenant.MustNewTenantID("tenant_123")
		err := cache.Set(ctx, "acme", expectedTenantID)
		require.NoError(t, err)

		tenantID, err := cache.Get(ctx, "acme")
		require.NoError(t, err)
		assert.Equal(t, expectedTenantID, tenantID)
	})

	t.Run("overwrites existing entry", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		firstTenantID := tenant.MustNewTenantID("tenant_1")
		secondTenantID := tenant.MustNewTenantID("tenant_2")

		err := cache.Set(ctx, "acme", firstTenantID)
		require.NoError(t, err)

		err = cache.Set(ctx, "acme", secondTenantID)
		require.NoError(t, err)

		tenantID, err := cache.Get(ctx, "acme")
		require.NoError(t, err)
		assert.Equal(t, secondTenantID, tenantID)
	})

	t.Run("ignores empty slug", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		err := cache.Set(ctx, "", tenant.MustNewTenantID("tenant_123"))
		require.NoError(t, err)
		assert.Equal(t, 0, cache.Size())
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		err := cache.Set(cancelledCtx, "acme", tenant.MustNewTenantID("tenant_123"))

		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 0, cache.Size())
	})
}

func TestInMemorySlugCache_TTLExpiration(t *testing.T) {
	ctx := context.Background()

	t.Run("expired entries return empty TenantID", func(t *testing.T) {
		// Use very short TTL for testing
		cache := NewInMemorySlugCache(
			WithTTL(50*time.Millisecond),
			WithCleanupInterval(1*time.Hour), // Disable auto cleanup for this test
		)
		defer cache.Stop()

		tenantID := tenant.MustNewTenantID("tenant_123")
		err := cache.Set(ctx, "acme", tenantID)
		require.NoError(t, err)

		// Verify entry exists
		result, err := cache.Get(ctx, "acme")
		require.NoError(t, err)
		assert.Equal(t, tenantID, result)

		// Wait for expiration using await
		err = await.New().
			AtMost(1 * time.Second).
			PollInterval(10 * time.Millisecond).
			Until(func() bool {
				result, _ := cache.Get(ctx, "acme")
				return result.IsEmpty()
			})

		require.NoError(t, err, "entry should have expired")
	})

	t.Run("non-expired entries are returned", func(t *testing.T) {
		cache := NewInMemorySlugCache(WithTTL(1 * time.Hour))
		defer cache.Stop()

		tenantID := tenant.MustNewTenantID("tenant_123")
		err := cache.Set(ctx, "acme", tenantID)
		require.NoError(t, err)

		// Should still be valid
		result, err := cache.Get(ctx, "acme")
		require.NoError(t, err)
		assert.Equal(t, tenantID, result)
	})
}

func TestInMemorySlugCache_BackgroundCleanup(t *testing.T) {
	ctx := context.Background()

	t.Run("removes expired entries periodically", func(t *testing.T) {
		cache := NewInMemorySlugCache(
			WithTTL(50*time.Millisecond),
			WithCleanupInterval(100*time.Millisecond),
		)
		defer cache.Stop()

		tenantID := tenant.MustNewTenantID("tenant_123")
		err := cache.Set(ctx, "acme", tenantID)
		require.NoError(t, err)

		assert.Equal(t, 1, cache.Size())

		// Wait for cleanup to remove expired entry
		err = await.New().
			AtMost(1 * time.Second).
			PollInterval(20 * time.Millisecond).
			Until(func() bool {
				return cache.Size() == 0
			})

		require.NoError(t, err, "cleanup should have removed expired entry")
	})

	t.Run("keeps non-expired entries", func(t *testing.T) {
		cache := NewInMemorySlugCache(
			WithTTL(1*time.Hour),
			WithCleanupInterval(50*time.Millisecond),
		)
		defer cache.Stop()

		tenantID := tenant.MustNewTenantID("tenant_123")
		err := cache.Set(ctx, "acme", tenantID)
		require.NoError(t, err)

		// Intentional sleep: Wait for multiple cleanup cycles to run (50ms interval x 3 = 150ms)
		// to verify they don't remove entries that haven't expired (1 hour TTL).
		time.Sleep(150 * time.Millisecond) //nolint:forbidigo // verifies non-expired entries are NOT removed — no condition to poll against

		// Entry should still exist
		assert.Equal(t, 1, cache.Size())
		result, err := cache.Get(ctx, "acme")
		require.NoError(t, err)
		assert.Equal(t, tenantID, result)
	})
}

func TestInMemorySlugCache_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()

	t.Run("handles concurrent reads and writes safely", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		const numGoroutines = 100
		const numOperations = 50

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()

				slug := "slug"
				tenantID := tenant.MustNewTenantID("tenant_123")

				for j := 0; j < numOperations; j++ {
					// Alternate between reads and writes
					if j%2 == 0 {
						_, err := cache.Get(ctx, slug)
						assert.NoError(t, err)
					} else {
						err := cache.Set(ctx, slug, tenantID)
						assert.NoError(t, err)
					}
				}
			}()
		}

		wg.Wait()
	})

	t.Run("concurrent writes to different keys", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		const numGoroutines = 100

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func(idx int) {
				defer wg.Done()

				slug := fmt.Sprintf("slug_%d", idx)
				tenantID := tenant.MustNewTenantID(fmt.Sprintf("tenant_%d", idx))

				err := cache.Set(ctx, slug, tenantID)
				assert.NoError(t, err)

				result, err := cache.Get(ctx, slug)
				assert.NoError(t, err)
				assert.False(t, result.IsEmpty(), "should have cached value")
			}(i)
		}

		wg.Wait()
	})
}

func TestInMemorySlugCache_Stop(t *testing.T) {
	t.Run("stops cleanup goroutine", func(t *testing.T) {
		cache := NewInMemorySlugCache(WithCleanupInterval(10 * time.Millisecond))

		// Stop should complete without hanging
		done := make(chan struct{})
		go func() {
			cache.Stop()
			close(done)
		}()

		select {
		case <-done:
			// Success - Stop returned
		case <-time.After(1 * time.Second):
			t.Fatal("Stop() did not return within timeout")
		}
	})

	t.Run("is safe to call multiple times", func(_ *testing.T) {
		cache := NewInMemorySlugCache()

		// Should not panic
		cache.Stop()
		cache.Stop()
		cache.Stop()
	})
}

func TestInMemorySlugCache_Size(t *testing.T) {
	ctx := context.Background()

	t.Run("returns correct count", func(t *testing.T) {
		cache := NewInMemorySlugCache()
		defer cache.Stop()

		assert.Equal(t, 0, cache.Size())

		err := cache.Set(ctx, "a", tenant.MustNewTenantID("t1"))
		require.NoError(t, err)
		assert.Equal(t, 1, cache.Size())

		err = cache.Set(ctx, "b", tenant.MustNewTenantID("t2"))
		require.NoError(t, err)
		assert.Equal(t, 2, cache.Size())

		err = cache.Set(ctx, "c", tenant.MustNewTenantID("t3"))
		require.NoError(t, err)
		assert.Equal(t, 3, cache.Size())
	})
}

// Compile-time interface check
var _ slugCache = (*InMemorySlugCache)(nil)
