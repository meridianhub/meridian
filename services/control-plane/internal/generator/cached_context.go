package generator

import (
	"io/fs"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// defaultRefreshInterval is the default time before cached static components are refreshed.
const defaultRefreshInterval = 5 * time.Minute

// staticComponents holds the three cached static prompt sections.
type staticComponents struct {
	handlerCard   string
	topicList     string
	schemaSummary string
}

// CachedContextAssembler wraps AssembleContext with a caching layer for the static
// components (handler reference card, topic list, schema summary) that are expensive
// to compute but rarely change. Pattern matching remains uncached because it varies
// per request based on the business description and industry.
type CachedContextAssembler struct {
	mu              sync.RWMutex
	cached          staticComponents
	lastRefresh     time.Time
	refreshInterval time.Duration

	registry   *schema.Registry
	cookbookFS fs.FS

	// buildStatics is the function used to compute static components. It is a field
	// so tests can inject a counting wrapper to verify cache behavior.
	buildStatics func(registry *schema.Registry) staticComponents
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
		buildStatics:    defaultBuildStatics,
	}
}

// AssembleContext assembles a generation prompt using cached static components and
// fresh per-request pattern matching. It is a drop-in replacement for the package-level
// AssembleContext function and returns the same types.
//
// Returns ErrMissingRegistry if the assembler was constructed with a nil registry.
func (c *CachedContextAssembler) AssembleContext(opts ContextAssemblerOptions) (*AssembledContext, error) {
	if c.registry == nil {
		return nil, ErrMissingRegistry
	}
	statics := c.getStatics()
	return assembleContextWithStatics(opts, statics.handlerCard, statics.topicList, statics.schemaSummary, c.registry, c.cookbookFS)
}

// Invalidate clears the cached static components, forcing a refresh on the next call.
// Useful when the registry changes at runtime.
func (c *CachedContextAssembler) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastRefresh = time.Time{}
}

// getStatics returns the cached static components, refreshing them if the cache is
// stale or has not been populated yet.
func (c *CachedContextAssembler) getStatics() staticComponents {
	// Fast path: read lock — cache is fresh.
	c.mu.RLock()
	if !c.lastRefresh.IsZero() && time.Since(c.lastRefresh) <= c.refreshInterval {
		s := c.cached
		c.mu.RUnlock()
		return s
	}
	c.mu.RUnlock()

	// Slow path: write lock — refresh the cache.
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock to avoid redundant work from concurrent refreshes.
	if !c.lastRefresh.IsZero() && time.Since(c.lastRefresh) <= c.refreshInterval {
		return c.cached
	}

	c.cached = c.buildStatics(c.registry)
	c.lastRefresh = time.Now()

	return c.cached
}

// defaultBuildStatics computes the three static prompt sections from the registry.
func defaultBuildStatics(registry *schema.Registry) staticComponents {
	return staticComponents{
		handlerCard:   BuildHandlerReferenceCard(registry),
		topicList:     BuildTopicList(),
		schemaSummary: BuildManifestSchemaSummary(),
	}
}
