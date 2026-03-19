// Package cache provides caching infrastructure for reference data definitions
// with tenant isolation and TTL-based expiration.
package cache

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"golang.org/x/sync/singleflight"

	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Account type cache configuration defaults.
const (
	// DefaultAccountTypeCacheSize is the maximum number of entries per tenant cache.
	DefaultAccountTypeCacheSize = 10000

	// DefaultAccountTypeTTL is the base TTL for regular (non-system) definitions.
	DefaultAccountTypeTTL = 5 * time.Minute

	// DefaultAccountTypeTTLJitter is the maximum random jitter added to TTL.
	DefaultAccountTypeTTLJitter = 30 * time.Second

	// SystemBlueprintTTL is the TTL for system blueprint definitions (IsSystem=true).
	SystemBlueprintTTL = 24 * time.Hour
)

// AccountTypeKey uniquely identifies an account type within a tenant's cache.
type AccountTypeKey struct {
	Code    string
	Version int
}

// CachedAccountType contains the account type definition and precompiled programs.
type CachedAccountType struct {
	// Definition is the cached account type definition.
	Definition *accounttype.Definition

	// ValidationProgram is the precompiled CEL program for validation.
	// May be nil if no validation expression is defined.
	ValidationProgram cel.Program

	// BucketingProgram is the precompiled CEL program for bucketing.
	// May be nil if no bucketing expression is defined.
	BucketingProgram cel.Program

	// EligibilityProgram is the precompiled CEL program for eligibility.
	// May be nil if no eligibility expression is defined.
	EligibilityProgram cel.Program

	// CompiledSchema is the compiled JSON Schema for attribute validation.
	// May be nil if no attribute schema is defined.
	CompiledSchema *jsonschema.Schema

	// cachedAt records when this entry was added to the cache.
	cachedAt time.Time

	// expiresAt is the precomputed expiration time.
	expiresAt time.Time

	// isSystem records whether this is a system blueprint for background refresh.
	isSystem bool
}

// CachedAt returns when this entry was cached.
func (c *CachedAccountType) CachedAt() time.Time {
	return c.cachedAt
}

// ExpiresAt returns when this entry will expire.
func (c *CachedAccountType) ExpiresAt() time.Time {
	return c.expiresAt
}

// AccountTypeLoader loads account type definitions from an external source (e.g., gRPC).
type AccountTypeLoader interface {
	// LoadAccountType loads a single account type by code, returning the latest active version.
	LoadAccountType(ctx context.Context, code string) (*accounttype.Definition, error)

	// ListActiveAccountTypes lists all active account type definitions for the tenant in context.
	ListActiveAccountTypes(ctx context.Context) ([]*accounttype.Definition, error)
}

// AccountTypeCacheOption configures a LocalAccountTypeCache.
type AccountTypeCacheOption func(*LocalAccountTypeCache)

// WithAccountTypeCacheSize sets the maximum number of entries per tenant cache.
func WithAccountTypeCacheSize(size int) AccountTypeCacheOption {
	return func(c *LocalAccountTypeCache) {
		c.cacheSize = size
	}
}

// WithAccountTypeTTL sets the base TTL and jitter for regular definitions.
func WithAccountTypeTTL(baseTTL, jitter time.Duration) AccountTypeCacheOption {
	return func(c *LocalAccountTypeCache) {
		c.baseTTL = baseTTL
		c.ttlJitter = jitter
	}
}

// LocalAccountTypeCache provides tenant-isolated caching for account type definitions
// with precompiled CEL programs and JSON Schema.
//
// Thread-safety: All methods are safe for concurrent use.
type LocalAccountTypeCache struct {
	mu           sync.RWMutex
	tenantCaches map[tenant.TenantID]*lru.Cache[AccountTypeKey, *CachedAccountType]
	loader       AccountTypeLoader
	celCompiler  AccountTypeCELCompiler
	cacheSize    int
	baseTTL      time.Duration
	ttlJitter    time.Duration
	sfGroup      singleflight.Group
}

// AccountTypeCELCompiler compiles CEL programs for account type definitions.
type AccountTypeCELCompiler interface {
	CompileValidation(expression string) (cel.Program, error)
	CompileBucketKey(expression string) (cel.Program, error)
	CompileEligibility(expression string) (cel.Program, error)
}

