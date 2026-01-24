// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// LookupResultCache provides thread-safe caching of external lookup results
// for deterministic saga replay. When a saga is replayed, builtins like
// resolve_account and resolve_instrument check this cache first to ensure
// the same values are returned even if Reference Data has changed.
//
// Per PRD FR-34: Capture external lookup results for replay safety.
type LookupResultCache struct {
	mu    sync.RWMutex
	cache map[string]any
}

// NewLookupResultCache creates a new empty lookup result cache.
func NewLookupResultCache() *LookupResultCache {
	return &LookupResultCache{
		cache: make(map[string]any),
	}
}

// Get retrieves a cached lookup result by key.
// Returns the value and true if found, or nil and false if not cached.
func (c *LookupResultCache) Get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.cache[key]
	return val, ok
}

// Set stores a lookup result in the cache.
// The key should be generated using GenerateCacheKey for consistency.
func (c *LookupResultCache) Set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = value
}

// Serialize converts the cache contents to JSON for persistence.
// The serialized form is stored in saga_execution_log.input_snapshot.lookup_results.
func (c *LookupResultCache) Serialize() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return json.Marshal(c.cache)
}

// Deserialize populates the cache from JSON data.
// Used when replaying a saga to restore the original lookup results.
func (c *LookupResultCache) Deserialize(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	newCache := make(map[string]any)
	if err := json.Unmarshal(data, &newCache); err != nil {
		return fmt.Errorf("failed to deserialize lookup cache: %w", err)
	}
	c.cache = newCache
	return nil
}

// Clear removes all entries from the cache.
func (c *LookupResultCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]any)
}

