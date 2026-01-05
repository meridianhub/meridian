// Package cache provides caching infrastructure for instrument definitions
// with tenant isolation and TTL-based expiration.
package cache

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/google/cel-go/cel"
	"golang.org/x/sync/singleflight"

	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// L2Cache defines the interface for L2 (Redis) caching of instrument definitions.
// L2 cache stores serialized InstrumentDefinition protos without CEL programs.
// CEL programs cannot be serialized and must be compiled after retrieval from L2.
//
// All methods extract tenant context from ctx using shared/platform/tenant.
// Methods are designed to be non-blocking and fail-safe:
// - Get returns nil on cache miss or error (no error propagation)
// - Put/Invalidate are best-effort operations
type L2Cache interface {
	// Get retrieves an instrument definition from the L2 cache.
	// Returns nil if the entry is not found, expired, or tenant context is missing.
	// Does NOT return errors - cache misses are expected and should not propagate errors.
	Get(ctx context.Context, code string, version int) *registry.InstrumentDefinition

	// Put stores an instrument definition in the L2 cache.
	// Does nothing if tenant context is missing or definition is nil.
	// This is a best-effort operation - failures are silently ignored.
	Put(ctx context.Context, code string, version int, def *registry.InstrumentDefinition)

	// Invalidate removes a specific entry from the L2 cache.
	// Does nothing if tenant context is missing.
	Invalidate(ctx context.Context, code string, version int)

	// InvalidateCode removes all versions of an instrument code from the L2 cache.
	// Does nothing if tenant context is missing.
	InvalidateCode(ctx context.Context, code string)

	// InvalidateAll removes all instrument cache entries for the tenant.
	// Does nothing if tenant context is missing.
	InvalidateAll(ctx context.Context)
}

// Source defines the interface for loading instrument definitions from the source of truth.
// This is typically the database via the InstrumentRegistry.
type Source interface {
	// GetDefinition retrieves a specific instrument by code and version.
	// Returns registry.ErrNotFound if the instrument doesn't exist.
	GetDefinition(ctx context.Context, code string, version int) (*registry.InstrumentDefinition, error)
}

// TieredCache provides a multi-level cache for instrument definitions with:
// - L1: In-memory LRU cache with compiled CEL programs (fast, short TTL)
// - L2: Redis cache with serialized protos (warm, longer TTL)
// - Source: Database via InstrumentRegistry (persistent, source of truth)
//
// Cache flow:
// 1. Check L1 (memory) - returns CachedInstrument with compiled CEL programs
// 2. If L1 miss, check L2 (Redis) - returns InstrumentDefinition (no CEL)
// 3. If L2 hit, compile CEL programs, store in L1, return
// 4. If L2 miss, fetch from Source, store in L2 and L1, return
//
// CEL program compilation happens only on L1 cache population.
// This ensures CEL programs are compiled once per L1 entry.
//
// Write-through invalidation:
// - On instrument update: Invalidate L1 and L2
// - On instrument activation: Invalidate L1 and L2 for the code
// - On instrument deprecation: Invalidate L1 and L2 for the code
type TieredCache interface {
	// Get retrieves a cached instrument with compiled CEL programs.
	// Implements the tiered lookup: L1 -> L2 -> Source.
	// Returns nil and error if the instrument cannot be found or loading fails.
	Get(ctx context.Context, code string, version int) (*CachedInstrument, error)

	// Invalidate removes a specific entry from all cache tiers.
	// This is called after instrument updates to ensure consistency.
	Invalidate(ctx context.Context, code string, version int)

	// InvalidateCode removes all versions of an instrument code from all cache tiers.
	// This is called after instrument activation/deprecation.
	InvalidateCode(ctx context.Context, code string)

	// InvalidateAll removes all entries for the tenant from all cache tiers.
	// This is an admin operation for emergency cache clearing.
	InvalidateAll(ctx context.Context)

	// Stats returns cache statistics for monitoring.
	Stats(ctx context.Context) TieredCacheStats
}

