package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/redis/go-redis/v9"
)

// SlugCache provides Redis-backed caching for slug-to-TenantID mappings.
// It supports TTL-based expiration and explicit invalidation.
type SlugCache struct {
	client *redis.Client
	ttl    time.Duration
}

// NewSlugCache creates a new Redis-backed slug cache with a default 5-minute TTL.
func NewSlugCache(client *redis.Client) *SlugCache {
	return &SlugCache{
		client: client,
		ttl:    5 * time.Minute,
	}
}

// Get retrieves a TenantID for the given slug from Redis.
// Returns an empty TenantID if the key doesn't exist (cache miss).
// Propagates other Redis errors.
func (c *SlugCache) Get(ctx context.Context, slug string) (tenant.TenantID, error) {
	key := c.redisKey(slug)

	result, err := c.client.Get(ctx, key).Result()
	if err != nil {
		// Cache miss is not an error - return empty string
		if errors.Is(err, redis.Nil) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get slug from cache: %w", err)
	}

	return tenant.TenantID(result), nil
}

// Set stores a slug-to-TenantID mapping in Redis with the configured TTL.
func (c *SlugCache) Set(ctx context.Context, slug string, tenantID tenant.TenantID) error {
	key := c.redisKey(slug)

	err := c.client.Set(ctx, key, tenantID.String(), c.ttl).Err()
	if err != nil {
		return fmt.Errorf("failed to set slug in cache: %w", err)
	}

	return nil
}

// Invalidate removes a slug from the cache.
// This should be called when a tenant's slug is updated.
func (c *SlugCache) Invalidate(ctx context.Context, slug string) error {
	key := c.redisKey(slug)

	err := c.client.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("failed to invalidate slug from cache: %w", err)
	}

	return nil
}

// redisKey generates the Redis key for a given slug.
func (c *SlugCache) redisKey(slug string) string {
	return fmt.Sprintf("tenant:slug:%s", slug)
}
