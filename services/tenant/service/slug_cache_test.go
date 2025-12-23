package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
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

// Integration tests for Service.GetBySlug with cache

func setupServiceWithCache(t *testing.T) (*Service, *redis.Client, *miniredis.Miniredis, func()) {
	t.Helper()

	// Setup PostgreSQL
	db, dbCleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	createAuditOutboxTable(t, db)

	// Setup miniredis
	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	repo := persistence.NewRepository(db)
	slugCache := NewSlugCache(redisClient)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := NewService(repo, nil, nil, slugCache, logger)

	cleanup := func() {
		_ = redisClient.Close()
		mr.Close()
		dbCleanup()
	}

	return svc, redisClient, mr, cleanup
}

func TestService_GetBySlug_CacheHit(t *testing.T) {
	svc, _, mr, cleanup := setupServiceWithCache(t)
	defer cleanup()

	ctx := context.Background()

	// Create a tenant
	req := &pb.InitiateTenantRequest{
		TenantId:        "cache_hit_test",
		DisplayName:     "Cache Hit Test",
		SettlementAsset: "GBP",
		Slug:            "cache-hit",
	}
	_, err := svc.InitiateTenant(ctx, req)
	if err != nil {
		t.Fatalf("InitiateTenant failed: %v", err)
	}

	// Verify cache was pre-populated during tenant creation
	if !mr.Exists("tenant:slug:cache-hit") {
		t.Error("Cache should be populated after tenant creation")
	}

	// First GetBySlug should hit cache (pre-populated)
	result1, err := svc.GetBySlug(ctx, "cache-hit")
	if err != nil {
		t.Fatalf("GetBySlug failed: %v", err)
	}
	if result1.ID.String() != "cache_hit_test" {
		t.Errorf("Expected tenant ID cache_hit_test, got %s", result1.ID.String())
	}

	// Second GetBySlug should also hit cache
	result2, err := svc.GetBySlug(ctx, "cache-hit")
	if err != nil {
		t.Fatalf("GetBySlug failed: %v", err)
	}
	if result2.ID != result1.ID {
		t.Error("Expected same tenant from cache")
	}
}

func TestService_GetBySlug_CacheMiss_PopulatesCache(t *testing.T) {
	svc, redisClient, mr, cleanup := setupServiceWithCache(t)
	defer cleanup()

	ctx := context.Background()

	// Create a tenant
	req := &pb.InitiateTenantRequest{
		TenantId:        "cache_miss_test",
		DisplayName:     "Cache Miss Test",
		SettlementAsset: "USD",
		Slug:            "cache-miss",
	}
	_, err := svc.InitiateTenant(ctx, req)
	if err != nil {
		t.Fatalf("InitiateTenant failed: %v", err)
	}

	// Manually clear cache to simulate cache miss
	err = redisClient.Del(ctx, "tenant:slug:cache-miss").Err()
	if err != nil {
		t.Fatalf("Failed to clear cache: %v", err)
	}

	// GetBySlug should miss cache, hit DB, and populate cache
	result, err := svc.GetBySlug(ctx, "cache-miss")
	if err != nil {
		t.Fatalf("GetBySlug failed: %v", err)
	}
	if result.ID.String() != "cache_miss_test" {
		t.Errorf("Expected tenant ID cache_miss_test, got %s", result.ID.String())
	}

	// Verify cache was populated
	if !mr.Exists("tenant:slug:cache-miss") {
		t.Error("Cache should be populated after DB lookup")
	}

	cachedValue, _ := mr.Get("tenant:slug:cache-miss")
	if cachedValue != "cache_miss_test" {
		t.Errorf("Expected cached value cache_miss_test, got %s", cachedValue)
	}
}

func TestService_GetBySlug_NotFound(t *testing.T) {
	svc, _, _, cleanup := setupServiceWithCache(t)
	defer cleanup()

	ctx := context.Background()

	// Lookup non-existent slug
	result, err := svc.GetBySlug(ctx, "nonexistent-slug")
	if err == nil {
		t.Fatal("Expected error for non-existent slug")
	}
	if result != nil {
		t.Error("Expected nil result for non-existent slug")
	}
	if !errors.Is(err, persistence.ErrTenantNotFound) {
		t.Errorf("Expected ErrTenantNotFound, got %v", err)
	}
}

func TestService_GetBySlug_StaleCache_Invalidates(t *testing.T) {
	svc, redisClient, mr, cleanup := setupServiceWithCache(t)
	defer cleanup()

	ctx := context.Background()

	// Create a tenant
	req := &pb.InitiateTenantRequest{
		TenantId:        "stale_test",
		DisplayName:     "Stale Test",
		SettlementAsset: "GBP",
		Slug:            "stale-slug",
	}
	_, err := svc.InitiateTenant(ctx, req)
	if err != nil {
		t.Fatalf("InitiateTenant failed: %v", err)
	}

	// Inject stale cache entry (wrong tenant ID)
	err = redisClient.Set(ctx, "tenant:slug:stale-slug", "wrong_tenant_id", 0).Err()
	if err != nil {
		t.Fatalf("Failed to set stale cache: %v", err)
	}

	// GetBySlug should detect stale cache, invalidate, and return correct tenant
	result, err := svc.GetBySlug(ctx, "stale-slug")
	if err != nil {
		t.Fatalf("GetBySlug failed: %v", err)
	}
	if result.ID.String() != "stale_test" {
		t.Errorf("Expected tenant ID stale_test, got %s", result.ID.String())
	}

	// Verify cache was repopulated with correct value
	cachedValue, _ := mr.Get("tenant:slug:stale-slug")
	if cachedValue != "stale_test" {
		t.Errorf("Expected cache to be repopulated with stale_test, got %s", cachedValue)
	}
}