// TieredCacheStats contains statistics for monitoring cache performance.
type TieredCacheStats struct {
	// L1Size is the current number of entries in the L1 cache for this tenant.
	L1Size int

	// L1Capacity is the maximum capacity of the L1 cache.
	L1Capacity int

	// L1Hits is the number of L1 cache hits (since service start).
	L1Hits int64

	// L1Misses is the number of L1 cache misses (since service start).
	L1Misses int64

	// L2Hits is the number of L2 cache hits (since service start).
	L2Hits int64

	// L2Misses is the number of L2 cache misses (since service start).
	L2Misses int64

	// SourceLoads is the number of loads from source (since service start).
	SourceLoads int64
}

// NoOpL2Cache is an L2Cache implementation that does nothing.
// Useful when Redis is not available or for testing.
type NoOpL2Cache struct{}

var _ L2Cache = (*NoOpL2Cache)(nil)

// Get always returns nil (cache miss).
func (n *NoOpL2Cache) Get(_ context.Context, _ string, _ int) *registry.InstrumentDefinition {
	return nil
}

// Put does nothing.
func (n *NoOpL2Cache) Put(_ context.Context, _ string, _ int, _ *registry.InstrumentDefinition) {}

// Invalidate does nothing.
func (n *NoOpL2Cache) Invalidate(_ context.Context, _ string, _ int) {}

// InvalidateCode does nothing.
func (n *NoOpL2Cache) InvalidateCode(_ context.Context, _ string) {}

// InvalidateAll does nothing.
func (n *NoOpL2Cache) InvalidateAll(_ context.Context) {}

// CELCompiler defines the interface for compiling CEL expressions.
// This is typically implemented by cel.Compiler.
type CELCompiler interface {
	// CompileValidation compiles a validation expression.
	// Returns nil if the expression is empty.
	CompileValidation(expression string) (cel.Program, error)

	// CompileBucketKey compiles a bucket key expression.
	// Returns nil if the expression is empty.
	CompileBucketKey(expression string) (cel.Program, error)
}

// TieredInstrumentCache provides a multi-level cache with L1 (memory) -> L2 (Redis) -> Source (gRPC/database).
// It implements the TieredCache interface and provides cold start resilience via the L2 cache.
//
// Cache flow:
// 1. Check L1 (memory) - returns CachedInstrument with compiled CEL programs
// 2. If L1 miss, check L2 (Redis) - returns InstrumentDefinition (no CEL)
// 3. If L2 hit, compile CEL programs, store in L1, return
// 4. If L2 miss, fetch from Source, store in L2 and L1, return
//
// Thread-safety: All methods are safe for concurrent use.
// Singleflight is used to deduplicate concurrent requests for the same key.
type TieredInstrumentCache struct {
	l1       *InstrumentCache
	l2       L2Cache
	source   Source
	compiler CELCompiler

	// sfGroup deduplicates concurrent cache misses for the same key.
	// The singleflight key includes tenant ID to maintain isolation.
	sfGroup singleflight.Group

	// Stats counters (atomic)
	l1Hits      int64
	l1Misses    int64
	l2Hits      int64
	l2Misses    int64
	sourceLoads int64
}

// Verify TieredInstrumentCache implements TieredCache.
var _ TieredCache = (*TieredInstrumentCache)(nil)

// NewTieredInstrumentCache creates a new tiered cache with the given components.
// If l2 is nil, a NoOpL2Cache is used (disabling L2 caching).
// If compiler is nil, CEL compilation will be skipped.
func NewTieredInstrumentCache(l1 *InstrumentCache, l2 L2Cache, source Source, compiler CELCompiler) *TieredInstrumentCache {
	if l2 == nil {
		l2 = &NoOpL2Cache{}
	}

	return &TieredInstrumentCache{
		l1:       l1,
		l2:       l2,
		source:   source,
		compiler: compiler,
	}
}

