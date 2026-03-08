package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

func TestSagaBindingCache_Get_CacheHit(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{
			"tenant-1": {
				"/v1/payments": "process_payment",
			},
		},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))

	ctx := tenant.WithTenant(context.Background(), "tenant-1")

	// First call triggers source refresh
	sagaName, found := cache.Get(ctx, "tenant-1", "/v1/payments")
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)
	assert.Equal(t, 1, source.refreshCount("tenant-1"))

	// Second call should use cache (no additional source call)
	sagaName, found = cache.Get(ctx, "tenant-1", "/v1/payments")
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)
	assert.Equal(t, 1, source.refreshCount("tenant-1"))
}

func TestSagaBindingCache_Get_CacheMiss_NoBinding(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{
			"tenant-1": {
				"/v1/payments": "process_payment",
			},
		},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))

	ctx := tenant.WithTenant(context.Background(), "tenant-1")

	sagaName, found := cache.Get(ctx, "tenant-1", "/v1/unknown")
	assert.False(t, found)
	assert.Empty(t, sagaName)
}

func TestSagaBindingCache_Invalidate_ClearsTenantBindings(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{
			"tenant-1": {
				"/v1/payments": "process_payment",
			},
		},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))

	ctx := tenant.WithTenant(context.Background(), "tenant-1")

	// Populate cache
	sagaName, found := cache.Get(ctx, "tenant-1", "/v1/payments")
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)
	assert.Equal(t, 1, source.refreshCount("tenant-1"))

	// Invalidate tenant
	err := cache.Invalidate(ctx, "tenant-1")
	require.NoError(t, err)

	// Update source to return different binding
	source.mu.Lock()
	source.bindings["tenant-1"]["/v1/payments"] = "process_payment_v2"
	source.mu.Unlock()

	// Next Get should trigger re-fetch from source
	sagaName, found = cache.Get(ctx, "tenant-1", "/v1/payments")
	require.True(t, found)
	assert.Equal(t, "process_payment_v2", sagaName)
	assert.Equal(t, 2, source.refreshCount("tenant-1"))
}

func TestSagaBindingCache_Refresh_PopulatesCache(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{
			"tenant-1": {
				"/v1/payments":    "process_payment",
				"/v1/settlements": "settle_trade",
			},
		},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))

	ctx := tenant.WithTenant(context.Background(), "tenant-1")

	err := cache.Refresh(ctx, "tenant-1")
	require.NoError(t, err)

	sagaName, found := cache.Get(ctx, "tenant-1", "/v1/payments")
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)

	sagaName, found = cache.Get(ctx, "tenant-1", "/v1/settlements")
	require.True(t, found)
	assert.Equal(t, "settle_trade", sagaName)

	// Only one refresh call
	assert.Equal(t, 1, source.refreshCount("tenant-1"))
}

func TestSagaBindingCache_TTLExpiry_TriggersRefresh(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{
			"tenant-1": {
				"/v1/payments": "process_payment",
			},
		},
	}

	// Use a very short TTL
	cache := NewSagaBindingCache(source, WithSagaBindingTTL(1*time.Millisecond))

	ctx := tenant.WithTenant(context.Background(), "tenant-1")

	// First call
	sagaName, found := cache.Get(ctx, "tenant-1", "/v1/payments")
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)
	assert.Equal(t, 1, source.refreshCount("tenant-1"))

	// Wait for TTL to expire
	time.Sleep(5 * time.Millisecond)

	// Should trigger a re-fetch
	sagaName, found = cache.Get(ctx, "tenant-1", "/v1/payments")
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)
	assert.Equal(t, 2, source.refreshCount("tenant-1"))
}

func TestSagaBindingCache_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{
			"tenant-1": {
				"/v1/payments": "process_payment",
			},
		},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))

	ctx := tenant.WithTenant(context.Background(), "tenant-1")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sagaName, found := cache.Get(ctx, "tenant-1", "/v1/payments")
			assert.True(t, found)
			assert.Equal(t, "process_payment", sagaName)
		}()
	}
	wg.Wait()

	// Source should be called fewer times than goroutines (singleflight dedup)
	assert.Less(t, source.refreshCount("tenant-1"), 100)
}

func TestSagaBindingCache_MultipleTenants(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{
			"tenant-1": {
				"/v1/payments": "process_payment_v1",
			},
			"tenant-2": {
				"/v1/payments": "process_payment_v2",
			},
		},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))

	ctx1 := tenant.WithTenant(context.Background(), "tenant-1")
	ctx2 := tenant.WithTenant(context.Background(), "tenant-2")

	sagaName, found := cache.Get(ctx1, "tenant-1", "/v1/payments")
	require.True(t, found)
	assert.Equal(t, "process_payment_v1", sagaName)

	sagaName, found = cache.Get(ctx2, "tenant-2", "/v1/payments")
	require.True(t, found)
	assert.Equal(t, "process_payment_v2", sagaName)
}

func TestSagaBindingCache_Invalidate_OnlyAffectsSpecifiedTenant(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{
			"tenant-1": {
				"/v1/payments": "process_payment_v1",
			},
			"tenant-2": {
				"/v1/payments": "process_payment_v2",
			},
		},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))

	ctx1 := tenant.WithTenant(context.Background(), "tenant-1")
	ctx2 := tenant.WithTenant(context.Background(), "tenant-2")

	// Populate both tenants
	cache.Get(ctx1, "tenant-1", "/v1/payments")
	cache.Get(ctx2, "tenant-2", "/v1/payments")

	assert.Equal(t, 1, source.refreshCount("tenant-1"))
	assert.Equal(t, 1, source.refreshCount("tenant-2"))

	// Invalidate tenant-1 only
	err := cache.Invalidate(ctx1, "tenant-1")
	require.NoError(t, err)

	// Access tenant-2 should still use cache
	cache.Get(ctx2, "tenant-2", "/v1/payments")
	assert.Equal(t, 1, source.refreshCount("tenant-2"))

	// Access tenant-1 should trigger refresh
	cache.Get(ctx1, "tenant-1", "/v1/payments")
	assert.Equal(t, 2, source.refreshCount("tenant-1"))
}

func TestSagaBindingCache_SourceReturnsEmpty(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))

	ctx := tenant.WithTenant(context.Background(), "tenant-1")

	sagaName, found := cache.Get(ctx, "tenant-1", "/v1/payments")
	assert.False(t, found)
	assert.Empty(t, sagaName)
}

// mockSagaBindingSource is a test double for SagaBindingSource.
type mockSagaBindingSource struct {
	mu       sync.Mutex
	bindings map[string]map[string]string
	calls    map[string]int
}

func (m *mockSagaBindingSource) GetBindingsForTenant(_ context.Context, tenantID string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.calls == nil {
		m.calls = make(map[string]int)
	}
	m.calls[tenantID]++

	bindings := m.bindings[tenantID]
	if bindings == nil {
		return map[string]string{}, nil
	}

	// Return a copy
	result := make(map[string]string, len(bindings))
	for k, v := range bindings {
		result[k] = v
	}
	return result, nil
}

func (m *mockSagaBindingSource) refreshCount(tenantID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[tenantID]
}
