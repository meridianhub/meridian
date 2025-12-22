package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/redis/go-redis/v9"
)

func setupSlugCache(t *testing.T) (*SlugCache, *miniredis.Miniredis, func()) {
	t.Helper()

	// Start miniredis server
	mr := miniredis.RunT(t)

	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	cache := NewSlugCache(client)

	cleanup := func() {
		_ = client.Close()
		mr.Close()
	}

	return cache, mr, cleanup
}

func TestSlugCache_Get_Hit(t *testing.T) {
	cache, mr, cleanup := setupSlugCache(t)
	defer cleanup()

	ctx := context.Background()
	slug := "acme-corp"
	expectedTenantID := "tenant_123"

	// Seed Redis with a value
	mr.Set("tenant:slug:acme-corp", expectedTenantID)

	// Get should return the seeded value
	result, err := cache.Get(ctx, slug)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if result.String() != expectedTenantID {
		t.Errorf("Expected tenant ID %s, got %s", expectedTenantID, result.String())
	}
}

func TestSlugCache_Get_Miss(t *testing.T) {
	cache, _, cleanup := setupSlugCache(t)
	defer cleanup()

	ctx := context.Background()
	slug := "non-existent-slug"

	// Get should return empty string for cache miss
	result, err := cache.Get(ctx, slug)
	if err != nil {
		t.Fatalf("Get should not return error on cache miss, got: %v", err)
	}

	if !result.IsEmpty() {
		t.Errorf("Expected empty TenantID for cache miss, got %s", result.String())
	}
}

func TestSlugCache_SetAndGet(t *testing.T) {
	cache, _, cleanup := setupSlugCache(t)
	defer cleanup()

	ctx := context.Background()
	slug := "test-company"
	tenantID := tenant.MustNewTenantID("tenant_456")

	// Set the value
	err := cache.Set(ctx, slug, tenantID)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get should return what we just set
	result, err := cache.Get(ctx, slug)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if result != tenantID {
		t.Errorf("Expected tenant ID %s, got %s", tenantID.String(), result.String())
	}
}

func TestSlugCache_Invalidate(t *testing.T) {
	cache, mr, cleanup := setupSlugCache(t)
	defer cleanup()

	ctx := context.Background()
	slug := "old-slug"
	tenantID := tenant.MustNewTenantID("tenant_789")

	// Set a value
	err := cache.Set(ctx, slug, tenantID)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify it exists
	redisKey := "tenant:slug:old-slug"
	if !mr.Exists(redisKey) {
		t.Fatal("Expected key to exist before invalidation")
	}

	// Invalidate
	err = cache.Invalidate(ctx, slug)
	if err != nil {
		t.Fatalf("Invalidate failed: %v", err)
	}

	// Verify it's gone
	if mr.Exists(redisKey) {
		t.Error("Expected key to be deleted after invalidation")
	}

	// Get should return empty after invalidation
	result, err := cache.Get(ctx, slug)
	if err != nil {
		t.Fatalf("Get failed after invalidation: %v", err)
	}

	if !result.IsEmpty() {
		t.Errorf("Expected empty TenantID after invalidation, got %s", result.String())
	}
}

func TestSlugCache_TTL(t *testing.T) {
	cache, mr, cleanup := setupSlugCache(t)
	defer cleanup()

	ctx := context.Background()
	slug := "temporary-slug"
	tenantID := tenant.MustNewTenantID("tenant_ttl")

	// Set the value
	err := cache.Set(ctx, slug, tenantID)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify TTL is set (should be 5 minutes)
	redisKey := "tenant:slug:temporary-slug"
	ttl := mr.TTL(redisKey)
	expectedTTL := 5 * time.Minute

	// Allow 1 second tolerance for test execution time
	if ttl < expectedTTL-time.Second || ttl > expectedTTL {
		t.Errorf("Expected TTL around %v, got %v", expectedTTL, ttl)
	}

	// Fast-forward time beyond TTL
	mr.FastForward(6 * time.Minute)

	// Key should be expired
	if mr.Exists(redisKey) {
		t.Error("Expected key to be expired after TTL")
	}

	// Get should return empty after expiration
	result, err := cache.Get(ctx, slug)
	if err != nil {
		t.Fatalf("Get failed after expiration: %v", err)
	}

	if !result.IsEmpty() {
		t.Errorf("Expected empty TenantID after expiration, got %s", result.String())
	}
}

