package refdata

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
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

	// Logger is the structured logger. Default: slog.Default().
	Logger *slog.Logger
}

const defaultCacheTTL = 5 * time.Minute

type cachedEntry struct {
	props     InstrumentProperties
	expiresAt time.Time
}

// CacheMetrics provides atomic counters for cache hit/miss metrics.
type CacheMetrics struct {
	Hits   atomic.Int64
	Misses atomic.Int64
}

// CachedResolver wraps a DataSource with in-memory caching.
// Safe for concurrent use.
type CachedResolver struct {
	source  DataSource
	ttl     time.Duration
	logger  *slog.Logger
	entries sync.Map // map[string]*cachedEntry
	Metrics CacheMetrics
}

// Verify CachedResolver implements InstrumentResolver.
var _ InstrumentResolver = (*CachedResolver)(nil)

// NewCachedResolver creates a new CachedResolver wrapping the given data source.
func NewCachedResolver(source DataSource, cfg CachedResolverConfig) *CachedResolver {
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = defaultCacheTTL
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &CachedResolver{
		source: source,
		ttl:    ttl,
		logger: logger,
	}
}

// Preload fetches all active instruments and populates the cache.
// Intended to be called at startup. Errors are logged but not fatal.
func (r *CachedResolver) Preload(ctx context.Context) error {
	instruments, err := r.source.FetchAllActive(ctx)
	if err != nil {
		return fmt.Errorf("preload instruments: %w", err)
	}

	now := time.Now()
	for _, props := range instruments {
		r.entries.Store(props.Code, &cachedEntry{
			props:     props,
			expiresAt: now.Add(r.ttl),
		})
	}

	r.logger.Info("instrument cache preloaded", "count", len(instruments))
	return nil
}

// Resolve returns instrument properties, using the cache when available.
func (r *CachedResolver) Resolve(ctx context.Context, code string) (InstrumentProperties, error) {
	// Check cache first
	if val, ok := r.entries.Load(code); ok {
		entry, _ := val.(*cachedEntry)
		if time.Now().Before(entry.expiresAt) {
			r.Metrics.Hits.Add(1)
			return entry.props, nil
		}
		// Expired - remove and fall through to fetch
		r.entries.Delete(code)
	}

	r.Metrics.Misses.Add(1)

	// Fetch from source
	props, err := r.source.FetchInstrument(ctx, code)
	if err != nil {
		return InstrumentProperties{}, err
	}

	// Store in cache
	r.entries.Store(code, &cachedEntry{
		props:     props,
		expiresAt: time.Now().Add(r.ttl),
	})

	return props, nil
}

// Invalidate removes a specific instrument from the cache.
func (r *CachedResolver) Invalidate(code string) {
	r.entries.Delete(code)
}

// InvalidateAll clears the entire cache.
func (r *CachedResolver) InvalidateAll() {
	r.entries.Range(func(key, _ any) bool {
		r.entries.Delete(key)
		return true
	})
}
