package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// CachedRegistry provides an in-memory cache for tenant validation.
// It caches tenant status to avoid database queries on every request.
// The cache is refreshed periodically in the background.
type CachedRegistry struct {
	repo            *persistence.Repository
	refreshInterval time.Duration
	refreshTimeout  time.Duration
	logger          *slog.Logger

	mu          sync.RWMutex
	cache       map[tenant.TenantID]*domain.Tenant
	lastRefresh time.Time
	refreshErr  error
}

// CachedRegistryConfig holds configuration for the cached registry.
type CachedRegistryConfig struct {
	RefreshInterval time.Duration
	RefreshTimeout  time.Duration
	Logger          *slog.Logger
}

// DefaultCachedRegistryConfig returns the default configuration.
func DefaultCachedRegistryConfig() CachedRegistryConfig {
	return CachedRegistryConfig{
		RefreshInterval: defaults.DefaultCircuitBreakerTimeout,
		RefreshTimeout:  defaults.DefaultRPCTimeout,
		Logger:          slog.Default(),
	}
}

// NewCachedRegistry creates a new cached tenant registry.
func NewCachedRegistry(repo *persistence.Repository, config CachedRegistryConfig) *CachedRegistry {
	if config.RefreshInterval <= 0 {
		config.RefreshInterval = defaults.DefaultCircuitBreakerTimeout
	}
	if config.RefreshTimeout <= 0 {
		config.RefreshTimeout = defaults.DefaultRPCTimeout
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	return &CachedRegistry{
		repo:            repo,
		cache:           make(map[tenant.TenantID]*domain.Tenant),
		refreshInterval: config.RefreshInterval,
		refreshTimeout:  config.RefreshTimeout,
		logger:          config.Logger,
	}
}

// Start begins the background cache refresh loop.
// The refresh loop stops automatically when the provided context is cancelled.
func (r *CachedRegistry) Start(ctx context.Context) {
	// Initial load
	if err := r.refresh(ctx); err != nil {
		r.logger.Error("failed initial cache load", "error", err)
	}

	// Background refresh loop
	go func() {
		ticker := time.NewTicker(r.refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.refresh(ctx); err != nil {
					r.logger.Error("failed to refresh tenant cache", "error", err)
				}
			}
		}
	}()
}

// IsActive checks if a tenant exists and is active.
// Uses the cache for fast lookups, falls back to database if cache miss.
func (r *CachedRegistry) IsActive(ctx context.Context, id tenant.TenantID) (bool, error) {
	// Try cache first
	r.mu.RLock()
	tenant, ok := r.cache[id]
	r.mu.RUnlock()

	if ok {
		return tenant.Status == domain.StatusActive, nil
	}

	// Cache miss - check database directly
	// This handles newly created tenants before cache refresh
	active, err := r.repo.IsActive(ctx, id)
	if err != nil {
		return false, err
	}

	return active, nil
}

// GetTenant retrieves a tenant from the cache.
// Returns nil if not found in cache (caller should query database if needed).
func (r *CachedRegistry) GetTenant(id tenant.TenantID) *domain.Tenant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cache[id]
}

// refresh reloads all tenants from the database into the cache.
// Uses fail-open strategy: if refresh fails, continue using stale cache.
// Uses a per-refresh timeout to prevent slow queries from blocking the refresh loop.
func (r *CachedRegistry) refresh(ctx context.Context) error {
	refreshCtx, cancel := context.WithTimeout(ctx, r.refreshTimeout)
	defer cancel()

	tenants, err := r.repo.GetAll(refreshCtx)
	if err != nil {
		r.mu.Lock()
		r.refreshErr = err
		r.mu.Unlock()
		return err
	}

	// Build new cache map atomically
	newCache := make(map[tenant.TenantID]*domain.Tenant, len(tenants))
	for _, tenant := range tenants {
		newCache[tenant.ID] = tenant
	}

	r.mu.Lock()
	r.cache = newCache
	r.lastRefresh = time.Now()
	r.refreshErr = nil
	refreshTime := r.lastRefresh
	r.mu.Unlock()

	r.logger.Debug("tenant cache refreshed",
		"count", len(tenants),
		"timestamp", refreshTime)

	return nil
}

// LastRefresh returns the timestamp of the last successful cache refresh.
func (r *CachedRegistry) LastRefresh() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastRefresh
}

// LastRefreshError returns the error from the last refresh attempt, if any.
func (r *CachedRegistry) LastRefreshError() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.refreshErr
}

// Count returns the number of tenants in the cache.
func (r *CachedRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.cache)
}
