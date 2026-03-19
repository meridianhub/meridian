package cache

import (
	"context"
	"errors"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// mockRegistryLoader is a test implementation of RegistryLoader.
type mockRegistryLoader struct {
	instruments       map[string][]*registry.InstrumentDefinition // keyed by tenant ID
	compileProgramsFn func(def *registry.InstrumentDefinition) (cel.Program, cel.Program, error)
	listActiveErr     error
}

func newMockRegistryLoader() *mockRegistryLoader {
	return &mockRegistryLoader{
		instruments: make(map[string][]*registry.InstrumentDefinition),
		compileProgramsFn: func(_ *registry.InstrumentDefinition) (cel.Program, cel.Program, error) {
			return nil, nil, nil // Default: no CEL programs
		},
	}
}

func (m *mockRegistryLoader) ListActive(ctx context.Context) ([]*registry.InstrumentDefinition, error) {
	if m.listActiveErr != nil {
		return nil, m.listActiveErr
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrTenantContextRequired
	}

	return m.instruments[string(tenantID)], nil
}

func (m *mockRegistryLoader) CompilePrograms(def *registry.InstrumentDefinition) (cel.Program, cel.Program, error) {
	return m.compileProgramsFn(def)
}

func (m *mockRegistryLoader) addInstrument(tenantID string, def *registry.InstrumentDefinition) {
	m.instruments[tenantID] = append(m.instruments[tenantID], def)
}

func newTestDefinition(code string, version int) *registry.InstrumentDefinition {
	return &registry.InstrumentDefinition{
		ID:        uuid.New(),
		Code:      code,
		Version:   version,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
		Status:    registry.StatusActive,
	}
}

func TestPrefetcher_Prefetch_LoadsAllActiveInstruments(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()

	// Add test instruments for tenant1
	loader.addInstrument("tenant1", newTestDefinition("USD", 1))
	loader.addInstrument("tenant1", newTestDefinition("EUR", 1))
	loader.addInstrument("tenant1", newTestDefinition("GBP", 1))

	prefetcher := NewPrefetcher(cache, loader)
	ctx := newTestContext("tenant1")

	// Prefetch should load all instruments
	err := prefetcher.Prefetch(ctx)
	require.NoError(t, err)

	// Verify all instruments are cached
	assert.NotNil(t, cache.Get(ctx, "USD", 1), "USD should be cached")
	assert.NotNil(t, cache.Get(ctx, "EUR", 1), "EUR should be cached")
	assert.NotNil(t, cache.Get(ctx, "GBP", 1), "GBP should be cached")

	// Verify cache stats
	size, _ := cache.Stats(ctx)
	assert.Equal(t, 3, size)
}

func TestPrefetcher_Prefetch_MissingTenantContext(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()
	prefetcher := NewPrefetcher(cache, loader)

	ctx := context.Background() // No tenant

	err := prefetcher.Prefetch(ctx)
	assert.ErrorIs(t, err, ErrTenantContextRequired)
}

func TestPrefetcher_Prefetch_RegistryError(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()
	loader.listActiveErr = errors.New("database connection failed")

	prefetcher := NewPrefetcher(cache, loader)
	ctx := newTestContext("tenant1")

	err := prefetcher.Prefetch(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list active instruments")
	assert.Contains(t, err.Error(), "database connection failed")
}

func TestPrefetcher_Prefetch_CELCompilationError(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()

	// Add instrument
	loader.addInstrument("tenant1", newTestDefinition("USD", 1))

	// Make CEL compilation fail
	loader.compileProgramsFn = func(_ *registry.InstrumentDefinition) (cel.Program, cel.Program, error) {
		return nil, nil, errors.New("invalid CEL expression")
	}

	prefetcher := NewPrefetcher(cache, loader)
	ctx := newTestContext("tenant1")

	err := prefetcher.Prefetch(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compile CEL programs")
	assert.Contains(t, err.Error(), "USD")
}

func TestPrefetcher_Prefetch_EmptyRegistry(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()
	// No instruments added

	prefetcher := NewPrefetcher(cache, loader)
	ctx := newTestContext("tenant1")

	// Should succeed with empty result
	err := prefetcher.Prefetch(ctx)
	require.NoError(t, err)

	// Cache should be empty
	size, _ := cache.Stats(ctx)
	assert.Equal(t, 0, size)
}

func TestPrefetcher_PrefetchCompletionTracking(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()
	prefetcher := NewPrefetcher(cache, loader)

	// Initially not complete
	assert.False(t, prefetcher.IsPrefetchComplete())

	// Manual completion
	prefetcher.MarkPrefetchComplete()
	assert.True(t, prefetcher.IsPrefetchComplete())

	// Reset
	prefetcher.ResetPrefetchStatus()
	assert.False(t, prefetcher.IsPrefetchComplete())
}

func TestPrefetcher_PrefetchMultipleTenants_Success(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()

	// Add instruments for multiple tenants
	loader.addInstrument("tenant1", newTestDefinition("USD", 1))
	loader.addInstrument("tenant1", newTestDefinition("EUR", 1))
	loader.addInstrument("tenant2", newTestDefinition("JPY", 1))
	loader.addInstrument("tenant2", newTestDefinition("CNY", 1))
	loader.addInstrument("tenant3", newTestDefinition("GBP", 1))

	prefetcher := NewPrefetcher(cache, loader)

	tenantIDs := []tenant.TenantID{
		tenant.MustNewTenantID("tenant1"),
		tenant.MustNewTenantID("tenant2"),
		tenant.MustNewTenantID("tenant3"),
	}

	err := prefetcher.PrefetchMultipleTenants(context.Background(), tenantIDs)
	require.NoError(t, err)

	// Verify completion status
	assert.True(t, prefetcher.IsPrefetchComplete())

	// Verify each tenant's instruments are cached
	ctx1 := newTestContext("tenant1")
	assert.NotNil(t, cache.Get(ctx1, "USD", 1))
	assert.NotNil(t, cache.Get(ctx1, "EUR", 1))

	ctx2 := newTestContext("tenant2")
	assert.NotNil(t, cache.Get(ctx2, "JPY", 1))
	assert.NotNil(t, cache.Get(ctx2, "CNY", 1))

	ctx3 := newTestContext("tenant3")
	assert.NotNil(t, cache.Get(ctx3, "GBP", 1))
}

func TestPrefetcher_PrefetchMultipleTenants_PartialFailure(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()

	// Add instruments for tenant1 only
	loader.addInstrument("tenant1", newTestDefinition("USD", 1))
	// tenant2 will fail with CEL compilation error

	loader.addInstrument("tenant2", newTestDefinition("FAIL", 1))

	callCount := 0
	loader.compileProgramsFn = func(def *registry.InstrumentDefinition) (cel.Program, cel.Program, error) {
		callCount++
		if def.Code == "FAIL" {
			return nil, nil, errors.New("compilation error")
		}
		return nil, nil, nil
	}

	prefetcher := NewPrefetcher(cache, loader)

	tenantIDs := []tenant.TenantID{
		tenant.MustNewTenantID("tenant1"),
		tenant.MustNewTenantID("tenant2"),
		tenant.MustNewTenantID("tenant3"),
	}

	err := prefetcher.PrefetchMultipleTenants(context.Background(), tenantIDs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prefetch failed for tenant tenant2")

	// Prefetch should NOT be marked complete on failure
	assert.False(t, prefetcher.IsPrefetchComplete())

	// tenant1's instruments should be cached (completed before failure)
	ctx1 := newTestContext("tenant1")
	assert.NotNil(t, cache.Get(ctx1, "USD", 1))
}

func TestPrefetcher_PrefetchMultipleTenants_ContextCancellation(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()

	loader.addInstrument("tenant1", newTestDefinition("USD", 1))
	loader.addInstrument("tenant2", newTestDefinition("EUR", 1))

	prefetcher := NewPrefetcher(cache, loader)

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	tenantIDs := []tenant.TenantID{
		tenant.MustNewTenantID("tenant1"),
		tenant.MustNewTenantID("tenant2"),
	}

	err := prefetcher.PrefetchMultipleTenants(ctx, tenantIDs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prefetch cancelled")

	// Should not be marked complete
	assert.False(t, prefetcher.IsPrefetchComplete())
}

func TestPrefetcher_PrefetchMultipleTenants_EmptyTenantList(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()
	prefetcher := NewPrefetcher(cache, loader)

	err := prefetcher.PrefetchMultipleTenants(context.Background(), []tenant.TenantID{})
	require.NoError(t, err)

	// Should be marked complete even with empty list
	assert.True(t, prefetcher.IsPrefetchComplete())
}

func TestPrefetcher_PrefetchMultipleTenants_TenantIsolation(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()

	// Same code "USD" in both tenants
	loader.addInstrument("tenant1", newTestDefinition("USD", 1))
	loader.addInstrument("tenant2", newTestDefinition("USD", 1))

	prefetcher := NewPrefetcher(cache, loader)

	tenantIDs := []tenant.TenantID{
		tenant.MustNewTenantID("tenant1"),
		tenant.MustNewTenantID("tenant2"),
	}

	err := prefetcher.PrefetchMultipleTenants(context.Background(), tenantIDs)
	require.NoError(t, err)

	// Verify each tenant has their own cached USD
	ctx1 := newTestContext("tenant1")
	ctx2 := newTestContext("tenant2")

	usd1 := cache.Get(ctx1, "USD", 1)
	usd2 := cache.Get(ctx2, "USD", 1)

	require.NotNil(t, usd1)
	require.NotNil(t, usd2)

	// They should be different cache entries (different UUIDs)
	assert.NotEqual(t, usd1.Definition.ID, usd2.Definition.ID)
}

func TestNewPrefetcher(t *testing.T) {
	cache := NewInstrumentCache()
	loader := newMockRegistryLoader()
	prefetcher := NewPrefetcher(cache, loader)

	assert.NotNil(t, prefetcher)
	assert.NotNil(t, prefetcher.cache)
	assert.NotNil(t, prefetcher.loader)
	assert.False(t, prefetcher.IsPrefetchComplete())
}