// Len returns the number of entries in the cache.
func (c *LookupResultCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// GenerateCacheKey creates a deterministic cache key for a builtin call.
// The key format is "builtin_name:sha256_hash" where the hash is computed
// from the ordered JSON serialization of the arguments.
//
// This ensures:
// - Same arguments in different order produce the same key
// - Different builtins with same args produce different keys
// - Nested structures are handled correctly
func GenerateCacheKey(builtinName string, args map[string]any) string {
	orderedJSON := orderedMarshal(args)
	hash := sha256.Sum256(orderedJSON)
	return fmt.Sprintf("%s:%s", builtinName, hex.EncodeToString(hash[:]))
}

// orderedMarshal produces a deterministic JSON representation of a map.
// Keys are sorted alphabetically at all nesting levels.
func orderedMarshal(v any) []byte {
	if v == nil {
		return []byte("null")
	}

	switch val := v.(type) {
	case map[string]any:
		return orderedMarshalMap(val)
	case []any:
		return orderedMarshalSlice(val)
	default:
		// For primitive types, standard JSON marshal is deterministic
		b, _ := json.Marshal(val)
		return b
	}
}

func orderedMarshalMap(m map[string]any) []byte {
	if len(m) == 0 {
		return []byte("{}")
	}

	// Sort keys
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build JSON manually to preserve key order
	result := []byte("{")
	for i, k := range keys {
		if i > 0 {
			result = append(result, ',')
		}
		keyJSON, _ := json.Marshal(k)
		result = append(result, keyJSON...)
		result = append(result, ':')
		result = append(result, orderedMarshal(m[k])...)
	}
	result = append(result, '}')
	return result
}

func orderedMarshalSlice(s []any) []byte {
	if len(s) == 0 {
		return []byte("[]")
	}

	result := []byte("[")
	for i, v := range s {
		if i > 0 {
			result = append(result, ',')
		}
		result = append(result, orderedMarshal(v)...)
	}
	result = append(result, ']')
	return result
}

// AccountResolver defines the interface for resolving account IDs from Reference Data.
// Implementations query the reference_data.accounts table or equivalent.
type AccountResolver interface {
	ResolveAccount(purpose, currency string) (map[string]any, error)
}

// InstrumentResolver defines the interface for resolving instrument IDs from Reference Data.
// Implementations query the reference_data.instruments table or equivalent.
type InstrumentResolver interface {
	ResolveInstrument(code, version string) (map[string]any, error)
}

// CachedResolveAccount resolves an account with cache-first lookup for replay safety.
// On cache miss, calls the resolver and stores the result for future replays.
// On cache hit, returns the cached value without calling the resolver.
//
// This ensures deterministic replay: even if Reference Data changes between
// the original execution and a replay, the same account ID is returned.
func CachedResolveAccount(cache *LookupResultCache, resolver AccountResolver, purpose, currency string) (map[string]any, error) {
	args := map[string]any{
		"purpose":  purpose,
		"currency": currency,
	}
	key := GenerateCacheKey("resolve_account", args)

	// Check cache first
	if cached, ok := cache.Get(key); ok {
		// Cache hit - return cached value for deterministic replay
		result, _ := cached.(map[string]any)
		return result, nil
	}

	// Cache miss - call resolver
	result, err := resolver.ResolveAccount(purpose, currency)
	if err != nil {
		// Do NOT cache errors - they should be retried on replay
		return nil, err
	}

	// Store successful result in cache
	cache.Set(key, result)
	return result, nil
}

// CachedResolveInstrument resolves an instrument with cache-first lookup for replay safety.
// On cache miss, calls the resolver and stores the result for future replays.
// On cache hit, returns the cached value without calling the resolver.
func CachedResolveInstrument(cache *LookupResultCache, resolver InstrumentResolver, code, version string) (map[string]any, error) {
	args := map[string]any{
		"code":    code,
		"version": version,
	}
	key := GenerateCacheKey("resolve_instrument", args)

	// Check cache first
	if cached, ok := cache.Get(key); ok {
		// Cache hit - return cached value for deterministic replay
		result, _ := cached.(map[string]any)
		return result, nil
	}

	// Cache miss - call resolver
	result, err := resolver.ResolveInstrument(code, version)
	if err != nil {
		// Do NOT cache errors - they should be retried on replay
		return nil, err
	}

	// Store successful result in cache
	cache.Set(key, result)
	return result, nil
}

// LookupResultsKey is the field name used to store lookup results in the input snapshot.
const LookupResultsKey = "lookup_results"

// ExtractLookupCache extracts a LookupResultCache from an input snapshot.
// If the input contains a "lookup_results" field, it is used to pre-populate
// the cache for deterministic replay. Otherwise, an empty cache is returned.
//
// This is called at the start of saga execution to restore lookup results
// from a previous execution (replay scenario).
func ExtractLookupCache(input map[string]any) (*LookupResultCache, error) {
	cache := NewLookupResultCache()

	if input == nil {
		return cache, nil
	}

	lookupResults, ok := input[LookupResultsKey]
	if !ok {
		return cache, nil
	}

	// Convert the lookup results to JSON and deserialize into the cache
	lookupMap, ok := lookupResults.(map[string]any)
	if !ok {
		return cache, nil
	}

	// Directly populate the cache from the map
	for key, value := range lookupMap {
		cache.Set(key, value)
	}

	return cache, nil
}

// MergeLookupCacheToInput merges the lookup cache contents into the input snapshot.
// This creates a new map containing the original input plus a "lookup_results" field
// with all cached lookup results.
//
// This is called after saga completion to persist lookup results for future replays.
func MergeLookupCacheToInput(input map[string]any, cache *LookupResultCache) (map[string]any, error) {
	// Create a new map to avoid mutating the original
	result := make(map[string]any)

	// Copy original input fields
	for k, v := range input {
		result[k] = v
	}

	// Serialize and deserialize to get a clean map
	cacheData, err := cache.Serialize()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize lookup cache: %w", err)
	}

	var lookupResults map[string]any
	if err := json.Unmarshal(cacheData, &lookupResults); err != nil {
		return nil, fmt.Errorf("failed to unmarshal lookup cache: %w", err)
	}

	result[LookupResultsKey] = lookupResults
	return result, nil
}
