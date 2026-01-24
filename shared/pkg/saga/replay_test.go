package saga

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupResultCache_GetSet(t *testing.T) {
	cache := NewLookupResultCache()

	// Test setting and getting a value
	cache.Set("resolve_account:abc123", map[string]any{
		"account_id": "ACC-001",
		"currency":   "GBP",
	})

	val, ok := cache.Get("resolve_account:abc123")
	require.True(t, ok, "expected cache hit")
	require.NotNil(t, val)

	result := val.(map[string]any)
	assert.Equal(t, "ACC-001", result["account_id"])
	assert.Equal(t, "GBP", result["currency"])
}

func TestLookupResultCache_GetMiss(t *testing.T) {
	cache := NewLookupResultCache()

	val, ok := cache.Get("nonexistent")
	assert.False(t, ok, "expected cache miss")
	assert.Nil(t, val)
}

func TestLookupResultCache_SetNilValue(t *testing.T) {
	cache := NewLookupResultCache()

	// Setting nil is allowed (represents "lookup returned nothing")
	cache.Set("resolve_account:empty", nil)

	val, ok := cache.Get("resolve_account:empty")
	assert.True(t, ok, "nil value should still be a cache hit")
	assert.Nil(t, val)
}

func TestGenerateCacheKey_Deterministic(t *testing.T) {
	args1 := map[string]any{
		"purpose":  "clearing",
		"currency": "GBP",
	}
	args2 := map[string]any{
		"currency": "GBP", // Different order
		"purpose":  "clearing",
	}

	key1 := GenerateCacheKey("resolve_account", args1)
	key2 := GenerateCacheKey("resolve_account", args2)

	assert.Equal(t, key1, key2, "same args with different order should produce same hash")
	assert.Contains(t, key1, "resolve_account:", "key should contain builtin name prefix")
}

func TestGenerateCacheKey_DifferentArgs(t *testing.T) {
	args1 := map[string]any{
		"purpose":  "clearing",
		"currency": "GBP",
	}
	args2 := map[string]any{
		"purpose":  "settlement",
		"currency": "GBP",
	}

	key1 := GenerateCacheKey("resolve_account", args1)
	key2 := GenerateCacheKey("resolve_account", args2)

	assert.NotEqual(t, key1, key2, "different args should produce different hashes")
}

func TestGenerateCacheKey_DifferentBuiltins(t *testing.T) {
	args := map[string]any{
		"code":    "ELEC-NZ",
		"version": "1",
	}

	key1 := GenerateCacheKey("resolve_account", args)
	key2 := GenerateCacheKey("resolve_instrument", args)

	assert.NotEqual(t, key1, key2, "different builtin names should produce different hashes")
}

func TestGenerateCacheKey_NestedMaps(t *testing.T) {
	args1 := map[string]any{
		"outer": map[string]any{
			"inner_a": "value_a",
			"inner_b": "value_b",
		},
	}
	args2 := map[string]any{
		"outer": map[string]any{
			"inner_b": "value_b", // Different order
			"inner_a": "value_a",
		},
	}

	key1 := GenerateCacheKey("test_builtin", args1)
	key2 := GenerateCacheKey("test_builtin", args2)

	assert.Equal(t, key1, key2, "nested maps with different order should produce same hash")
}

func TestGenerateCacheKey_EmptyArgs(t *testing.T) {
	key1 := GenerateCacheKey("no_args_builtin", map[string]any{})
	key2 := GenerateCacheKey("no_args_builtin", map[string]any{})

	assert.Equal(t, key1, key2, "empty args should produce consistent hash")
	assert.Contains(t, key1, "no_args_builtin:")
}

func TestLookupResultCache_Serialize(t *testing.T) {
	cache := NewLookupResultCache()

	cache.Set("resolve_account:key1", map[string]any{
		"account_id": "ACC-001",
	})
	cache.Set("resolve_instrument:key2", map[string]any{
		"instrument_id": "INST-001",
	})

	data, err := cache.Serialize()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Deserialize into a new cache
	newCache := NewLookupResultCache()
	err = newCache.Deserialize(data)
	require.NoError(t, err)

	// Verify the values are preserved
	val1, ok := newCache.Get("resolve_account:key1")
	require.True(t, ok)
	result1 := val1.(map[string]any)
	assert.Equal(t, "ACC-001", result1["account_id"])

	val2, ok := newCache.Get("resolve_instrument:key2")
	require.True(t, ok)
	result2 := val2.(map[string]any)
	assert.Equal(t, "INST-001", result2["instrument_id"])
}

func TestLookupResultCache_DeserializeEmpty(t *testing.T) {
	cache := NewLookupResultCache()

	err := cache.Deserialize([]byte("{}"))
	require.NoError(t, err)

	_, ok := cache.Get("anything")
	assert.False(t, ok)
}

func TestLookupResultCache_DeserializeInvalid(t *testing.T) {
	cache := NewLookupResultCache()

	err := cache.Deserialize([]byte("not json"))
	assert.Error(t, err)
}