func TestSlugCache_RedisError(t *testing.T) {
	cache, mr, cleanup := setupSlugCache(t)
	defer cleanup()

	ctx := context.Background()

	// Close the miniredis server to simulate connection failure
	mr.Close()

	// Get should return an error (not a cache miss)
	_, err := cache.Get(ctx, "any-slug")
	if err == nil {
		t.Fatal("Expected error when Redis is unavailable, got nil")
	}

	// Verify it's not treated as a simple cache miss (redis.Nil)
	if errors.Is(err, redis.Nil) {
		t.Error("Expected non-Nil error for Redis connection failure")
	}

	// Set should also return an error
	tenantID := tenant.MustNewTenantID("tenant_error")
	err = cache.Set(ctx, "any-slug", tenantID)
	if err == nil {
		t.Fatal("Expected error when Redis is unavailable for Set, got nil")
	}

	// Invalidate should also return an error
	err = cache.Invalidate(ctx, "any-slug")
	if err == nil {
		t.Fatal("Expected error when Redis is unavailable for Invalidate, got nil")
	}
}

func TestSlugCache_RedisKeyFormat(t *testing.T) {
	cache, mr, cleanup := setupSlugCache(t)
	defer cleanup()

	ctx := context.Background()
	slug := "test-format"
	tenantID := tenant.MustNewTenantID("tenant_format")

	// Set a value
	err := cache.Set(ctx, slug, tenantID)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify the key format in Redis
	expectedKey := "tenant:slug:test-format"
	if !mr.Exists(expectedKey) {
		t.Errorf("Expected Redis key %s to exist", expectedKey)
	}

	// Verify the value is stored correctly
	storedValue, _ := mr.Get(expectedKey)
	if storedValue != tenantID.String() {
		t.Errorf("Expected stored value %s, got %s", tenantID.String(), storedValue)
	}
}

func TestSlugCache_MultipleOperations(t *testing.T) {
	cache, _, cleanup := setupSlugCache(t)
	defer cleanup()

	ctx := context.Background()

	// Set multiple slug-tenant mappings
	mappings := map[string]tenant.TenantID{
		"slug-one":   tenant.MustNewTenantID("tenant_1"),
		"slug-two":   tenant.MustNewTenantID("tenant_2"),
		"slug-three": tenant.MustNewTenantID("tenant_3"),
	}

	// Set all mappings
	for slug, tenantID := range mappings {
		if err := cache.Set(ctx, slug, tenantID); err != nil {
			t.Fatalf("Set failed for %s: %v", slug, err)
		}
	}

	// Verify all can be retrieved
	for slug, expectedTenantID := range mappings {
		result, err := cache.Get(ctx, slug)
		if err != nil {
			t.Fatalf("Get failed for %s: %v", slug, err)
		}
		if result != expectedTenantID {
			t.Errorf("For slug %s: expected %s, got %s", slug, expectedTenantID.String(), result.String())
		}
	}

	// Invalidate one
	if err := cache.Invalidate(ctx, "slug-two"); err != nil {
		t.Fatalf("Invalidate failed: %v", err)
	}

	// Verify the invalidated one is gone
	result, err := cache.Get(ctx, "slug-two")
	if err != nil {
		t.Fatalf("Get failed after invalidation: %v", err)
	}
	if !result.IsEmpty() {
		t.Errorf("Expected empty after invalidation, got %s", result.String())
	}

	// Verify the others still exist
	for slug, expectedTenantID := range mappings {
		if slug == "slug-two" {
			continue
		}
		result, err := cache.Get(ctx, slug)
		if err != nil {
			t.Fatalf("Get failed for %s: %v", slug, err)
		}
		if result != expectedTenantID {
			t.Errorf("For slug %s: expected %s, got %s", slug, expectedTenantID.String(), result.String())
		}
	}
}
