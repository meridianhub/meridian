package validation

import (
	"context"
	"errors"
	"time"
)

// ErrValidatorRequired is returned when NewCachedValidator is called with a nil Validator.
var ErrValidatorRequired = errors.New("validator is required")

// CachedValidator wraps a DryRunValidator with a caching layer.
// It caches successful validation results by script content hash,
// avoiding redundant validations for unchanged scripts.
//
// Only successful validation results are cached. Failed validations
// are not cached to avoid caching transient failures or scripts
// that might be fixed and re-validated.
type CachedValidator struct {
	validator *DryRunValidator
	cache     *Cache
}

// CachedValidatorConfig configures a CachedValidator.
type CachedValidatorConfig struct {
	// Validator is the underlying DryRunValidator to wrap.
	Validator *DryRunValidator

	// TTL is the time-to-live for cache entries. Default: 1 hour.
	TTL time.Duration

	// MaxSize is the maximum number of cached entries. Default: 1000.
	MaxSize int
}

// DefaultCacheTTL is the default cache entry time-to-live.
const DefaultCacheTTL = time.Hour

// DefaultCacheMaxSize is the default maximum cache size.
const DefaultCacheMaxSize = 1000

// NewCachedValidator creates a new CachedValidator wrapping the given validator.
// Returns ErrValidatorRequired if cfg.Validator is nil.
// If cfg.TTL is 0, DefaultCacheTTL (1 hour) is used.
// If cfg.MaxSize is 0, DefaultCacheMaxSize (1000) is used.
func NewCachedValidator(cfg CachedValidatorConfig) (*CachedValidator, error) {
	if cfg.Validator == nil {
		return nil, ErrValidatorRequired
	}

	ttl := cfg.TTL
	if ttl == 0 {
		ttl = DefaultCacheTTL
	}

	maxSize := cfg.MaxSize
	if maxSize == 0 {
		maxSize = DefaultCacheMaxSize
	}

	return &CachedValidator{
		validator: cfg.Validator,
		cache:     NewCache(ttl, maxSize),
	}, nil
}

// Validate validates a Starlark script, using cached results when available.
//
// If the script has been successfully validated before and the cache entry
// hasn't expired, the cached result is returned immediately without
// re-executing the validation.
//
// Only successful validation results are cached. Failed validations
// always trigger a fresh validation to ensure updated scripts or
// fixed issues are detected.
func (v *CachedValidator) Validate(ctx context.Context, script string) (*ValidationResult, error) {
	// Check cache first
	if result, ok := v.cache.Get(script); ok {
		return result, nil
	}

	// Cache miss - perform actual validation
	result, err := v.validator.Validate(ctx, script)
	if err != nil {
		return nil, err
	}

	// Only cache successful results
	if result.Success {
		v.cache.Set(script, result)
	}

	return result, nil
}

// Start begins background eviction of expired cache entries.
// Call this method once during application startup.
// The background goroutine runs until the context is cancelled.
func (v *CachedValidator) Start(ctx context.Context) {
	v.cache.Start(ctx)
}

// CacheSize returns the current number of entries in the cache.
func (v *CachedValidator) CacheSize() int {
	return v.cache.Size()
}

// ClearCache removes all entries from the cache.
func (v *CachedValidator) ClearCache() {
	v.cache.Clear()
}

// Cache returns the underlying ValidationCache for advanced operations.
// This is primarily useful for testing or exposing cache metrics.
func (v *CachedValidator) Cache() *Cache {
	return v.cache
}

// UnderlyingValidator returns the wrapped DryRunValidator.
// This allows access to the original validator for operations
// that should bypass the cache.
func (v *CachedValidator) UnderlyingValidator() *DryRunValidator {
	return v.validator
}