// Get retrieves a cached instrument with compiled CEL programs.
// Implements the tiered lookup: L1 -> L2 -> Source.
// Uses singleflight to deduplicate concurrent requests for the same key.
func (t *TieredInstrumentCache) Get(ctx context.Context, code string, version int) (*CachedInstrument, error) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrTenantContextRequired
	}

	// Fast path: check L1 cache
	if cached := t.l1.Get(ctx, code, version); cached != nil {
		atomic.AddInt64(&t.l1Hits, 1)
		return cached, nil
	}
	atomic.AddInt64(&t.l1Misses, 1)

	// Slow path: use singleflight to deduplicate concurrent fetches
	sfKey := fmt.Sprintf("%s:%s:%d", tenantID, code, version)

	result, err, _ := t.sfGroup.Do(sfKey, func() (interface{}, error) {
		// Double-check L1 after acquiring singleflight slot
		// Another goroutine might have populated the cache
		if cached := t.l1.Get(ctx, code, version); cached != nil {
			return cached, nil
		}

		// Check L2 cache
		if def := t.l2.Get(ctx, code, version); def != nil {
			atomic.AddInt64(&t.l2Hits, 1)

			// Compile CEL programs and populate L1
			cached, err := t.compileCEL(def)
			if err != nil {
				return nil, err
			}
			t.l1.Put(ctx, code, version, cached)
			return cached, nil
		}
		atomic.AddInt64(&t.l2Misses, 1)

		// Fetch from source
		def, err := t.source.GetDefinition(ctx, code, version)
		if err != nil {
			return nil, err
		}
		atomic.AddInt64(&t.sourceLoads, 1)

		// Populate L2 (non-blocking, best-effort)
		t.l2.Put(ctx, code, version, def)

		// Compile CEL programs and populate L1
		cached, err := t.compileCEL(def)
		if err != nil {
			return nil, err
		}
		t.l1.Put(ctx, code, version, cached)

		return cached, nil
	})

	if err != nil {
		return nil, err
	}

	cached, ok := result.(*CachedInstrument)
	if !ok {
		return nil, ErrUnexpectedResultType
	}

	return cached, nil
}

// compileCEL compiles CEL programs for the given definition.
// Returns a CachedInstrument with the definition and compiled programs.
func (t *TieredInstrumentCache) compileCEL(def *registry.InstrumentDefinition) (*CachedInstrument, error) {
	cached := &CachedInstrument{
		Definition: def,
	}

	if t.compiler == nil {
		return cached, nil
	}

	// Compile validation expression if present
	if def.ValidationExpression != "" {
		prg, err := t.compiler.CompileValidation(def.ValidationExpression)
		if err != nil {
			return nil, fmt.Errorf("compile validation expression: %w", err)
		}
		cached.ValidationProgram = prg
	}

	// Compile bucket key expression if present
	if def.FungibilityKeyExpression != "" {
		prg, err := t.compiler.CompileBucketKey(def.FungibilityKeyExpression)
		if err != nil {
			return nil, fmt.Errorf("compile bucket key expression: %w", err)
		}
		cached.BucketKeyProgram = prg
	}

	return cached, nil
}

// Invalidate removes a specific entry from all cache tiers.
func (t *TieredInstrumentCache) Invalidate(ctx context.Context, code string, version int) {
	t.l1.Invalidate(ctx, code, version)
	t.l2.Invalidate(ctx, code, version)
}

// InvalidateCode removes all versions of an instrument code from all cache tiers.
func (t *TieredInstrumentCache) InvalidateCode(ctx context.Context, code string) {
	t.l1.InvalidateCode(ctx, code)
	t.l2.InvalidateCode(ctx, code)
}

// InvalidateAll removes all entries for the tenant from all cache tiers.
func (t *TieredInstrumentCache) InvalidateAll(ctx context.Context) {
	t.l1.InvalidateAll(ctx)
	t.l2.InvalidateAll(ctx)
}

// Stats returns cache statistics for monitoring.
func (t *TieredInstrumentCache) Stats(ctx context.Context) TieredCacheStats {
	l1Size, l1Capacity := t.l1.Stats(ctx)

	return TieredCacheStats{
		L1Size:      l1Size,
		L1Capacity:  l1Capacity,
		L1Hits:      atomic.LoadInt64(&t.l1Hits),
		L1Misses:    atomic.LoadInt64(&t.l1Misses),
		L2Hits:      atomic.LoadInt64(&t.l2Hits),
		L2Misses:    atomic.LoadInt64(&t.l2Misses),
		SourceLoads: atomic.LoadInt64(&t.sourceLoads),
	}
}
