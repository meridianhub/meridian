// Package cache provides tiered caching for forward curve observations
// used by gateway CEL extension functions.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/singleflight"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Default configuration values for the forward curve cache.
const (
	DefaultL1Size   = 10000
	DefaultL1TTL    = 5 * time.Minute
	DefaultL1Jitter = 30 * time.Second
	DefaultL2TTL    = 30 * time.Minute
	DefaultL2Prefix = "fwd"
)

// Errors returned by the forward curve cache.
var (
	ErrTenantContextRequired = errors.New("tenant context required")
	ErrObservationNotFound   = errors.New("forward curve observation not found")
	ErrUnexpectedResultType  = errors.New("unexpected result type from singleflight")
	ErrNilSource             = errors.New("source must not be nil")
)

// Prometheus metrics for forward curve cache.
var (
	cacheHitsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "forward_curve_cache_hits_total",
			Help: "Total forward curve cache hits by level",
		},
		[]string{"level"},
	)

	cacheLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "forward_curve_cache_latency_seconds",
			Help:    "Forward curve cache lookup latency by level",
			Buckets: []float64{.0001, .0005, .001, .005, .01, .05, .1, .5, 1},
		},
		[]string{"level"},
	)

	celEvaluationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "forward_curve_cel_evaluations_total",
			Help: "Total CEL forward curve function evaluations",
		},
		[]string{"function"},
	)
)

