package cache

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// Default configuration for the saga binding cache.
const (
	DefaultSagaBindingTTL = 5 * time.Minute
)

// SagaBindingSource provides the source of truth for saga bindings.
// Implementations query the database or manifest store to resolve
// which saga handles each API path for a given tenant.
type SagaBindingSource interface {
	// GetBindingsForTenant returns a map of api_path -> saga_name
	// for all sagas with "api:" triggers configured for the given tenant.
	GetBindingsForTenant(ctx context.Context, tenantID string) (map[string]string, error)
}

// SagaBindingCache provides an in-memory cache mapping (tenant_id, api_path) to saga_name.
// It supports TTL-based expiry, explicit invalidation (e.g., on manifest apply),
// and automatic refresh from a SagaBindingSource on cache miss.
//
// Thread-safety: All methods are safe for concurrent use.
// Invalidation safety: A per-tenant generation counter prevents in-flight refreshes
// from overwriting entries that were invalidated after the refresh started.
type SagaBindingCache struct {
	mu      sync.RWMutex
	entries map[string]*tenantBindings // tenantID -> bindings
	gens    sync.Map                   // tenantID -> *atomic.Uint64 (invalidation generation)
	source  SagaBindingSource
	sfGroup singleflight.Group
	ttl     time.Duration
	logger  *slog.Logger
}

// tenantBindings holds cached saga bindings for a single tenant.
type tenantBindings struct {
	pathToSaga map[string]string
	expiresAt  time.Time
}

// SagaBindingOption configures a SagaBindingCache.
type SagaBindingOption func(*SagaBindingCache)

// WithSagaBindingTTL sets the TTL for cached saga bindings.
func WithSagaBindingTTL(ttl time.Duration) SagaBindingOption {
	return func(c *SagaBindingCache) {
		if ttl > 0 {
			c.ttl = ttl
		}
	}
}

// WithSagaBindingLogger sets the logger for the cache.
func WithSagaBindingLogger(logger *slog.Logger) SagaBindingOption {
	return func(c *SagaBindingCache) {
		c.logger = logger
	}
}

// NewSagaBindingCache creates a new SagaBindingCache.
// Panics if source is nil.
func NewSagaBindingCache(source SagaBindingSource, opts ...SagaBindingOption) *SagaBindingCache {
	if source == nil {
		panic("SagaBindingSource cannot be nil")
	}
	c := &SagaBindingCache{
		entries: make(map[string]*tenantBindings),
		source:  source,
		ttl:     DefaultSagaBindingTTL,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// tenantGeneration returns the generation counter for a tenant,
// creating one atomically if it doesn't exist. Safe for concurrent use
// without external locking (uses sync.Map internally).
func (c *SagaBindingCache) tenantGeneration(tenantID string) *atomic.Uint64 {
	if v, ok := c.gens.Load(tenantID); ok {
		if g, ok := v.(*atomic.Uint64); ok {
			return g
		}
	}
	actual, _ := c.gens.LoadOrStore(tenantID, &atomic.Uint64{})
	g, _ := actual.(*atomic.Uint64)
	return g
}

// Get returns the saga name bound to the given API path for the specified tenant.
// On cache miss or TTL expiry, it refreshes from the source.
// Returns an error if the refresh fails and no cached data is available.
func (c *SagaBindingCache) Get(ctx context.Context, tenantID, path string) (sagaName string, found bool, err error) {
	// Fast path: check cache under read lock
	c.mu.RLock()
	if entry, ok := c.entries[tenantID]; ok && time.Now().Before(entry.expiresAt) {
		sagaName, found = entry.pathToSaga[path]
		c.mu.RUnlock()
		return sagaName, found, nil
	}
	c.mu.RUnlock()

	// Cache miss or expired: refresh from source (deduplicated via singleflight)
	if _, refreshErr := c.doRefresh(ctx, tenantID); refreshErr != nil {
		c.logger.Warn("saga binding cache refresh failed",
			"tenant_id", tenantID,
			"error", refreshErr,
		)
		return "", false, fmt.Errorf("refresh saga bindings for tenant %s: %w", tenantID, refreshErr)
	}

	// Re-check cache after refresh
	c.mu.RLock()
	defer c.mu.RUnlock()

	if entry, ok := c.entries[tenantID]; ok {
		sagaName, found = entry.pathToSaga[path]
		return sagaName, found, nil
	}

	return "", false, nil
}

// Invalidate clears cached bindings for the specified tenant.
// The next Get call for this tenant will trigger a refresh from the source.
// Uses a generation counter to prevent in-flight refreshes from overwriting
// the invalidation with stale data.
func (c *SagaBindingCache) Invalidate(_ context.Context, tenantID string) error {
	c.mu.Lock()
	// Bump generation so any in-flight refresh for this tenant is fenced out
	c.tenantGeneration(tenantID).Add(1)
	delete(c.entries, tenantID)
	c.mu.Unlock()
	return nil
}

// Refresh forces a reload of bindings for the specified tenant from the source.
// It invalidates the cache entry first to ensure a fresh load regardless of TTL.
// Bumps the generation counter to fence out any concurrent in-flight refreshes.
func (c *SagaBindingCache) Refresh(ctx context.Context, tenantID string) error {
	// Bump generation and clear entry so doRefresh fetches fresh data
	c.mu.Lock()
	c.tenantGeneration(tenantID).Add(1)
	delete(c.entries, tenantID)
	c.mu.Unlock()

	_, err := c.doRefresh(ctx, tenantID)
	return err
}

// doRefresh loads bindings from the source and populates the cache.
// Uses singleflight to deduplicate concurrent refreshes for the same tenant.
// Uses a generation counter to prevent stale writes after invalidation.
// The singleflight key includes the generation so that post-invalidation callers
// start a new flight instead of joining an obsolete one.
func (c *SagaBindingCache) doRefresh(ctx context.Context, tenantID string) (map[string]string, error) {
	gen := c.tenantGeneration(tenantID).Load()
	sfKey := tenantID + ":" + strconv.FormatUint(gen, 10)

	result, err, _ := c.sfGroup.Do(sfKey, func() (interface{}, error) {
		// Re-check cache inside singleflight to avoid redundant loads
		c.mu.RLock()
		if entry, ok := c.entries[tenantID]; ok && time.Now().Before(entry.expiresAt) {
			c.mu.RUnlock()
			return entry.pathToSaga, nil
		}
		c.mu.RUnlock()

		bindings, err := c.source.GetBindingsForTenant(ctx, tenantID)
		if err != nil {
			return nil, err
		}

		// Only write if generation hasn't changed (no invalidation happened during load)
		c.mu.Lock()
		currentGen := c.tenantGeneration(tenantID).Load()
		if gen == currentGen {
			c.entries[tenantID] = &tenantBindings{
				pathToSaga: bindings,
				expiresAt:  time.Now().Add(c.ttl),
			}
		}
		c.mu.Unlock()

		return bindings, nil
	})

	if err != nil {
		return nil, err
	}

	bindings, _ := result.(map[string]string)
	return bindings, nil
}
