package validation

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// Cache provides an in-memory cache for validation results.
// It uses SHA256 hashing of script content as the cache key, LRU eviction
// when the maximum size is reached, and TTL-based expiration.
//
// The cache is thread-safe and can be used concurrently from multiple goroutines.
type Cache struct {
	cache   map[string]*cacheEntry
	lru     *list.List // Doubly-linked list for LRU tracking
	mu      sync.RWMutex
	ttl     time.Duration
	maxSize int
}

// cacheEntry represents a single cached validation result.
type cacheEntry struct {
	Result     *ValidationResult
	CachedAt   time.Time
	ScriptHash string
	element    *list.Element // Pointer to LRU list element for O(1) access
}

// NewCache creates a new validation cache.
//
// Parameters:
//   - ttl: Time-to-live for cache entries. Use 0 for no expiration.
//   - maxSize: Maximum number of entries. Use 0 for unlimited (not recommended).
//
// Default recommended values: ttl=1h, maxSize=1000
func NewCache(ttl time.Duration, maxSize int) *Cache {
	return &Cache{
		cache:   make(map[string]*cacheEntry),
		lru:     list.New(),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// computeHash generates a SHA256 hash of the script content.
func computeHash(script string) string {
	hash := sha256.Sum256([]byte(script))
	return hex.EncodeToString(hash[:])
}

// Get retrieves a cached validation result for the given script.
// Returns (result, true) on cache hit, (nil, false) on cache miss or expiration.
//
// On cache hit, the entry is moved to the front of the LRU list.
// Emits Prometheus metrics for cache hits and misses.
func (c *Cache) Get(script string) (*ValidationResult, bool) {
	hashStr := computeHash(script)

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.cache[hashStr]
	if !ok {
		RecordCacheMiss()
		return nil, false
	}

	// Check TTL expiration (skip if ttl is 0)
	if c.ttl > 0 && time.Since(entry.CachedAt) > c.ttl {
		// Entry expired - remove it
		c.removeEntryWithEviction(hashStr, entry, "ttl")
		RecordCacheMiss()
		return nil, false
	}

	// Move to front of LRU list (most recently used)
	c.lru.MoveToFront(entry.element)

	RecordCacheHit()
	return entry.Result, true
}

// Set stores a validation result in the cache.
// If the cache is at capacity, the least recently used entry is evicted.
// Updates Prometheus cache size gauge after modification.
func (c *Cache) Set(script string, result *ValidationResult) {
	hashStr := computeHash(script)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if entry already exists
	if existing, ok := c.cache[hashStr]; ok {
		// Update existing entry
		existing.Result = result
		existing.CachedAt = time.Now()
		c.lru.MoveToFront(existing.element)
		return
	}

	// Evict LRU entry if at capacity
	if c.maxSize > 0 && len(c.cache) >= c.maxSize {
		c.evictOldest()
	}

	// Create new entry
	entry := &cacheEntry{
		Result:     result,
		CachedAt:   time.Now(),
		ScriptHash: hashStr,
	}

	// Add to front of LRU list
	entry.element = c.lru.PushFront(hashStr)
	c.cache[hashStr] = entry

	RecordCacheSize(len(c.cache))
}

// evictOldest removes the least recently used entry.
// Must be called with lock held.
func (c *Cache) evictOldest() {
	oldest := c.lru.Back()
	if oldest == nil {
		return
	}

	hashStr, _ := oldest.Value.(string)
	delete(c.cache, hashStr)
	c.lru.Remove(oldest)
	RecordCacheEviction("lru")
}

// removeEntry removes a specific entry from the cache without recording eviction metrics.
// Must be called with lock held. Use removeEntryWithEviction when evicting for metrics.
func (c *Cache) removeEntry(hashStr string, entry *cacheEntry) {
	delete(c.cache, hashStr)
	if entry.element != nil {
		c.lru.Remove(entry.element)
	}
}

// removeEntryWithEviction removes a specific entry and records the eviction metric.
// Must be called with lock held.
func (c *Cache) removeEntryWithEviction(hashStr string, entry *cacheEntry, reason string) {
	c.removeEntry(hashStr, entry)
	RecordCacheEviction(reason)
}

// EvictExpired removes all expired entries from the cache.
// This is called periodically by the background eviction goroutine.
// Records TTL eviction metrics for each removed entry.
func (c *Cache) EvictExpired() {
	if c.ttl == 0 {
		return // No expiration configured
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for hashStr, entry := range c.cache {
		if now.Sub(entry.CachedAt) > c.ttl {
			c.removeEntryWithEviction(hashStr, entry, "ttl")
		}
	}
	RecordCacheSize(len(c.cache))
}

// Start begins background eviction of expired entries.
// The eviction goroutine runs every 10 minutes until the context is cancelled.
func (c *Cache) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.EvictExpired()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Size returns the current number of entries in the cache.
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// Clear removes all entries from the cache.
// Updates Prometheus cache size gauge to 0.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*cacheEntry)
	c.lru.Init()
	RecordCacheSize(0)
}
