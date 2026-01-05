// Package cache provides caching infrastructure for instrument definitions
// with tenant isolation and TTL-based expiration.
package cache

import (
	"context"

	"github.com/meridianhub/meridian/services/reference-data/registry"
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
