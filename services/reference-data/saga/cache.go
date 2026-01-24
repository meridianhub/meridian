// Package saga provides caching infrastructure for saga definitions
// with tenant isolation and TTL-based expiration.
package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/redis/go-redis/v9"
)

// Default Redis cache configuration values.
const (
	// DefaultCacheTTL is the base TTL for saga cache entries (1 hour).
	DefaultCacheTTL = 1 * time.Hour

	// DefaultCacheTTLJitter is the maximum random variation added to TTL.
	// This prevents thundering herd when many entries expire simultaneously.
	DefaultCacheTTLJitter = 5 * time.Minute

	// DefaultCacheKeyPrefix is the default key prefix for saga cache entries.
	DefaultCacheKeyPrefix = "saga"
)

// ErrCacheClientRequired is returned when Redis client is nil.
var ErrCacheClientRequired = errors.New("redis client is required")

// CacheOption configures a Cache.
type CacheOption func(*Cache)

// WithCacheKeyPrefix sets the key prefix for Redis keys.
func WithCacheKeyPrefix(prefix string) CacheOption {
	return func(c *Cache) {
		c.keyPrefix = prefix
	}
}

// WithCacheTTL sets the base TTL and jitter for cache entries.
func WithCacheTTL(baseTTL, jitter time.Duration) CacheOption {
	return func(c *Cache) {
		c.baseTTL = baseTTL
		c.ttlJitter = jitter
	}
}

// Cache provides Redis-based caching for saga definitions.
// It stores JSON-serialized Definition objects with tenant-scoped keys.
//
// Key formats:
//   - By ID: {keyPrefix}:id:{tenantID}:{id}
//   - By name+version: {keyPrefix}:def:{tenantID}:{name}:{version}
//   - Active resolution: {keyPrefix}:active:{tenantID}:{name}
//
// Thread-safety: All methods are safe for concurrent use.
type Cache struct {
	// client is the Redis client for cache operations.
	client redis.Cmdable

	// keyPrefix is the prefix for all Redis keys.
	keyPrefix string

	// baseTTL is the base time-to-live for cache entries.
	baseTTL time.Duration

	// ttlJitter is the maximum random variation added to TTL.
	ttlJitter time.Duration
}

