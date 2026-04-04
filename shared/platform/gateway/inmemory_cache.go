// Package gateway provides HTTP middleware for API gateway functionality.
package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// DefaultCacheTTL is the default time-to-live for cache entries.
const DefaultCacheTTL = 5 * time.Minute

// DefaultCleanupInterval is the default interval for background cleanup of expired entries.
const DefaultCleanupInterval = 1 * time.Minute

// cacheEntry holds a cached tenant ID and status with its expiration time.
type cacheEntry struct {
	tenantID  tenant.TenantID
	status    string
	expiresAt time.Time
}

// isExpired returns true if the cache entry has expired.
func (e cacheEntry) isExpired() bool {
	return time.Now().After(e.expiresAt)
}

// InMemorySlugCache is a thread-safe in-memory cache for slug-to-tenant-ID mappings.
// It implements the slugCache interface and provides TTL-based expiration with
// automatic background cleanup of expired entries.
//
// This cache is intended for local development without Redis dependency.
// For production use, consider a distributed cache like Redis.
type InMemorySlugCache struct {
	mu              sync.RWMutex
	cache           map[string]cacheEntry
	ttl             time.Duration
	cleanupInterval time.Duration
	stopCleanup     chan struct{}
	cleanupDone     chan struct{}
	stopOnce        sync.Once
}

// InMemoryCacheOption is a functional option for configuring InMemorySlugCache.
type InMemoryCacheOption func(*InMemorySlugCache)

// WithTTL sets the time-to-live for cache entries.
func WithTTL(ttl time.Duration) InMemoryCacheOption {
	return func(c *InMemorySlugCache) {
		if ttl > 0 {
			c.ttl = ttl
		}
	}
}

// WithCleanupInterval sets the interval for background cleanup of expired entries.
func WithCleanupInterval(interval time.Duration) InMemoryCacheOption {
	return func(c *InMemorySlugCache) {
		if interval > 0 {
			c.cleanupInterval = interval
		}
	}
}

// NewInMemorySlugCache creates a new in-memory slug cache with the given options.
// It starts a background goroutine to periodically clean up expired entries.
// Call Stop() to release resources when the cache is no longer needed.
func NewInMemorySlugCache(opts ...InMemoryCacheOption) *InMemorySlugCache {
	c := &InMemorySlugCache{
		cache:           make(map[string]cacheEntry),
		ttl:             DefaultCacheTTL,
		cleanupInterval: DefaultCleanupInterval,
		stopCleanup:     make(chan struct{}),
		cleanupDone:     make(chan struct{}),
	}

	for _, opt := range opts {
		opt(c)
	}

	// Start background cleanup goroutine
	go c.cleanupLoop()

	return c
}

// Get retrieves a tenant ID and status for the given slug from the cache.
// Returns an empty TenantID and empty status for cache miss (not an error).
// Context cancellation is respected.
func (c *InMemorySlugCache) Get(ctx context.Context, slug string) (tenant.TenantID, string, error) {
	// Check context first
	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	default:
	}

	if slug == "" {
		return "", "", nil
	}

	c.mu.RLock()
	entry, ok := c.cache[slug]
	c.mu.RUnlock()

	if !ok {
		return "", "", nil
	}

	// Check if entry has expired
	if entry.isExpired() {
		// Entry expired - treat as cache miss
		// Cleanup goroutine will eventually remove it
		return "", "", nil
	}

	return entry.tenantID, entry.status, nil
}

// Set stores a tenant ID and status for the given slug in the cache.
// The entry will expire after the configured TTL.
// Context cancellation is respected.
func (c *InMemorySlugCache) Set(ctx context.Context, slug string, tenantID tenant.TenantID, status string) error {
	// Check context first
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if slug == "" {
		return nil
	}

	c.mu.Lock()
	c.cache[slug] = cacheEntry{
		tenantID:  tenantID,
		status:    status,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return nil
}

// Invalidate removes a slug entry from the cache, forcing the next lookup
// to query the database. Used when provisioning transitions occur.
func (c *InMemorySlugCache) Invalidate(_ context.Context, slug string) {
	if slug == "" {
		return
	}

	c.mu.Lock()
	delete(c.cache, slug)
	c.mu.Unlock()
}

// Stop stops the background cleanup goroutine and releases resources.
// It blocks until the cleanup goroutine has exited.
// Safe to call multiple times.
func (c *InMemorySlugCache) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCleanup)
	})
	// Wait for cleanup goroutine to finish
	<-c.cleanupDone
}

// cleanupLoop runs in the background and periodically removes expired entries.
func (c *InMemorySlugCache) cleanupLoop() {
	defer close(c.cleanupDone)

	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCleanup:
			return
		case <-ticker.C:
			c.removeExpiredEntries()
		}
	}
}

// removeExpiredEntries removes all expired entries from the cache.
func (c *InMemorySlugCache) removeExpiredEntries() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for slug, entry := range c.cache {
		if now.After(entry.expiresAt) {
			delete(c.cache, slug)
		}
	}
}

// Size returns the number of entries in the cache (for testing).
func (c *InMemorySlugCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// Compile-time interface check.
var _ slugCache = (*InMemorySlugCache)(nil)