// NewLocalAccountTypeCache creates a new tenant-isolated account type cache.
func NewLocalAccountTypeCache(
	loader AccountTypeLoader,
	celCompiler AccountTypeCELCompiler,
	opts ...AccountTypeCacheOption,
) *LocalAccountTypeCache {
	c := &LocalAccountTypeCache{
		tenantCaches: make(map[tenant.TenantID]*lru.Cache[AccountTypeKey, *CachedAccountType]),
		loader:       loader,
		celCompiler:  celCompiler,
		cacheSize:    DefaultAccountTypeCacheSize,
		baseTTL:      DefaultAccountTypeTTL,
		ttlJitter:    DefaultAccountTypeTTLJitter,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Get retrieves a cached account type for the tenant in context.
// Returns nil if not found, expired, or tenant context is missing.
// For expired system blueprints, triggers a background refresh and returns the stale entry.
func (c *LocalAccountTypeCache) Get(ctx context.Context, code string, version int) *CachedAccountType {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	cache := c.getTenantCache(tenantID)
	if cache == nil {
		return nil
	}

	key := AccountTypeKey{Code: code, Version: version}
	entry, ok := cache.Get(key)
	if !ok {
		return nil
	}

	if time.Now().After(entry.expiresAt) {
		if entry.isSystem {
			// System blueprints: trigger background refresh, return stale entry.
			// Intentionally detached from request context to survive caller cancellation.
			bgCtx := context.Background() //nolint:contextcheck // detached goroutine for background refresh
			go c.backgroundRefresh(bgCtx, tenantID, code)
			return entry
		}
		// Regular entries: remove expired
		cache.Remove(key)
		return nil
	}

	return entry
}

// Put stores an account type in the cache for the tenant in context.
func (c *LocalAccountTypeCache) Put(ctx context.Context, entry *CachedAccountType) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	cache := c.getOrCreateTenantCache(tenantID)
	if cache == nil {
		return
	}

	key := AccountTypeKey{Code: entry.Definition.Code, Version: entry.Definition.Version}
	now := time.Now()
	entry.cachedAt = now
	entry.isSystem = entry.Definition.IsSystem

	if entry.Definition.IsSystem {
		entry.expiresAt = now.Add(SystemBlueprintTTL)
	} else {
		entry.expiresAt = now.Add(c.jitteredTTL())
	}

	cache.Add(key, entry)
}

// GetOrLoad retrieves a cached account type or loads it on cache miss.
// Concurrent requests for the same key share a single load call via singleflight.
func (c *LocalAccountTypeCache) GetOrLoad(
	ctx context.Context,
	tenantID tenant.TenantID,
	code string,
) (*CachedAccountType, error) {
	tenantCtx := tenant.WithTenant(ctx, tenantID)

	// Fast path: check cache (use version 0 to indicate "latest")
	if cached := c.getByCode(tenantCtx, code); cached != nil {
		return cached, nil
	}

	// Slow path: singleflight deduplication
	sfKey := fmt.Sprintf("%s:%s", tenantID, code)
	result, err, _ := c.sfGroup.Do(sfKey, func() (interface{}, error) {
		// Double-check cache
		if cached := c.getByCode(tenantCtx, code); cached != nil {
			return cached, nil
		}

		// Load from source
		def, err := c.loader.LoadAccountType(tenantCtx, code)
		if err != nil {
			return nil, err
		}

		// Compile programs
		entry, err := c.compileCachedEntry(def)
		if err != nil {
			return nil, fmt.Errorf("compile account type %s: %w", code, err)
		}

		// Store in cache
		c.Put(tenantCtx, entry)
		return entry, nil
	})

	if err != nil {
		return nil, err
	}

	cached, ok := result.(*CachedAccountType)
	if !ok {
		return nil, ErrUnexpectedResultType
	}

	return cached, nil
}

// PrefetchAccountTypes loads all active account types for the given tenants into the cache.
func (c *LocalAccountTypeCache) PrefetchAccountTypes(ctx context.Context, tenantIDs []tenant.TenantID) error {
	for _, tenantID := range tenantIDs {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("prefetch cancelled: %w", err)
		}

		tenantCtx := tenant.WithTenant(ctx, tenantID)
		defs, err := c.loader.ListActiveAccountTypes(tenantCtx)
		if err != nil {
			return fmt.Errorf("prefetch failed for tenant %s: %w", tenantID, err)
		}

		for _, def := range defs {
			entry, err := c.compileCachedEntry(def)
			if err != nil {
				return fmt.Errorf("compile account type %s for tenant %s: %w", def.Code, tenantID, err)
			}
			c.Put(tenantCtx, entry)
		}
	}
	return nil
}

// Invalidate removes a specific entry from the cache for the tenant in context.
func (c *LocalAccountTypeCache) Invalidate(ctx context.Context, code string, version int) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	cache := c.getTenantCache(tenantID)
	if cache == nil {
		return
	}

	cache.Remove(AccountTypeKey{Code: code, Version: version})
}

// InvalidateAll removes all entries for the tenant in context.
func (c *LocalAccountTypeCache) InvalidateAll(ctx context.Context) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tenantCaches, tenantID)
}

// Stats returns cache statistics for the tenant in context.
func (c *LocalAccountTypeCache) Stats(ctx context.Context) (size int, capacity int) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return 0, 0
	}

	cache := c.getTenantCache(tenantID)
	if cache == nil {
		return 0, 0
	}

	return cache.Len(), c.cacheSize
}