// NewCache creates a new Redis saga cache.
// The client can be *redis.Client, *redis.ClusterClient, or any redis.Cmdable.
func NewCache(client redis.Cmdable, opts ...CacheOption) (*Cache, error) {
	if client == nil {
		return nil, ErrCacheClientRequired
	}

	c := &Cache{
		client:    client,
		keyPrefix: DefaultCacheKeyPrefix,
		baseTTL:   DefaultCacheTTL,
		ttlJitter: DefaultCacheTTLJitter,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// cachedSaga is the JSON-serializable cache representation.
type cachedSaga struct {
	ID                      string     `json:"id"`
	Name                    string     `json:"name"`
	Version                 int        `json:"version"`
	Script                  string     `json:"script"`
	Status                  string     `json:"status"`
	IsSystem                bool       `json:"is_system"`
	PreconditionsExpression string     `json:"preconditions_expression,omitempty"`
	DisplayName             string     `json:"display_name,omitempty"`
	Description             string     `json:"description,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	ActivatedAt             *time.Time `json:"activated_at,omitempty"`
	DeprecatedAt            *time.Time `json:"deprecated_at,omitempty"`
	SuccessorID             *string    `json:"successor_id,omitempty"`
}

// GetByID retrieves a saga definition by ID from the cache.
// Returns nil if the entry is not found or tenant context is missing.
func (c *Cache) GetByID(ctx context.Context, id uuid.UUID) *Definition {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	key := c.idKey(tenantID, id)
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil
	}

	return c.deserialize(data)
}

// Get retrieves a saga definition by name and version from the cache.
// Returns nil if the entry is not found or tenant context is missing.
func (c *Cache) Get(ctx context.Context, name string, version int) *Definition {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	key := c.definitionKey(tenantID, name, version)
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil
	}

	return c.deserialize(data)
}

// GetActive retrieves the active saga for a name from the cache.
// Returns nil if the entry is not found or tenant context is missing.
func (c *Cache) GetActive(ctx context.Context, name string) *Definition {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	key := c.activeKey(tenantID, name)
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil
	}

	return c.deserialize(data)
}

// PutByID stores a saga definition in the cache by ID.
// Does nothing if tenant context is missing.
func (c *Cache) PutByID(ctx context.Context, def *Definition) {
	if def == nil {
		return
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	data := c.serialize(def)
	if data == nil {
		return
	}

	key := c.idKey(tenantID, def.ID)
	ttl := c.jitteredTTL()
	_ = c.client.Set(ctx, key, data, ttl).Err()
}

// Put stores a saga definition in the cache by name and version.
// Does nothing if tenant context is missing.
func (c *Cache) Put(ctx context.Context, def *Definition) {
	if def == nil {
		return
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	data := c.serialize(def)
	if data == nil {
		return
	}

	key := c.definitionKey(tenantID, def.Name, def.Version)
	ttl := c.jitteredTTL()
	_ = c.client.Set(ctx, key, data, ttl).Err()
}

// PutActive stores a saga definition as the active version for its name.
// Does nothing if tenant context is missing.
func (c *Cache) PutActive(ctx context.Context, def *Definition) {
	if def == nil {
		return
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	data := c.serialize(def)
	if data == nil {
		return
	}

	key := c.activeKey(tenantID, def.Name)
	ttl := c.jitteredTTL()
	_ = c.client.Set(ctx, key, data, ttl).Err()
}

// InvalidateByID removes a saga from the cache by ID.
func (c *Cache) InvalidateByID(ctx context.Context, id uuid.UUID) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	key := c.idKey(tenantID, id)
	_ = c.client.Del(ctx, key).Err()
}

// Invalidate removes a specific entry from the cache.
func (c *Cache) Invalidate(ctx context.Context, name string, version int) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	key := c.definitionKey(tenantID, name, version)
	_ = c.client.Del(ctx, key).Err()
}

// InvalidateName removes all versions and the active cache for a saga name.
// Uses pattern matching with SCAN to find and delete all matching keys.
// Uses UNLINK instead of DEL for non-blocking deletion.
func (c *Cache) InvalidateName(ctx context.Context, name string) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	// Delete active cache
	activeKey := c.activeKey(tenantID, name)
	_ = c.client.Del(ctx, activeKey).Err()

	// Pattern to match all versions: {prefix}:def:{tenantID}:{name}:*
	pattern := c.definitionKeyPattern(tenantID, name)

	// Use SCAN to find matching keys (safer than KEYS for production)
	var cursor uint64
	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return // Best effort invalidation
		}

		if len(keys) > 0 {
			_ = c.client.Unlink(ctx, keys...).Err()
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
}

// InvalidateAll removes all saga cache entries for the tenant.
// Uses pattern matching with SCAN to find and delete all matching keys.
func (c *Cache) InvalidateAll(ctx context.Context) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	// Pattern to match all tenant entries: {prefix}:*:{tenantID}:*
	patterns := []string{
		c.tenantKeyPattern(tenantID, "id"),
		c.tenantKeyPattern(tenantID, "def"),
		c.tenantKeyPattern(tenantID, "active"),
	}

	for _, pattern := range patterns {
		var cursor uint64
		for {
			keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
			if err != nil {
				break
			}

			if len(keys) > 0 {
				_ = c.client.Unlink(ctx, keys...).Err()
			}

			cursor = nextCursor
			if cursor == 0 {
				break
			}
		}
	}
}

// idKey generates the Redis key for a saga by ID.
// Format: {keyPrefix}:id:{tenantID}:{id}
func (c *Cache) idKey(tenantID tenant.TenantID, id uuid.UUID) string {
	return fmt.Sprintf("%s:id:%s:%s", c.keyPrefix, tenantID, id)
}

// definitionKey generates the Redis key for a specific saga version.
// Format: {keyPrefix}:def:{tenantID}:{name}:{version}
func (c *Cache) definitionKey(tenantID tenant.TenantID, name string, version int) string {
	return fmt.Sprintf("%s:def:%s:%s:%d", c.keyPrefix, tenantID, name, version)
}

// activeKey generates the Redis key for the active saga resolution.
// Format: {keyPrefix}:active:{tenantID}:{name}
func (c *Cache) activeKey(tenantID tenant.TenantID, name string) string {
	return fmt.Sprintf("%s:active:%s:%s", c.keyPrefix, tenantID, name)
}

// definitionKeyPattern generates a pattern to match all versions of a saga.
// Format: {keyPrefix}:def:{tenantID}:{name}:*
func (c *Cache) definitionKeyPattern(tenantID tenant.TenantID, name string) string {
	return fmt.Sprintf("%s:def:%s:%s:*", c.keyPrefix, tenantID, name)
}

// tenantKeyPattern generates a pattern to match all entries of a type for a tenant.
// Format: {keyPrefix}:{type}:{tenantID}:*
func (c *Cache) tenantKeyPattern(tenantID tenant.TenantID, keyType string) string {
	return fmt.Sprintf("%s:%s:%s:*", c.keyPrefix, keyType, tenantID)
}

// jitteredTTL returns the base TTL plus a random jitter.
func (c *Cache) jitteredTTL() time.Duration {
	if c.ttlJitter == 0 {
		return c.baseTTL
	}

	// Generate random jitter in range [-ttlJitter, +ttlJitter]
	jitterRange := int64(c.ttlJitter) * 2
	jitter := rand.Int64N(jitterRange) - int64(c.ttlJitter)

	return c.baseTTL + time.Duration(jitter)
}

// serialize converts a Definition to JSON bytes.
func (c *Cache) serialize(def *Definition) []byte {
	cached := &cachedSaga{
		ID:                      def.ID.String(),
		Name:                    def.Name,
		Version:                 def.Version,
		Script:                  def.Script,
		Status:                  string(def.Status),
		IsSystem:                def.IsSystem,
		PreconditionsExpression: def.PreconditionsExpression,
		DisplayName:             def.DisplayName,
		Description:             def.Description,
		CreatedAt:               def.CreatedAt,
		UpdatedAt:               def.UpdatedAt,
		ActivatedAt:             def.ActivatedAt,
		DeprecatedAt:            def.DeprecatedAt,
	}

	if def.SuccessorID != nil {
		s := def.SuccessorID.String()
		cached.SuccessorID = &s
	}

	data, err := json.Marshal(cached)
	if err != nil {
		return nil
	}
	return data
}

// deserialize converts JSON bytes to a Definition.
func (c *Cache) deserialize(data []byte) *Definition {
	var cached cachedSaga
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil
	}

	def := &Definition{
		Name:                    cached.Name,
		Version:                 cached.Version,
		Script:                  cached.Script,
		Status:                  Status(cached.Status),
		IsSystem:                cached.IsSystem,
		PreconditionsExpression: cached.PreconditionsExpression,
		DisplayName:             cached.DisplayName,
		Description:             cached.Description,
		CreatedAt:               cached.CreatedAt,
		UpdatedAt:               cached.UpdatedAt,
		ActivatedAt:             cached.ActivatedAt,
		DeprecatedAt:            cached.DeprecatedAt,
	}

	if id, err := uuid.Parse(cached.ID); err == nil {
		def.ID = id
	}

	if cached.SuccessorID != nil {
		if id, err := uuid.Parse(*cached.SuccessorID); err == nil {
			def.SuccessorID = &id
		}
	}

	return def
}
