package cache

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/await"
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
	sagaName, found, err := cache.Get(ctx, "tenant-1", "/v1/payments")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)
	assert.Equal(t, 1, source.refreshCount("tenant-1"))

	// Second call should use cache (no additional source call)
	sagaName, found, err = cache.Get(ctx, "tenant-1", "/v1/payments")
	require.NoError(t, err)
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

	sagaName, found, err := cache.Get(ctx, "tenant-1", "/v1/unknown")
	require.NoError(t, err)
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
	sagaName, found, err := cache.Get(ctx, "tenant-1", "/v1/payments")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)
	assert.Equal(t, 1, source.refreshCount("tenant-1"))

	// Invalidate tenant
	err = cache.Invalidate(ctx, "tenant-1")
	require.NoError(t, err)

	// Update source to return different binding
	source.mu.Lock()
	source.bindings["tenant-1"]["/v1/payments"] = "process_payment_v2"
	source.mu.Unlock()

	// Next Get should trigger re-fetch from source
	sagaName, found, err = cache.Get(ctx, "tenant-1", "/v1/payments")
	require.NoError(t, err)
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

	sagaName, found, err := cache.Get(ctx, "tenant-1", "/v1/payments")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)

	sagaName, found, err = cache.Get(ctx, "tenant-1", "/v1/settlements")
	require.NoError(t, err)
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
	sagaName, found, err := cache.Get(ctx, "tenant-1", "/v1/payments")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)
	assert.Equal(t, 1, source.refreshCount("tenant-1"))

	// Wait for TTL to expire, then trigger a re-fetch
	var getErr error
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(1 * time.Millisecond).
		Until(func() bool {
			sagaName, found, getErr = cache.Get(ctx, "tenant-1", "/v1/payments")
			return source.refreshCount("tenant-1") >= 2
		})
	require.NoError(t, err)
	require.NoError(t, getErr)
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)
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
			sagaName, found, err := cache.Get(ctx, "tenant-1", "/v1/payments")
			assert.NoError(t, err)
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

	sagaName, found, err := cache.Get(ctx1, "tenant-1", "/v1/payments")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "process_payment_v1", sagaName)

	sagaName, found, err = cache.Get(ctx2, "tenant-2", "/v1/payments")
	require.NoError(t, err)
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
	_, _, err := cache.Get(ctx1, "tenant-1", "/v1/payments")
	require.NoError(t, err)
	_, _, err = cache.Get(ctx2, "tenant-2", "/v1/payments")
	require.NoError(t, err)

	assert.Equal(t, 1, source.refreshCount("tenant-1"))
	assert.Equal(t, 1, source.refreshCount("tenant-2"))

	// Invalidate tenant-1 only
	err = cache.Invalidate(ctx1, "tenant-1")
	require.NoError(t, err)

	// Access tenant-2 should still use cache
	_, _, err = cache.Get(ctx2, "tenant-2", "/v1/payments")
	require.NoError(t, err)
	assert.Equal(t, 1, source.refreshCount("tenant-2"))

	// Access tenant-1 should trigger refresh
	_, _, err = cache.Get(ctx1, "tenant-1", "/v1/payments")
	require.NoError(t, err)
	assert.Equal(t, 2, source.refreshCount("tenant-1"))
}

func TestSagaBindingCache_SourceReturnsEmpty(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))

	ctx := tenant.WithTenant(context.Background(), "tenant-1")

	sagaName, found, err := cache.Get(ctx, "tenant-1", "/v1/payments")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, sagaName)
}

func TestSagaBindingCache_NilSource_Panics(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		NewSagaBindingCache(nil)
	})
}

func TestSagaBindingCache_Get_SourceError_ReturnsError(t *testing.T) {
	t.Parallel()

	source := &errorSagaBindingSource{
		err: fmt.Errorf("database connection refused"),
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))

	ctx := tenant.WithTenant(context.Background(), "tenant-1")

	_, found, err := cache.Get(ctx, "tenant-1", "/v1/payments")
	assert.False(t, found)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database connection refused")
}

// errorSagaBindingSource always returns an error.
type errorSagaBindingSource struct {
	err error
}

func (e *errorSagaBindingSource) GetBindingsForTenant(_ context.Context, _ string) (map[string]string, error) {
	return nil, e.err
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

// --- Option Tests ---

func TestSagaBindingCache_WithSagaBindingTTL(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(10*time.Minute))
	assert.Equal(t, 10*time.Minute, cache.ttl)
}

func TestSagaBindingCache_WithSagaBindingTTL_NegativeIgnored(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{},
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(-1*time.Minute))
	assert.Equal(t, DefaultSagaBindingTTL, cache.ttl)
}

func TestSagaBindingCache_WithSagaBindingLogger(t *testing.T) {
	t.Parallel()

	source := &mockSagaBindingSource{
		bindings: map[string]map[string]string{},
	}

	logger := slog.Default()
	cache := NewSagaBindingCache(source, WithSagaBindingLogger(logger))
	assert.Equal(t, logger, cache.logger)
}

func TestSagaBindingCache_Refresh_ClearsAndReloads(t *testing.T) {
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
	sagaName, found, err := cache.Get(ctx, "tenant-1", "/v1/payments")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "process_payment", sagaName)

	// Update source
	source.mu.Lock()
	source.bindings["tenant-1"]["/v1/payments"] = "process_payment_v3"
	source.mu.Unlock()

	// Refresh forces reload
	err = cache.Refresh(ctx, "tenant-1")
	require.NoError(t, err)

	// Should get updated binding
	sagaName, found, err = cache.Get(ctx, "tenant-1", "/v1/payments")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "process_payment_v3", sagaName)
}

func TestSagaBindingCache_Refresh_SourceError(t *testing.T) {
	t.Parallel()

	source := &errorSagaBindingSource{
		err: fmt.Errorf("database unavailable"),
	}

	cache := NewSagaBindingCache(source, WithSagaBindingTTL(5*time.Minute))
	ctx := tenant.WithTenant(context.Background(), "tenant-1")

	err := cache.Refresh(ctx, "tenant-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database unavailable")
}