func TestLookupResultCache_ThreadSafety(t *testing.T) {
	cache := NewLookupResultCache()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := GenerateCacheKey("concurrent_test", map[string]any{"n": n})
			cache.Set(key, map[string]any{"value": n})
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := GenerateCacheKey("concurrent_test", map[string]any{"n": n})
			cache.Get(key)
		}(i)
	}

	wg.Wait()

	// Verify at least some entries were stored
	data, err := cache.Serialize()
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestLookupResultCache_Clear(t *testing.T) {
	cache := NewLookupResultCache()

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")

	cache.Clear()

	_, ok := cache.Get("key1")
	assert.False(t, ok, "cache should be empty after Clear")
}

func TestLookupResultCache_Len(t *testing.T) {
	cache := NewLookupResultCache()
	assert.Equal(t, 0, cache.Len())

	cache.Set("key1", "value1")
	assert.Equal(t, 1, cache.Len())

	cache.Set("key2", "value2")
	assert.Equal(t, 2, cache.Len())

	// Setting same key doesn't increase count
	cache.Set("key1", "updated")
	assert.Equal(t, 2, cache.Len())
}

// Test for AccountResolver integration with cache

var (
	errAccountNotFound    = errors.New("account not found")
	errInstrumentNotFound = errors.New("instrument not found")
)

type mockAccountResolver struct {
	calls   int
	results map[string]map[string]any
}

func (m *mockAccountResolver) ResolveAccount(purpose, currency string) (map[string]any, error) {
	m.calls++
	key := purpose + ":" + currency
	if result, ok := m.results[key]; ok {
		return result, nil
	}
	return nil, errAccountNotFound
}

type mockInstrumentResolver struct {
	calls   int
	results map[string]map[string]any
}

func (m *mockInstrumentResolver) ResolveInstrument(code, version string) (map[string]any, error) {
	m.calls++
	key := code + ":" + version
	if result, ok := m.results[key]; ok {
		return result, nil
	}
	return nil, errInstrumentNotFound
}

func TestCachedResolveAccount_CacheMiss(t *testing.T) {
	cache := NewLookupResultCache()
	resolver := &mockAccountResolver{
		results: map[string]map[string]any{
			"clearing:GBP": {"account_id": "ACC-001", "currency": "GBP"},
		},
	}

	result, err := CachedResolveAccount(cache, resolver, "clearing", "GBP")
	require.NoError(t, err)
	assert.Equal(t, "ACC-001", result["account_id"])
	assert.Equal(t, 1, resolver.calls, "resolver should be called on cache miss")

	// Verify the result was cached
	key := GenerateCacheKey("resolve_account", map[string]any{"purpose": "clearing", "currency": "GBP"})
	cachedVal, ok := cache.Get(key)
	require.True(t, ok, "result should be cached")
	assert.Equal(t, "ACC-001", cachedVal.(map[string]any)["account_id"])
}

func TestCachedResolveAccount_CacheHit(t *testing.T) {
	cache := NewLookupResultCache()
	resolver := &mockAccountResolver{
		results: map[string]map[string]any{
			"clearing:GBP": {"account_id": "ACC-NEW", "currency": "GBP"},
		},
	}

	// Pre-populate cache with old value
	key := GenerateCacheKey("resolve_account", map[string]any{"purpose": "clearing", "currency": "GBP"})
	cache.Set(key, map[string]any{"account_id": "ACC-OLD", "currency": "GBP"})

	result, err := CachedResolveAccount(cache, resolver, "clearing", "GBP")
	require.NoError(t, err)
	assert.Equal(t, "ACC-OLD", result["account_id"], "should return cached value even if resolver would return different")
	assert.Equal(t, 0, resolver.calls, "resolver should NOT be called on cache hit")
}

func TestCachedResolveInstrument_CacheMiss(t *testing.T) {
	cache := NewLookupResultCache()
	resolver := &mockInstrumentResolver{
		results: map[string]map[string]any{
			"ELEC-NZ:1": {"instrument_id": "INST-001", "code": "ELEC-NZ"},
		},
	}

	result, err := CachedResolveInstrument(cache, resolver, "ELEC-NZ", "1")
	require.NoError(t, err)
	assert.Equal(t, "INST-001", result["instrument_id"])
	assert.Equal(t, 1, resolver.calls, "resolver should be called on cache miss")

	// Verify the result was cached
	key := GenerateCacheKey("resolve_instrument", map[string]any{"code": "ELEC-NZ", "version": "1"})
	cachedVal, ok := cache.Get(key)
	require.True(t, ok, "result should be cached")
	assert.Equal(t, "INST-001", cachedVal.(map[string]any)["instrument_id"])
}