// Observation represents a cached forward curve observation.
type Observation struct {
	Value       decimal.Decimal   `json:"value"`
	Unit        string            `json:"unit"`
	Quality     string            `json:"quality"`
	ObservedAt  time.Time         `json:"observed_at"`
	ValidFrom   time.Time         `json:"valid_from"`
	ValidTo     time.Time         `json:"valid_to"`
	DataSetCode string            `json:"dataset_code"`
	SourceID    string            `json:"source_id"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// l1Entry wraps an Observation with TTL tracking.
type l1Entry struct {
	obs       *Observation
	expiresAt time.Time
}

// l1Key is the key for the L1 in-memory cache.
type l1Key struct {
	TenantID      string
	ResolutionKey string
	HourEpoch     int64
}

// Source queries the Market Data Service for forward curve observations.
type Source interface {
	// GetForwardPrice queries MDS for a single forward curve observation.
	// Returns ErrObservationNotFound if no matching observation exists.
	GetForwardPrice(ctx context.Context, resolutionKey string, ts time.Time) (*Observation, error)

	// GetForwardPriceRange queries MDS for observations in a time range.
	GetForwardPriceRange(ctx context.Context, resolutionKey string, start, end time.Time) ([]*Observation, error)
}

// L2Client defines the interface for the Redis L2 cache layer.
type L2Client interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

// ForwardCurveCache provides tiered caching for forward curve observations:
//   - L1: In-memory LRU with TTL (fast, per-instance)
//   - L2: Redis with TTL (shared across instances)
//   - L3: MDS gRPC query (source of truth)
//
// Thread-safety: All methods are safe for concurrent use.
type ForwardCurveCache struct {
	l1      *lru.Cache[l1Key, *l1Entry]
	l2      L2Client
	source  Source
	sfGroup singleflight.Group

	l1TTL    time.Duration
	l1Jitter time.Duration
	l2TTL    time.Duration
	l2Prefix string

	// Stats counters (atomic)
	l1Hits      int64
	l1Misses    int64
	l2Hits      int64
	l2Misses    int64
	sourceLoads int64
}

// Option configures a ForwardCurveCache.
type Option func(*ForwardCurveCache)

// WithL1Size sets the maximum number of entries in the L1 cache.
// Size must be positive; non-positive values are ignored.
func WithL1Size(size int) Option {
	return func(c *ForwardCurveCache) {
		if size <= 0 {
			return
		}
		newL1, err := lru.New[l1Key, *l1Entry](size)
		if err != nil {
			return
		}
		c.l1 = newL1
	}
}

// WithL1TTL sets the base TTL and jitter for L1 cache entries.
func WithL1TTL(ttl, jitter time.Duration) Option {
	return func(c *ForwardCurveCache) {
		c.l1TTL = ttl
		c.l1Jitter = jitter
	}
}

// WithL2TTL sets the TTL for L2 (Redis) cache entries.
func WithL2TTL(ttl time.Duration) Option {
	return func(c *ForwardCurveCache) {
		c.l2TTL = ttl
	}
}

// WithL2Prefix sets the key prefix for L2 (Redis) cache entries.
func WithL2Prefix(prefix string) Option {
	return func(c *ForwardCurveCache) {
		c.l2Prefix = prefix
	}
}

// NewForwardCurveCache creates a new tiered forward curve cache.
// Source must not be nil. If l2 is nil, L2 caching is disabled (L1 -> L3 only).
func NewForwardCurveCache(source Source, l2 L2Client, opts ...Option) (*ForwardCurveCache, error) {
	if source == nil {
		return nil, ErrNilSource
	}

	l1, err := lru.New[l1Key, *l1Entry](DefaultL1Size)
	if err != nil {
		return nil, fmt.Errorf("create L1 cache: %w", err)
	}

	c := &ForwardCurveCache{
		l1:       l1,
		l2:       l2,
		source:   source,
		l1TTL:    DefaultL1TTL,
		l1Jitter: DefaultL1Jitter,
		l2TTL:    DefaultL2TTL,
		l2Prefix: DefaultL2Prefix,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// Get retrieves a forward curve observation for the given resolution key and timestamp.
// Implements the tiered lookup: L1 -> L2 -> L3 (MDS).
func (c *ForwardCurveCache) Get(ctx context.Context, resolutionKey string, ts time.Time) (*Observation, error) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrTenantContextRequired
	}

	hourEpoch := truncateToHour(ts)
	key := l1Key{
		TenantID:      string(tenantID),
		ResolutionKey: resolutionKey,
		HourEpoch:     hourEpoch,
	}

	// L1 lookup
	start := time.Now()
	if entry, found := c.l1.Get(key); found {
		if time.Now().Before(entry.expiresAt) {
			atomic.AddInt64(&c.l1Hits, 1)
			cacheHitsTotal.WithLabelValues("local").Inc()
			cacheLatency.WithLabelValues("local").Observe(time.Since(start).Seconds())
			return entry.obs, nil
		}
		c.l1.Remove(key)
	}
	atomic.AddInt64(&c.l1Misses, 1)

	// Singleflight to deduplicate concurrent lookups
	sfKey := fmt.Sprintf("%s:%s:%d", tenantID, resolutionKey, hourEpoch)
	result, err, _ := c.sfGroup.Do(sfKey, func() (interface{}, error) {
		return c.loadFromL2OrSource(ctx, key, string(tenantID), resolutionKey, hourEpoch, ts)
	})

	if err != nil {
		return nil, err
	}

	obs, ok := result.(*Observation)
	if !ok {
		return nil, ErrUnexpectedResultType
	}

	return obs, nil
}

// loadFromL2OrSource attempts L1 (double-check), L2, then L3 lookup.
// Called within a singleflight group to deduplicate concurrent cache misses.
func (c *ForwardCurveCache) loadFromL2OrSource(ctx context.Context, key l1Key, tenantID, resolutionKey string, hourEpoch int64, ts time.Time) (*Observation, error) {
	// Double-check L1 after acquiring singleflight slot
	if entry, found := c.l1.Get(key); found && time.Now().Before(entry.expiresAt) {
		return entry.obs, nil
	}

	// L2 lookup
	obs := c.getFromL2(ctx, tenantID, resolutionKey, hourEpoch)
	if obs != nil {
		atomic.AddInt64(&c.l2Hits, 1)
		cacheHitsTotal.WithLabelValues("redis").Inc()
		c.putL1(key, obs)
		return obs, nil
	}
	atomic.AddInt64(&c.l2Misses, 1)
	cacheHitsTotal.WithLabelValues("miss").Inc()

	// L3 lookup (source of truth)
	start := time.Now()
	obs, err := c.source.GetForwardPrice(ctx, resolutionKey, ts)
	cacheLatency.WithLabelValues("source").Observe(time.Since(start).Seconds())
	if err != nil {
		return nil, err
	}
	atomic.AddInt64(&c.sourceLoads, 1)

	// Populate L2 (best-effort)
	c.putL2(ctx, tenantID, resolutionKey, hourEpoch, obs)

	// Populate L1
	c.putL1(key, obs)

	return obs, nil
}

// rangeCollector accumulates observations from cache lookups and tracks misses.
type rangeCollector struct {
	hours      []int64
	obsByEpoch map[int64]*Observation
	missStart  time.Time
	missEnd    time.Time
	haveMiss   bool
}

// GetRange retrieves multiple forward curve observations in a time range.
// Tries L1/L2 for each hourly bucket, falls back to L3 for misses.
// Results are returned in chronological order by ValidFrom.
func (c *ForwardCurveCache) GetRange(ctx context.Context, resolutionKey string, start, end time.Time) ([]*Observation, error) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrTenantContextRequired
	}

	tid := string(tenantID)
	rc := c.collectCachedBuckets(ctx, tid, resolutionKey, start, end)

	if rc.haveMiss {
		if err := c.backfillMisses(ctx, tid, resolutionKey, rc); err != nil {
			return nil, err
		}
	}

	// Assemble results in chronological order
	observations := make([]*Observation, 0, len(rc.obsByEpoch))
	for _, epoch := range rc.hours {
		if obs, ok := rc.obsByEpoch[epoch]; ok {
			observations = append(observations, obs)
		}
	}

	return observations, nil
}

// collectCachedBuckets iterates over hourly buckets in [start, end], checking L1 and L2
// caches for each. Returns a collector with hits indexed by epoch and miss range tracked.
func (c *ForwardCurveCache) collectCachedBuckets(ctx context.Context, tenantID, resolutionKey string, start, end time.Time) *rangeCollector {
	current := start.Truncate(time.Hour)
	endTrunc := end.Truncate(time.Hour)

	rc := &rangeCollector{obsByEpoch: make(map[int64]*Observation)}

	for !current.After(endTrunc) {
		hourEpoch := current.Unix()
		rc.hours = append(rc.hours, hourEpoch)
		key := l1Key{TenantID: tenantID, ResolutionKey: resolutionKey, HourEpoch: hourEpoch}

		if entry, found := c.l1.Get(key); found {
			if time.Now().Before(entry.expiresAt) {
				rc.obsByEpoch[hourEpoch] = entry.obs
				current = current.Add(time.Hour)
				continue
			}
			c.l1.Remove(key)
		}

		if obs := c.getFromL2(ctx, tenantID, resolutionKey, hourEpoch); obs != nil {
			c.putL1(key, obs)
			rc.obsByEpoch[hourEpoch] = obs
			current = current.Add(time.Hour)
			continue
		}

		if !rc.haveMiss {
			rc.missStart = current
			rc.haveMiss = true
		}
		rc.missEnd = current
		current = current.Add(time.Hour)
	}

	return rc
}

// backfillMisses bulk-queries the source for the miss range and populates both caches.
func (c *ForwardCurveCache) backfillMisses(ctx context.Context, tenantID, resolutionKey string, rc *rangeCollector) error {
	rangeObs, err := c.source.GetForwardPriceRange(ctx, resolutionKey, rc.missStart, rc.missEnd.Add(time.Hour))
	if err != nil {
		return err
	}

	for _, obs := range rangeObs {
		hourEpoch := truncateToHour(obs.ValidFrom)
		key := l1Key{TenantID: tenantID, ResolutionKey: resolutionKey, HourEpoch: hourEpoch}
		c.putL1(key, obs)
		c.putL2(ctx, tenantID, resolutionKey, hourEpoch, obs)
		rc.obsByEpoch[hourEpoch] = obs
	}

	return nil
}

// Invalidate removes a specific entry from L1 cache.
func (c *ForwardCurveCache) Invalidate(tenantID string, resolutionKey string, ts time.Time) {
	key := l1Key{
		TenantID:      tenantID,
		ResolutionKey: resolutionKey,
		HourEpoch:     truncateToHour(ts),
	}
	c.l1.Remove(key)
}

// Stats returns cache statistics.
type Stats struct {
	L1Hits      int64
	L1Misses    int64
	L2Hits      int64
	L2Misses    int64
	SourceLoads int64
	L1Size      int
}

// Stats returns current cache statistics.
func (c *ForwardCurveCache) Stats() Stats {
	return Stats{
		L1Hits:      atomic.LoadInt64(&c.l1Hits),
		L1Misses:    atomic.LoadInt64(&c.l1Misses),
		L2Hits:      atomic.LoadInt64(&c.l2Hits),
		L2Misses:    atomic.LoadInt64(&c.l2Misses),
		SourceLoads: atomic.LoadInt64(&c.sourceLoads),
		L1Size:      c.l1.Len(),
	}
}

// putL1 stores an observation in the L1 cache with jittered TTL.
func (c *ForwardCurveCache) putL1(key l1Key, obs *Observation) {
	c.l1.Add(key, &l1Entry{
		obs:       obs,
		expiresAt: time.Now().Add(c.jitteredL1TTL()),
	})
}

// getFromL2 retrieves an observation from the L2 Redis cache.
// Returns nil on cache miss, error, or if Redis is unavailable.
func (c *ForwardCurveCache) getFromL2(ctx context.Context, tenantID, resolutionKey string, hourEpoch int64) *Observation {
	if c.l2 == nil {
		return nil
	}

	start := time.Now()
	redisKey := c.l2Key(tenantID, resolutionKey, hourEpoch)
	val, err := c.l2.Get(ctx, redisKey).Result()
	cacheLatency.WithLabelValues("redis").Observe(time.Since(start).Seconds())

	if err != nil {
		return nil
	}

	var obs Observation
	if err := json.Unmarshal([]byte(val), &obs); err != nil {
		return nil
	}

	return &obs
}

// putL2 stores an observation in the L2 Redis cache (best-effort).
func (c *ForwardCurveCache) putL2(ctx context.Context, tenantID, resolutionKey string, hourEpoch int64, obs *Observation) {
	if c.l2 == nil {
		return
	}

	data, err := json.Marshal(obs)
	if err != nil {
		return
	}

	redisKey := c.l2Key(tenantID, resolutionKey, hourEpoch)
	c.l2.Set(ctx, redisKey, data, c.l2TTL)
}

// l2Key builds the Redis key for a forward curve cache entry.
// Format: fwd:{tenant_id}:{resolution_key}:{hour_epoch}
func (c *ForwardCurveCache) l2Key(tenantID, resolutionKey string, hourEpoch int64) string {
	return c.l2Prefix + ":" + tenantID + ":" + resolutionKey + ":" + strconv.FormatInt(hourEpoch, 10)
}

// jitteredL1TTL returns the base L1 TTL plus random jitter.
func (c *ForwardCurveCache) jitteredL1TTL() time.Duration {
	if c.l1Jitter <= 0 {
		return c.l1TTL
	}
	jitterRange := int64(c.l1Jitter) * 2
	jitter := rand.Int64N(jitterRange) - int64(c.l1Jitter)
	return c.l1TTL + time.Duration(jitter)
}

// truncateToHour returns the Unix timestamp of the hour containing ts.
func truncateToHour(ts time.Time) int64 {
	return ts.Truncate(time.Hour).Unix()
}

// RecordCELEvaluation increments the CEL evaluation counter for a function.
func RecordCELEvaluation(function string) {
	celEvaluationsTotal.WithLabelValues(function).Inc()
}
