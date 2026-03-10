package generator

import (
	"io/fs"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// defaultRefreshInterval is the default time before cached static components are refreshed.
const defaultRefreshInterval = 5 * time.Minute

// CachedContextAssembler wraps AssembleContext with a caching layer for the static
// components (handler reference card, topic list, schema summary) that are expensive
// to compute but rarely change. Pattern matching remains uncached because it varies
// per request based on the business description and industry.
type CachedContextAssembler struct {
	mu              sync.RWMutex
	handlerCard     string
	topicList       string
	schemaSummary   string
	lastRefresh     time.Time
	refreshInterval time.Duration

	registry   *schema.Registry
	cookbookFS fs.FS
}

// NewCachedContextAssembler creates a CachedContextAssembler with the given dependencies
// and refresh interval. If refreshInterval is zero, defaultRefreshInterval (5 minutes) is used.
func NewCachedContextAssembler(registry *schema.Registry, cookbookFS fs.FS, refreshInterval time.Duration) *CachedContextAssembler {
	if refreshInterval <= 0 {
		refreshInterval = defaultRefreshInterval
	}
	return &CachedContextAssembler{
		registry:        registry,
		cookbookFS:      cookbookFS,
		refreshInterval: refreshInterval,
	}
}

// AssembleContext assembles a generation prompt using cached static components and
// fresh per-request pattern matching. It is a drop-in replacement for the package-level
// AssembleContext function and returns the same types.
func (c *CachedContextAssembler) AssembleContext(opts ContextAssemblerOptions) (*AssembledContext, error) {
	handlerCard, topicList, schemaSummary := c.cachedStatics()
	return assembleContextWithStatics(opts, handlerCard, topicList, schemaSummary, c.registry, c.cookbookFS)
}

// Invalidate clears the cached static components, forcing a refresh on the next call.
// Useful in tests or when the registry changes at runtime.
func (c *CachedContextAssembler) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastRefresh = time.Time{}
}

// cachedStatics returns the three static prompt components, refreshing them if the cache
// is stale or has not been populated yet.
func (c *CachedContextAssembler) cachedStatics() (handlerCard, topicList, schemaSummary string) {
	// Fast path: read lock — cache is fresh.
	c.mu.RLock()
	if !c.lastRefresh.IsZero() && time.Since(c.lastRefresh) <= c.refreshInterval {
		handlerCard, topicList, schemaSummary = c.handlerCard, c.topicList, c.schemaSummary
		c.mu.RUnlock()
		return
	}
	c.mu.RUnlock()

	// Slow path: write lock — refresh the cache.
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock to avoid redundant work.
	if !c.lastRefresh.IsZero() && time.Since(c.lastRefresh) <= c.refreshInterval {
		return c.handlerCard, c.topicList, c.schemaSummary
	}

	c.handlerCard = BuildHandlerReferenceCard(c.registry)
	c.topicList = BuildTopicList()
	c.schemaSummary = BuildManifestSchemaSummary()
	c.lastRefresh = time.Now()

	return c.handlerCard, c.topicList, c.schemaSummary
}