// getByCode looks up a cache entry by code across all versions for the tenant in context.
// Returns the first matching entry found (there should typically be one active version).
func (c *LocalAccountTypeCache) getByCode(ctx context.Context, code string) *CachedAccountType {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	cache := c.getTenantCache(tenantID)
	if cache == nil {
		return nil
	}

	for _, key := range cache.Keys() {
		if key.Code == code {
			entry, ok := cache.Get(key)
			if !ok {
				continue
			}
			if time.Now().After(entry.expiresAt) {
				if entry.isSystem {
					bgCtx := context.Background() //nolint:contextcheck // detached goroutine for background refresh
					go c.backgroundRefresh(bgCtx, tenantID, code)
					return entry
				}
				cache.Remove(key)
				continue
			}
			return entry
		}
	}

	return nil
}

// backgroundRefresh asynchronously reloads a system blueprint entry.
// Uses singleflight to prevent thundering herd when multiple concurrent
// Get calls see the same expired system blueprint.
func (c *LocalAccountTypeCache) backgroundRefresh(ctx context.Context, tenantID tenant.TenantID, code string) {
	sfKey := fmt.Sprintf("bg:%s:%s", tenantID, code)
	c.sfGroup.Do(sfKey, func() (interface{}, error) { //nolint:errcheck // fire-and-forget refresh
		ctx = tenant.WithTenant(ctx, tenantID)

		def, err := c.loader.LoadAccountType(ctx, code)
		if err != nil {
			return nil, err // Stale entry remains accessible
		}

		entry, err := c.compileCachedEntry(def)
		if err != nil {
			return nil, err
		}

		c.Put(ctx, entry)
		return entry, nil
	})
}

// compileCachedEntry compiles CEL programs and JSON Schema for a definition.
func (c *LocalAccountTypeCache) compileCachedEntry(def *accounttype.Definition) (*CachedAccountType, error) {
	entry := &CachedAccountType{
		Definition: def,
	}

	if c.celCompiler != nil {
		if def.ValidationCEL != "" {
			prg, err := c.celCompiler.CompileValidation(def.ValidationCEL)
			if err != nil {
				return nil, fmt.Errorf("validation CEL: %w", err)
			}
			entry.ValidationProgram = prg
		}
		if def.BucketingCEL != "" {
			prg, err := c.celCompiler.CompileBucketKey(def.BucketingCEL)
			if err != nil {
				return nil, fmt.Errorf("bucketing CEL: %w", err)
			}
			entry.BucketingProgram = prg
		}
		if def.EligibilityCEL != "" {
			prg, err := c.celCompiler.CompileEligibility(def.EligibilityCEL)
			if err != nil {
				return nil, fmt.Errorf("eligibility CEL: %w", err)
			}
			entry.EligibilityProgram = prg
		}
	}

	if len(def.AttributeSchema) > 0 {
		schema, err := compileJSONSchema(def.AttributeSchema)
		if err != nil {
			return nil, fmt.Errorf("attribute schema: %w", err)
		}
		entry.CompiledSchema = schema
	}

	return entry, nil
}

// compileJSONSchema compiles raw JSON Schema bytes into a validated schema.
func compileJSONSchema(raw []byte) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", strings.NewReader(string(raw))); err != nil {
		return nil, err
	}
	return compiler.Compile("schema.json")
}

// getTenantCache returns the cache for a tenant, or nil if not found.
func (c *LocalAccountTypeCache) getTenantCache(tenantID tenant.TenantID) *lru.Cache[AccountTypeKey, *CachedAccountType] {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tenantCaches[tenantID]
}

// getOrCreateTenantCache returns the cache for a tenant, creating it if needed.
func (c *LocalAccountTypeCache) getOrCreateTenantCache(tenantID tenant.TenantID) *lru.Cache[AccountTypeKey, *CachedAccountType] {
	c.mu.RLock()
	cache, ok := c.tenantCaches[tenantID]
	c.mu.RUnlock()

	if ok {
		return cache
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if cache, ok = c.tenantCaches[tenantID]; ok {
		return cache
	}

	newCache, err := lru.New[AccountTypeKey, *CachedAccountType](c.cacheSize)
	if err != nil {
		slog.Error("failed to create tenant account type cache", "tenant_id", tenantID, "error", err)
		return nil
	}

	c.tenantCaches[tenantID] = newCache
	return newCache
}

// jitteredTTL returns the base TTL plus a random jitter.
func (c *LocalAccountTypeCache) jitteredTTL() time.Duration {
	if c.ttlJitter <= 0 {
		return c.baseTTL
	}
	jitterRange := int64(c.ttlJitter) * 2
	jitter := rand.Int64N(jitterRange) - int64(c.ttlJitter)
	return c.baseTTL + time.Duration(jitter)
}