func TestService_UpdateTenantStatus_Deprovisioned_InvalidatesCache(t *testing.T) {
	svc, _, mr, cleanup := setupServiceWithCache(t)
	defer cleanup()

	ctx := context.Background()

	// Create a tenant
	req := &pb.InitiateTenantRequest{
		TenantId:        "deprovision_test",
		DisplayName:     "Deprovision Test",
		SettlementAsset: "GBP",
		Slug:            "deprovision-slug",
	}
	_, err := svc.InitiateTenant(ctx, req)
	if err != nil {
		t.Fatalf("InitiateTenant failed: %v", err)
	}

	// Verify cache is populated
	if !mr.Exists("tenant:slug:deprovision-slug") {
		t.Fatal("Cache should be populated after tenant creation")
	}

	// Update status to DEPROVISIONED
	updateReq := &pb.UpdateTenantStatusRequest{
		TenantId: "deprovision_test",
		Status:   pb.TenantStatus_TENANT_STATUS_DEPROVISIONED,
	}
	_, err = svc.UpdateTenantStatus(ctx, updateReq)
	if err != nil {
		t.Fatalf("UpdateTenantStatus failed: %v", err)
	}

	// Verify cache was invalidated
	if mr.Exists("tenant:slug:deprovision-slug") {
		t.Error("Cache should be invalidated after deprovisioning")
	}
}

func TestService_GetBySlug_CacheDisabled(t *testing.T) {
	// Setup without cache
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	defer cleanup()
	createAuditOutboxTable(t, db)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := NewService(repo, nil, nil, nil, logger) // nil slugCache

	ctx := context.Background()

	// Create a tenant
	req := &pb.InitiateTenantRequest{
		TenantId:        "no_cache_test",
		DisplayName:     "No Cache Test",
		SettlementAsset: "USD",
		Slug:            "no-cache",
	}
	_, err := svc.InitiateTenant(ctx, req)
	if err != nil {
		t.Fatalf("InitiateTenant failed: %v", err)
	}

	// GetBySlug should work without cache
	result, err := svc.GetBySlug(ctx, "no-cache")
	if err != nil {
		t.Fatalf("GetBySlug failed: %v", err)
	}
	if result.ID.String() != "no_cache_test" {
		t.Errorf("Expected tenant ID no_cache_test, got %s", result.ID.String())
	}
}

func TestService_InitiateTenant_CachePopulationFailure_NonFatal(t *testing.T) {
	// Setup with cache
	svc, _, mr, cleanup := setupServiceWithCache(t)
	defer cleanup()

	ctx := context.Background()

	// Close Redis to simulate cache failure
	mr.Close()

	// Create tenant - should succeed despite cache failure
	req := &pb.InitiateTenantRequest{
		TenantId:        "cache_fail_test",
		DisplayName:     "Cache Fail Test",
		SettlementAsset: "GBP",
		Slug:            "cache-fail",
	}
	resp, err := svc.InitiateTenant(ctx, req)
	if err != nil {
		t.Fatalf("InitiateTenant should succeed even if cache fails: %v", err)
	}

	// Verify tenant was created successfully
	if resp.Tenant == nil {
		t.Fatal("Expected tenant to be created")
	}
	if resp.Tenant.TenantId != "cache_fail_test" {
		t.Errorf("Expected tenant ID cache_fail_test, got %s", resp.Tenant.TenantId)
	}
	if resp.Tenant.Slug != "cache-fail" {
		t.Errorf("Expected slug cache-fail, got %s", resp.Tenant.Slug)
	}

	// Note: Cache population failure would be logged but we can't easily verify
	// log output in this test. The important part is that the request succeeded.
}

func TestService_InitiateTenant_EmptySlug_SkipsCachePopulation(t *testing.T) {
	svc, _, mr, cleanup := setupServiceWithCache(t)
	defer cleanup()

	ctx := context.Background()

	// Create tenant without slug
	req := &pb.InitiateTenantRequest{
		TenantId:        "empty_slug_test",
		DisplayName:     "Empty Slug Test",
		SettlementAsset: "USD",
		Slug:            "", // Empty slug
	}
	resp, err := svc.InitiateTenant(ctx, req)
	if err != nil {
		t.Fatalf("InitiateTenant failed: %v", err)
	}

	// Verify tenant was created
	if resp.Tenant == nil {
		t.Fatal("Expected tenant to be created")
	}

	// Verify no cache entry was created (can't cache empty slug)
	// Check that there are no keys matching the tenant:slug: pattern
	keys := mr.Keys()
	for _, key := range keys {
		if key == "tenant:slug:" || key == "tenant:slug:empty_slug_test" {
			t.Errorf("Unexpected cache entry for empty slug: %s", key)
		}
	}
}