func TestCachedResolveInstrument_CacheHit(t *testing.T) {
	cache := NewLookupResultCache()
	resolver := &mockInstrumentResolver{
		results: map[string]map[string]any{
			"ELEC-NZ:1": {"instrument_id": "INST-NEW", "code": "ELEC-NZ"},
		},
	}

	// Pre-populate cache with old value
	key := GenerateCacheKey("resolve_instrument", map[string]any{"code": "ELEC-NZ", "version": "1"})
	cache.Set(key, map[string]any{"instrument_id": "INST-OLD", "code": "ELEC-NZ"})

	result, err := CachedResolveInstrument(cache, resolver, "ELEC-NZ", "1")
	require.NoError(t, err)
	assert.Equal(t, "INST-OLD", result["instrument_id"], "should return cached value")
	assert.Equal(t, 0, resolver.calls, "resolver should NOT be called on cache hit")
}

func TestCachedResolveAccount_ResolverError(t *testing.T) {
	cache := NewLookupResultCache()
	resolver := &mockAccountResolver{
		results: map[string]map[string]any{}, // No results = error
	}

	_, err := CachedResolveAccount(cache, resolver, "unknown", "XYZ")
	assert.Error(t, err)
	assert.Equal(t, 1, resolver.calls)

	// Error results should NOT be cached
	key := GenerateCacheKey("resolve_account", map[string]any{"purpose": "unknown", "currency": "XYZ"})
	_, ok := cache.Get(key)
	assert.False(t, ok, "error results should not be cached")
}

// Tests for InputSnapshot integration

func TestInputSnapshot_WithLookupResults(t *testing.T) {
	// Create input with lookup_results field
	input := map[string]any{
		"amount":   "100.00",
		"currency": "GBP",
		"lookup_results": map[string]any{
			"resolve_account:abc123": map[string]any{
				"account_id": "ACC-001",
			},
		},
	}

	cache, err := ExtractLookupCache(input)
	require.NoError(t, err)
	require.NotNil(t, cache)

	// The cache should contain the lookup results
	val, ok := cache.Get("resolve_account:abc123")
	require.True(t, ok)
	assert.Equal(t, "ACC-001", val.(map[string]any)["account_id"])
}

func TestInputSnapshot_WithoutLookupResults(t *testing.T) {
	// Input without lookup_results field
	input := map[string]any{
		"amount":   "100.00",
		"currency": "GBP",
	}

	cache, err := ExtractLookupCache(input)
	require.NoError(t, err)
	require.NotNil(t, cache)
	assert.Equal(t, 0, cache.Len(), "cache should be empty when no lookup_results in input")
}

func TestInputSnapshot_NilInput(t *testing.T) {
	cache, err := ExtractLookupCache(nil)
	require.NoError(t, err)
	require.NotNil(t, cache)
	assert.Equal(t, 0, cache.Len())
}

func TestMergeLookupCacheToInput(t *testing.T) {
	cache := NewLookupResultCache()
	cache.Set("resolve_account:key1", map[string]any{"account_id": "ACC-001"})
	cache.Set("resolve_instrument:key2", map[string]any{"instrument_id": "INST-001"})

	input := map[string]any{
		"amount":   "100.00",
		"currency": "GBP",
	}

	result, err := MergeLookupCacheToInput(input, cache)
	require.NoError(t, err)

	// Original fields should be preserved
	assert.Equal(t, "100.00", result["amount"])
	assert.Equal(t, "GBP", result["currency"])

	// lookup_results should be added
	lookupResults, ok := result["lookup_results"].(map[string]any)
	require.True(t, ok, "lookup_results should be present")

	assert.Equal(t, "ACC-001", lookupResults["resolve_account:key1"].(map[string]any)["account_id"])
	assert.Equal(t, "INST-001", lookupResults["resolve_instrument:key2"].(map[string]any)["instrument_id"])
}

func TestMergeLookupCacheToInput_NilInput(t *testing.T) {
	cache := NewLookupResultCache()
	cache.Set("key1", "value1")

	result, err := MergeLookupCacheToInput(nil, cache)
	require.NoError(t, err)

	lookupResults, ok := result["lookup_results"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "value1", lookupResults["key1"])
}

func TestMergeLookupCacheToInput_EmptyCache(t *testing.T) {
	cache := NewLookupResultCache()
	input := map[string]any{"foo": "bar"}

	result, err := MergeLookupCacheToInput(input, cache)
	require.NoError(t, err)

	// With empty cache, lookup_results should still be present but empty
	lookupResults, ok := result["lookup_results"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, lookupResults)
}

func TestRoundTrip_ExtractAndMerge(t *testing.T) {
	// Start with a cache
	originalCache := NewLookupResultCache()
	originalCache.Set("resolve_account:key1", map[string]any{"account_id": "ACC-001"})

	// Merge into input
	input, err := MergeLookupCacheToInput(map[string]any{"data": "test"}, originalCache)
	require.NoError(t, err)

	// Extract cache from input
	extractedCache, err := ExtractLookupCache(input)
	require.NoError(t, err)

	// The extracted cache should contain the same data
	val, ok := extractedCache.Get("resolve_account:key1")
	require.True(t, ok)
	assert.Equal(t, "ACC-001", val.(map[string]any)["account_id"])
}
