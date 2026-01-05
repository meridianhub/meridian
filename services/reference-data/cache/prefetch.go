// Package cache provides caching infrastructure for instrument definitions
// with tenant isolation and TTL-based expiration.
package cache

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/google/cel-go/cel"

	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// RegistryLoader provides access to the instrument registry for prefetching.
// This interface is implemented by the service layer to decouple the cache
// from specific registry implementations.
type RegistryLoader interface {
	// ListActive retrieves all instruments with ACTIVE status for the tenant in context.
	ListActive(ctx context.Context) ([]*registry.InstrumentDefinition, error)

	// CompilePrograms compiles CEL programs for an instrument definition.
	// Returns the validation program, bucket key program, and any compilation error.
	// Either program may be nil if no expression is defined.
	CompilePrograms(def *registry.InstrumentDefinition) (validationPrg, bucketKeyPrg cel.Program, err error)
}

// Prefetcher loads all active instruments into the cache at startup.
// This ensures the cache is warm before the service starts accepting traffic,
// reducing cold-start latency for the first requests.
type Prefetcher struct {
	cache  *InstrumentCache
	loader RegistryLoader

	// prefetchComplete tracks whether prefetch has finished successfully.
	// Use atomic operations for thread-safe access.
	prefetchComplete atomic.Bool
}

// NewPrefetcher creates a new Prefetcher for warming the instrument cache.
func NewPrefetcher(cache *InstrumentCache, loader RegistryLoader) *Prefetcher {
	return &Prefetcher{
		cache:  cache,
		loader: loader,
	}
}

// Prefetch loads all active instruments for the tenant in context into the cache.
// The tenant context must be present in ctx.
//
// Returns an error if the tenant context is missing, the registry query fails,
// or any CEL compilation fails. On error, partial results may have been cached.
func (p *Prefetcher) Prefetch(ctx context.Context) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return ErrTenantContextRequired
	}

	// Load all active instruments from registry
	instruments, err := p.loader.ListActive(ctx)
	if err != nil {
		return fmt.Errorf("failed to list active instruments for tenant %s: %w", tenantID, err)
	}

	// Compile CEL programs and cache each instrument
	for _, def := range instruments {
		validationPrg, bucketKeyPrg, err := p.loader.CompilePrograms(def)
		if err != nil {
			return fmt.Errorf("failed to compile CEL programs for instrument %s v%d: %w",
				def.Code, def.Version, err)
		}

		cached := &CachedInstrument{
			Definition:        def,
			ValidationProgram: validationPrg,
			BucketKeyProgram:  bucketKeyPrg,
		}

		p.cache.Put(ctx, def.Code, def.Version, cached)
	}

	return nil
}

// PrefetchMultipleTenants loads all active instruments for multiple tenants.
// This is the primary method for startup prefetching when the service serves
// multiple tenants.
//
// The base context should not have a tenant set - tenant context will be added
// for each tenant ID. If any tenant prefetch fails, an error is returned and
// prefetch completion is not marked (partial results may be cached).
//
// On successful completion of all tenants, marks prefetch as complete.
func (p *Prefetcher) PrefetchMultipleTenants(ctx context.Context, tenantIDs []tenant.TenantID) error {
	for _, tenantID := range tenantIDs {
		// Check for context cancellation between tenants
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("prefetch cancelled: %w", err)
		}

		tenantCtx := tenant.WithTenant(ctx, tenantID)
		if err := p.Prefetch(tenantCtx); err != nil {
			return fmt.Errorf("prefetch failed for tenant %s: %w", tenantID, err)
		}
	}

	// Mark prefetch as complete only after all tenants succeed
	p.prefetchComplete.Store(true)

	return nil
}

// IsPrefetchComplete returns true if prefetch has completed successfully.
// This can be used by health checks to determine if the service is ready
// to accept traffic.
func (p *Prefetcher) IsPrefetchComplete() bool {
	return p.prefetchComplete.Load()
}

// MarkPrefetchComplete manually marks prefetch as complete.
// This is useful when prefetch is performed externally or skipped.
func (p *Prefetcher) MarkPrefetchComplete() {
	p.prefetchComplete.Store(true)
}

// ResetPrefetchStatus resets the prefetch completion status.
// This is primarily useful for testing.
func (p *Prefetcher) ResetPrefetchStatus() {
	p.prefetchComplete.Store(false)
}
