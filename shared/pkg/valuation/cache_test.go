package valuation_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/valuation"
	"github.com/meridianhub/meridian/shared/platform/await"
)

func TestInMemoryCache_MethodCaching(t *testing.T) {
	cache := valuation.NewInMemoryCache(valuation.InMemoryCacheConfig{
		MethodTTL: 5 * time.Minute,
		PolicyTTL: 5 * time.Minute,
	})

	method := &valuation.Method{
		ID:               "method-1",
		Version:          1,
		Name:             "retail_energy_tariff",
		Script:           "def valuate(ctx): return {'valued_amount': 100, 'instrument': 'GBP'}",
		OutputInstrument: "GBP",
	}

	// Cache miss on first lookup
	cached, err := cache.GetMethod("method-1", intPtr(1))
	require.NoError(t, err)
	assert.Nil(t, cached)

	// Set method
	err = cache.SetMethod(method)
	require.NoError(t, err)

	// Cache hit on second lookup
	cached, err = cache.GetMethod("method-1", intPtr(1))
	require.NoError(t, err)
	assert.NotNil(t, cached)
	assert.Equal(t, "method-1", cached.ID)
	assert.Equal(t, "retail_energy_tariff", cached.Name)
}

func TestInMemoryCache_MethodVersioning(t *testing.T) {
	cache := valuation.NewInMemoryCache(valuation.InMemoryCacheConfig{
		MethodTTL: 5 * time.Minute,
		PolicyTTL: 5 * time.Minute,
	})

	v1 := &valuation.Method{ID: "method-1", Version: 1, Name: "v1", Script: "v1"}
	v2 := &valuation.Method{ID: "method-1", Version: 2, Name: "v2", Script: "v2"}

	cache.SetMethod(v1)
	cache.SetMethod(v2)

	// Get specific versions
	cached, _ := cache.GetMethod("method-1", intPtr(1))
	assert.Equal(t, "v1", cached.Name)

	cached, _ = cache.GetMethod("method-1", intPtr(2))
	assert.Equal(t, "v2", cached.Name)

	// Get latest (nil version)
	cached, _ = cache.GetMethod("method-1", nil)
	assert.Nil(t, cached, "latest version lookup not yet implemented")
}

func TestInMemoryCache_MethodTTL(t *testing.T) {
	cache := valuation.NewInMemoryCache(valuation.InMemoryCacheConfig{
		MethodTTL: 50 * time.Millisecond,
		PolicyTTL: 5 * time.Minute,
	})

	method := &valuation.Method{
		ID:      "method-1",
		Version: 1,
		Script:  "test",
	}

	cache.SetMethod(method)

	// Immediate lookup succeeds
	cached, _ := cache.GetMethod("method-1", intPtr(1))
	assert.NotNil(t, cached)

	// Wait for TTL to expire, polling until GetMethod returns nil (expired)
	require.NoError(t, await.New().AtMost(500*time.Millisecond).PollInterval(10*time.Millisecond).Until(func() bool {
		cached, _ = cache.GetMethod("method-1", intPtr(1))
		return cached == nil
	}), "entry should be expired")
}

func TestInMemoryCache_PolicyCaching(t *testing.T) {
	cache := valuation.NewInMemoryCache(valuation.InMemoryCacheConfig{
		MethodTTL: 5 * time.Minute,
		PolicyTTL: 5 * time.Minute,
	})

	// Create a mock compiled policy
	runtime, _ := valuation.NewPolicyRuntime()
	policy, _ := runtime.CompilePolicy("amount * 1.5")

	// Cache miss
	cached, err := cache.GetPolicy("test_policy", intPtr(1))
	require.NoError(t, err)
	assert.Nil(t, cached)

	// Set policy
	err = cache.SetPolicy("test_policy", 1, policy)
	require.NoError(t, err)

	// Cache hit
	cached, err = cache.GetPolicy("test_policy", intPtr(1))
	require.NoError(t, err)
	assert.NotNil(t, cached)
	assert.Equal(t, "amount * 1.5", cached.Expression())
}

func TestInMemoryCache_Clear(t *testing.T) {
	cache := valuation.NewInMemoryCache(valuation.InMemoryCacheConfig{
		MethodTTL: 5 * time.Minute,
		PolicyTTL: 5 * time.Minute,
	})

	method := &valuation.Method{ID: "method-1", Version: 1, Script: "test"}
	cache.SetMethod(method)

	runtime, _ := valuation.NewPolicyRuntime()
	policy, _ := runtime.CompilePolicy("1 + 1")
	cache.SetPolicy("policy-1", 1, policy)

	// Verify entries exist
	cached, _ := cache.GetMethod("method-1", intPtr(1))
	assert.NotNil(t, cached)

	cachedPolicy, _ := cache.GetPolicy("policy-1", intPtr(1))
	assert.NotNil(t, cachedPolicy)

	// Clear cache
	cache.Clear()

	// Verify entries gone
	cached, _ = cache.GetMethod("method-1", intPtr(1))
	assert.Nil(t, cached)

	cachedPolicy, _ = cache.GetPolicy("policy-1", intPtr(1))
	assert.Nil(t, cachedPolicy)
}

func TestInMemoryCache_ThreadSafety(t *testing.T) {
	cache := valuation.NewInMemoryCache(valuation.InMemoryCacheConfig{
		MethodTTL: 5 * time.Minute,
		PolicyTTL: 5 * time.Minute,
	})

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			method := &valuation.Method{
				ID:      "method-1",
				Version: idx,
				Script:  "test",
			}
			cache.SetMethod(method)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Cache should not panic and should have some entries
	cached, _ := cache.GetMethod("method-1", intPtr(5))
	assert.NotNil(t, cached)
}

// Helper function
func intPtr(i int) *int {
	return &i
}
