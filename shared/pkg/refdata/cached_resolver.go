package refdata

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/sync/singleflight"
)

// DataSource provides instrument properties from an upstream source (e.g., gRPC).
// Implementations must be safe for concurrent use.
type DataSource interface {
	// FetchInstrument retrieves properties for a single instrument code.
	// Returns ErrUnknownInstrument if the instrument cannot be found.
	FetchInstrument(ctx context.Context, code string) (InstrumentProperties, error)

	// FetchAllActive retrieves all active instrument properties.
	FetchAllActive(ctx context.Context) ([]InstrumentProperties, error)
}

// CachedResolverConfig holds configuration for the CachedResolver.
type CachedResolverConfig struct {
	// TTL is the time-to-live for cached entries. Default: 5 minutes.
	TTL time.Duration

	// MaxEntries is the maximum number of cached instruments. Default: 10000.
	MaxEntries int

	// Logger is the structured logger. Default: slog.Default().
	Logger *slog.Logger
}

const (
	defaultCacheTTL        = 5 * time.Minute
	defaultCacheMaxEntries = 10000
)

type cachedEntry struct {
	props     InstrumentProperties
	expiresAt time.Time
}

// CacheMetrics provides atomic counters for cache hit/miss metrics.
type CacheMetrics struct {
	Hits   atomic.Int64
	Misses atomic.Int64
}

// CachedResolver wraps a DataSource with a bounded LRU cache and singleflight
// to coalesce concurrent upstream fetches. Safe for concurrent use.
type CachedResolver struct {
	source  DataSource
	ttl     time.Duration
	logger  *slog.Logger
	cache   *lru.Cache[string, *cachedEntry]
	flight  singleflight.Group
	Metrics CacheMetrics
}

// Verify CachedResolver implements InstrumentResolver.
var _ InstrumentResolver = (*CachedResolver)(nil)

// NewCachedResolver creates a new CachedResolver wrapping the given data source.
func NewCachedResolver(source DataSource, cfg CachedResolverConfig) *CachedResolver {
	if source == nil {
		panic("refdata: DataSource must not be nil")
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultCacheMaxEntries
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// LRU constructor only returns error when size <= 0.
	cache, _ := lru.New[string, *cachedEntry](maxEntries)
	return &CachedResolver{
		source: source,
		ttl:    ttl,
		logger: logger,
		cache:  cache,
	}
}

// Preload fetches all active instruments and populates the cache.
// Intended to be called at startup.
func (r *CachedResolver) Preload(ctx context.Context) error {
	instruments, err := r.source.FetchAllActive(ctx)
	if err != nil {
		return fmt.Errorf("preload instruments: %w", err)
	}

	r.cache.Purge() // Replace active set rather than merging to remove deactivated instruments
	now := time.Now()
	for _, props := range instruments {
		r.cache.Add(props.Code, &cachedEntry{
			props:     props,
			expiresAt: now.Add(r.ttl),
		})
	}

	r.logger.Info("instrument cache preloaded", "count", len(instruments))
	return nil
}

// Resolve returns instrument properties, using the cache when available.
// Concurrent requests for the same code are coalesced via singleflight.
func (r *CachedResolver) Resolve(ctx context.Context, code string) (InstrumentProperties, error) {
	// Check cache first
	if entry, ok := r.cache.Get(code); ok {
		if time.Now().Before(entry.expiresAt) {
			r.Metrics.Hits.Add(1)
			return entry.props, nil
		}
		// Expired - remove and fall through to fetch
		r.cache.Remove(code)
	}

	r.Metrics.Misses.Add(1)

	// Use singleflight to coalesce concurrent fetches for the same code
	val, err, _ := r.flight.Do(code, func() (any, error) {
		// Double-check cache (another goroutine may have populated it)
		if entry, ok := r.cache.Get(code); ok {
			if time.Now().Before(entry.expiresAt) {
				return entry.props, nil
			}
		}

		props, fetchErr := r.source.FetchInstrument(ctx, code)
		if fetchErr != nil {
			return InstrumentProperties{}, fetchErr
		}

		r.cache.Add(code, &cachedEntry{
			props:     props,
			expiresAt: time.Now().Add(r.ttl),
		})

		return props, nil
	})
	if err != nil {
		return InstrumentProperties{}, err
	}

	props, _ := val.(InstrumentProperties)
	return props, nil
}

// Invalidate removes a specific instrument from the cache.
func (r *CachedResolver) Invalidate(code string) {
	r.cache.Remove(code)
}

// InvalidateAll clears the entire cache.
func (r *CachedResolver) InvalidateAll() {
	r.cache.Purge()
}
