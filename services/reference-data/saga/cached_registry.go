// Package saga provides a cached wrapper for Registry implementations.
package saga

import (
	"context"

	"github.com/google/uuid"
)

// CachedRegistry wraps a Registry with a Redis cache layer.
// Cache is read-through: misses fetch from the underlying registry.
// Cache is invalidated on write operations.
type CachedRegistry struct {
	registry Registry
	cache    *Cache
}

// NewCachedRegistry creates a new cached saga registry.
func NewCachedRegistry(registry Registry, cache *Cache) *CachedRegistry {
	return &CachedRegistry{
		registry: registry,
		cache:    cache,
	}
}

// GetByID retrieves a specific saga by its UUID.
// Checks cache first, then falls back to the underlying registry.
func (r *CachedRegistry) GetByID(ctx context.Context, id uuid.UUID) (*Definition, error) {
	// Try cache first
	if cached := r.cache.GetByID(ctx, id); cached != nil {
		return cached, nil
	}

	// Fetch from registry
	def, err := r.registry.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Cache the result
	r.cache.PutByID(ctx, def)
	r.cache.Put(ctx, def)

	return def, nil
}

// GetDefinition retrieves a specific saga by name and version.
// Checks cache first, then falls back to the underlying registry.
func (r *CachedRegistry) GetDefinition(ctx context.Context, name string, version int) (*Definition, error) {
	// Try cache first
	if cached := r.cache.Get(ctx, name, version); cached != nil {
		return cached, nil
	}

	// Fetch from registry
	def, err := r.registry.GetDefinition(ctx, name, version)
	if err != nil {
		return nil, err
	}

	// Cache the result
	r.cache.Put(ctx, def)
	r.cache.PutByID(ctx, def)

	return def, nil
}

// GetActive retrieves the active saga for a name using tenant resolution.
// Checks cache first, then falls back to the underlying registry.
func (r *CachedRegistry) GetActive(ctx context.Context, name string) (*Definition, error) {
	// Try cache first
	if cached := r.cache.GetActive(ctx, name); cached != nil {
		return cached, nil
	}

	// Fetch from registry
	def, err := r.registry.GetActive(ctx, name)
	if err != nil {
		return nil, err
	}

	// Cache the result
	r.cache.PutActive(ctx, def)
	r.cache.Put(ctx, def)
	r.cache.PutByID(ctx, def)

	return def, nil
}

// ListByStatus retrieves all sagas with the specified status.
// This is not cached as it returns potentially large result sets.
func (r *CachedRegistry) ListByStatus(ctx context.Context, status Status) ([]*Definition, error) {
	return r.registry.ListByStatus(ctx, status)
}

// CreateDraft creates a new saga definition in DRAFT status.
// No cache invalidation needed as drafts are not cached for active resolution.
func (r *CachedRegistry) CreateDraft(ctx context.Context, def *Definition) error {
	return r.registry.CreateDraft(ctx, def)
}

// UpdateDefinition updates a DRAFT saga definition.
// Invalidates the cache for the updated saga.
func (r *CachedRegistry) UpdateDefinition(ctx context.Context, id uuid.UUID, updates *Definition) error {
	// Get the current saga to know its name for cache invalidation
	current, err := r.registry.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := r.registry.UpdateDefinition(ctx, id, updates); err != nil {
		return err
	}

	// Invalidate cache
	r.cache.InvalidateByID(ctx, id)
	r.cache.Invalidate(ctx, current.Name, current.Version)

	return nil
}

// ActivateSaga transitions a saga from DRAFT to ACTIVE.
// Invalidates the active cache for the saga's name as the resolution may change.
func (r *CachedRegistry) ActivateSaga(ctx context.Context, id uuid.UUID) error {
	// Get the current saga to know its name for cache invalidation
	current, err := r.registry.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := r.registry.ActivateSaga(ctx, id); err != nil {
		return err
	}

	// Invalidate all cache entries for this saga name
	// because active resolution may have changed
	r.cache.InvalidateName(ctx, current.Name)

	return nil
}

// DeprecateSaga transitions a saga from ACTIVE to DEPRECATED.
// Invalidates the active cache for the saga's name as the resolution may change.
func (r *CachedRegistry) DeprecateSaga(ctx context.Context, id uuid.UUID, successorID *uuid.UUID) error {
	// Get the current saga to know its name for cache invalidation
	current, err := r.registry.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := r.registry.DeprecateSaga(ctx, id, successorID); err != nil {
		return err
	}

	// Invalidate all cache entries for this saga name
	// because active resolution may have changed
	r.cache.InvalidateName(ctx, current.Name)

	return nil
}
