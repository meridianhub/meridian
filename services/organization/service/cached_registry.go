package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/organization/adapters/persistence"
	"github.com/meridianhub/meridian/services/organization/domain"
	"github.com/meridianhub/meridian/shared/platform/organization"
)

// CachedRegistry provides an in-memory cache for organization validation.
// It caches organization status to avoid database queries on every request.
// The cache is refreshed periodically in the background.
type CachedRegistry struct {
	repo            *persistence.Repository
	refreshInterval time.Duration
	logger          *slog.Logger

	mu          sync.RWMutex
	cache       map[organization.OrganizationID]*domain.Organization
	lastRefresh time.Time
	refreshErr  error
}

// CachedRegistryConfig holds configuration for the cached registry.
type CachedRegistryConfig struct {
	RefreshInterval time.Duration
	Logger          *slog.Logger
}

// DefaultCachedRegistryConfig returns the default configuration.
func DefaultCachedRegistryConfig() CachedRegistryConfig {
	return CachedRegistryConfig{
		RefreshInterval: 60 * time.Second,
		Logger:          slog.Default(),
	}
}

// NewCachedRegistry creates a new cached organization registry.
func NewCachedRegistry(repo *persistence.Repository, config CachedRegistryConfig) *CachedRegistry {
	if config.RefreshInterval <= 0 {
		config.RefreshInterval = 60 * time.Second
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	return &CachedRegistry{
		repo:            repo,
		cache:           make(map[organization.OrganizationID]*domain.Organization),
		refreshInterval: config.RefreshInterval,
		logger:          config.Logger,
	}
}

// Start begins the background cache refresh loop.
// Call Stop() to stop the refresh loop.
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
					r.logger.Error("failed to refresh organization cache", "error", err)
				}
			}
		}
	}()
}

// IsActive checks if an organization exists and is active.
// Uses the cache for fast lookups, falls back to database if cache miss.
func (r *CachedRegistry) IsActive(ctx context.Context, id organization.OrganizationID) (bool, error) {
	// Try cache first
	r.mu.RLock()
	org, ok := r.cache[id]
	r.mu.RUnlock()

	if ok {
		return org.Status == domain.StatusActive, nil
	}

	// Cache miss - check database directly
	// This handles newly created organizations before cache refresh
	active, err := r.repo.IsActive(ctx, id)
	if err != nil {
		return false, err
	}

	return active, nil
}

// GetOrganization retrieves an organization from the cache.
// Returns nil if not found in cache (caller should query database if needed).
func (r *CachedRegistry) GetOrganization(id organization.OrganizationID) *domain.Organization {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cache[id]
}

// refresh reloads all organizations from the database into the cache.
// Uses fail-open strategy: if refresh fails, continue using stale cache.
func (r *CachedRegistry) refresh(ctx context.Context) error {
	orgs, err := r.repo.GetAll(ctx)
	if err != nil {
		r.mu.Lock()
		r.refreshErr = err
		r.mu.Unlock()
		return err
	}

	// Build new cache map atomically
	newCache := make(map[organization.OrganizationID]*domain.Organization, len(orgs))
	for _, org := range orgs {
		newCache[org.ID] = org
	}

	r.mu.Lock()
	r.cache = newCache
	r.lastRefresh = time.Now()
	r.refreshErr = nil
	refreshTime := r.lastRefresh
	r.mu.Unlock()

	r.logger.Debug("organization cache refreshed",
		"count", len(orgs),
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

// Count returns the number of organizations in the cache.
func (r *CachedRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.cache)
}
