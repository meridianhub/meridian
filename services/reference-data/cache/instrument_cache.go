// Package cache provides caching infrastructure for instrument definitions
// with tenant isolation and TTL-based expiration.
package cache

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Default cache configuration values.
const (
	// DefaultCacheSize is the maximum number of entries per tenant cache.
	DefaultCacheSize = 10000

	// DefaultTTL is the base TTL for cached entries.
	DefaultTTL = 5 * time.Minute

	// DefaultTTLJitter is the maximum random variation added to TTL.
	// This prevents thundering herd when many entries expire simultaneously.
	DefaultTTLJitter = 30 * time.Second
)

// Key uniquely identifies an instrument within a tenant's cache.
// Tenant isolation is handled externally via context - each tenant has
// its own LRU cache instance.
type Key struct {
	Code    string
	Version int
}

// CachedInstrument contains the instrument definition and precompiled CEL programs.
type CachedInstrument struct {
	// Definition is the cached instrument definition.
	Definition *registry.InstrumentDefinition

	// ValidationProgram is the precompiled CEL program for validation.
	// May be nil if no validation expression is defined.
	ValidationProgram cel.Program

	// BucketKeyProgram is the precompiled CEL program for bucket key generation.
	// May be nil if no bucket key expression is defined.
	BucketKeyProgram cel.Program

	// cachedAt records when this entry was added to the cache.
	cachedAt time.Time

	// expiresAt is the precomputed expiration time (cachedAt + jitteredTTL).
	// Computed once at cache time to ensure consistent expiration checks.
	expiresAt time.Time
}

// InstrumentCache provides tenant-isolated caching for instrument definitions.
// Each tenant has its own LRU cache to ensure complete isolation.
//
// Thread-safety: All methods are safe for concurrent use.
type InstrumentCache struct {
	// mu protects the tenantCaches map
	mu sync.RWMutex

	// tenantCaches maps tenant IDs to their individual LRU caches
	tenantCaches map[tenant.TenantID]*lru.Cache[Key, *CachedInstrument]

	// cacheSize is the maximum entries per tenant cache
	cacheSize int

	// baseTTL is the base time-to-live for cache entries
	baseTTL time.Duration

	// ttlJitter is the maximum random variation added to TTL
	ttlJitter time.Duration
}

// Option configures an InstrumentCache.
type Option func(*InstrumentCache)

// WithCacheSize sets the maximum number of entries per tenant cache.
func WithCacheSize(size int) Option {
	return func(c *InstrumentCache) {
		c.cacheSize = size
	}
}

// WithTTL sets the base TTL and jitter for cache entries.
func WithTTL(baseTTL, jitter time.Duration) Option {
	return func(c *InstrumentCache) {
		c.baseTTL = baseTTL
		c.ttlJitter = jitter
	}
}

// NewInstrumentCache creates a new tenant-isolated instrument cache.
func NewInstrumentCache(opts ...Option) *InstrumentCache {
	c := &InstrumentCache{
		tenantCaches: make(map[tenant.TenantID]*lru.Cache[Key, *CachedInstrument]),
		cacheSize:    DefaultCacheSize,
		baseTTL:      DefaultTTL,
		ttlJitter:    DefaultTTLJitter,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Get retrieves a cached instrument for the tenant in context.
// Returns nil if the entry is not found, expired, or tenant context is missing.
func (c *InstrumentCache) Get(ctx context.Context, code string, version int) *CachedInstrument {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	cache := c.getTenantCache(tenantID)
	if cache == nil {
		return nil
	}

	key := Key{Code: code, Version: version}
	entry, ok := cache.Get(key)
	if !ok {
		return nil
	}

	// Check if entry has expired
	if time.Now().After(entry.expiresAt) {
		// Remove expired entry
		cache.Remove(key)
		return nil
	}

	return entry
}

// Put stores an instrument in the cache for the tenant in context.
// Does nothing if tenant context is missing.
func (c *InstrumentCache) Put(ctx context.Context, code string, version int, instrument *CachedInstrument) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	cache := c.getOrCreateTenantCache(tenantID)
	if cache == nil {
		return
	}

	key := Key{Code: code, Version: version}

	// Set cache timestamps
	now := time.Now()
	instrument.cachedAt = now
	instrument.expiresAt = now.Add(c.jitteredTTL())

	cache.Add(key, instrument)
}

// Invalidate removes a specific entry from the cache for the tenant in context.
func (c *InstrumentCache) Invalidate(ctx context.Context, code string, version int) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	cache := c.getTenantCache(tenantID)
	if cache == nil {
		return
	}

	key := Key{Code: code, Version: version}
	cache.Remove(key)
}

// InvalidateAll removes all entries for the tenant in context.
func (c *InstrumentCache) InvalidateAll(ctx context.Context) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.tenantCaches, tenantID)
}

// Stats returns cache statistics for the tenant in context.
// Returns (0, 0) if tenant context is missing or cache doesn't exist.
func (c *InstrumentCache) Stats(ctx context.Context) (size int, capacity int) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return 0, 0
	}

	cache := c.getTenantCache(tenantID)
	if cache == nil {
		return 0, 0
	}

	return cache.Len(), c.cacheSize
}

// getTenantCache returns the cache for a tenant, or nil if not found.
func (c *InstrumentCache) getTenantCache(tenantID tenant.TenantID) *lru.Cache[Key, *CachedInstrument] {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tenantCaches[tenantID]
}

// getOrCreateTenantCache returns the cache for a tenant, creating it if needed.
func (c *InstrumentCache) getOrCreateTenantCache(tenantID tenant.TenantID) *lru.Cache[Key, *CachedInstrument] {
	// Fast path: check if already exists
	c.mu.RLock()
	cache, ok := c.tenantCaches[tenantID]
	c.mu.RUnlock()

	if ok {
		return cache
	}

	// Slow path: create new cache
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if cache, ok = c.tenantCaches[tenantID]; ok {
		return cache
	}

	// Create new LRU cache for this tenant
	newCache, err := lru.New[Key, *CachedInstrument](c.cacheSize)
	if err != nil {
		// This should never happen with valid cache size
		return nil
	}

	c.tenantCaches[tenantID] = newCache
	return newCache
}

// jitteredTTL returns the base TTL plus a random jitter.
// The jitter helps prevent thundering herd when many entries expire.
func (c *InstrumentCache) jitteredTTL() time.Duration {
	if c.ttlJitter == 0 {
		return c.baseTTL
	}

	// Generate random jitter in range [-ttlJitter, +ttlJitter]
	jitterRange := int64(c.ttlJitter) * 2
	jitter := rand.Int64N(jitterRange) - int64(c.ttlJitter)

	return c.baseTTL + time.Duration(jitter)
}

// CachedAt returns when this entry was cached.
func (ci *CachedInstrument) CachedAt() time.Time {
	return ci.cachedAt
}

// ExpiresAt returns when this entry will expire.
func (ci *CachedInstrument) ExpiresAt() time.Time {
	return ci.expiresAt
}
