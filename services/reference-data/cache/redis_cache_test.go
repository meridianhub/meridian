package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// setupMiniRedis creates a miniredis server and returns a connected client.
func setupMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	return mr, client
}

// newTestDefinitionWithDetails creates a test InstrumentDefinition with additional fields.
func newTestDefinitionWithDetails(code string, version int) *registry.InstrumentDefinition {
	return &registry.InstrumentDefinition{
		ID:                       uuid.New(),
		Code:                     code,
		Version:                  version,
		Dimension:                registry.DimensionMonetary,
		Precision:                2,
		Status:                   registry.StatusActive,
		ValidationExpression:     "amount > 0",
		FungibilityKeyExpression: "instrument_code",
		DisplayName:              code + " Display",
		Description:              "Test instrument " + code,
		CreatedAt:                time.Now(),
	}
}

func TestRedisL2Cache_NewRedisL2Cache(t *testing.T) {
	t.Run("success with client", func(t *testing.T) {
		_, client := setupMiniRedis(t)
		defer client.Close()

		cache, err := NewRedisL2Cache(client)
		require.NoError(t, err)
		assert.NotNil(t, cache)
	})

	t.Run("error when client is nil", func(t *testing.T) {
		cache, err := NewRedisL2Cache(nil)
		require.ErrorIs(t, err, ErrRedisClientRequired)
		assert.Nil(t, cache)
	})

	t.Run("applies options", func(t *testing.T) {
		_, client := setupMiniRedis(t)
		defer client.Close()

		cache, err := NewRedisL2Cache(client,
			WithRedisKeyPrefix("custom"),
			WithRedisL2TTL(2*time.Hour, 10*time.Minute),
		)
		require.NoError(t, err)
		assert.Equal(t, "custom", cache.keyPrefix)
		assert.Equal(t, 2*time.Hour, cache.baseTTL)
		assert.Equal(t, 10*time.Minute, cache.ttlJitter)
	})
}

func TestRedisL2Cache_Get_Hit(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")
	def := newTestDefinitionWithDetails("USD", 1)

	// Put and Get
	cache.Put(ctx, "USD", 1, def)
	result := cache.Get(ctx, "USD", 1)

	require.NotNil(t, result)
	assert.Equal(t, def.Code, result.Code)
	assert.Equal(t, def.Version, result.Version)
	assert.Equal(t, def.Dimension, result.Dimension)
	assert.Equal(t, def.Precision, result.Precision)
	assert.Equal(t, def.Status, result.Status)
	assert.Equal(t, def.ValidationExpression, result.ValidationExpression)
	assert.Equal(t, def.FungibilityKeyExpression, result.FungibilityKeyExpression)
	assert.Equal(t, def.DisplayName, result.DisplayName)
	assert.Equal(t, def.Description, result.Description)
	assert.Equal(t, def.ID, result.ID)
}

func TestRedisL2Cache_Get_Miss(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")

	// Get without Put should return nil
	result := cache.Get(ctx, "USD", 1)
	assert.Nil(t, result)
}

func TestRedisL2Cache_Get_MissingTenant(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := context.Background() // No tenant

	// Should return nil when tenant context is missing
	result := cache.Get(ctx, "USD", 1)
	assert.Nil(t, result)
}

func TestRedisL2Cache_Put_MissingTenant(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := context.Background() // No tenant
	def := newTestDefinition("USD", 1)

	// Put should be a no-op when tenant context is missing
	cache.Put(ctx, "USD", 1, def)

	// Verify nothing was cached by trying with a valid tenant
	validCtx := newTestContext("tenant1")
	result := cache.Get(validCtx, "USD", 1)
	assert.Nil(t, result)
}

func TestRedisL2Cache_Put_NilDefinition(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")

	// Put nil should be a no-op
	cache.Put(ctx, "USD", 1, nil)

	result := cache.Get(ctx, "USD", 1)
	assert.Nil(t, result)
}

func TestRedisL2Cache_TenantIsolation(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx1 := newTestContext("tenant1")
	ctx2 := newTestContext("tenant2")

	// Put entry for tenant1
	def1 := newTestDefinition("USD", 1)
	cache.Put(ctx1, "USD", 1, def1)

	// Put different entry for tenant2
	def2 := newTestDefinition("EUR", 1)
	cache.Put(ctx2, "EUR", 1, def2)

	// tenant1 should only see USD
	result1USD := cache.Get(ctx1, "USD", 1)
	require.NotNil(t, result1USD)
	assert.Equal(t, "USD", result1USD.Code)

	result1EUR := cache.Get(ctx1, "EUR", 1)
	assert.Nil(t, result1EUR, "tenant1 should not see tenant2's EUR")

	// tenant2 should only see EUR
	result2EUR := cache.Get(ctx2, "EUR", 1)
	require.NotNil(t, result2EUR)
	assert.Equal(t, "EUR", result2EUR.Code)

	result2USD := cache.Get(ctx2, "USD", 1)
	assert.Nil(t, result2USD, "tenant2 should not see tenant1's USD")
}

func TestRedisL2Cache_TTL(t *testing.T) {
	mr, client := setupMiniRedis(t)
	defer client.Close()

	// Use short TTL for testing
	cache, err := NewRedisL2Cache(client,
		WithRedisL2TTL(100*time.Millisecond, 0), // No jitter
	)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")
	def := newTestDefinition("USD", 1)

	// Put entry
	cache.Put(ctx, "USD", 1, def)

	// Should be retrievable immediately
	result := cache.Get(ctx, "USD", 1)
	require.NotNil(t, result)

	// Fast-forward time in miniredis
	mr.FastForward(200 * time.Millisecond)

	// Should be expired now
	result = cache.Get(ctx, "USD", 1)
	assert.Nil(t, result, "entry should be expired")
}

func TestRedisL2Cache_Invalidate(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")
	def := newTestDefinition("USD", 1)

	// Put and verify
	cache.Put(ctx, "USD", 1, def)
	result := cache.Get(ctx, "USD", 1)
	require.NotNil(t, result)

	// Invalidate
	cache.Invalidate(ctx, "USD", 1)

	// Should be gone
	result = cache.Get(ctx, "USD", 1)
	assert.Nil(t, result)
}

func TestRedisL2Cache_Invalidate_MissingTenant(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := context.Background() // No tenant
	validCtx := newTestContext("tenant1")

	// Put something with valid tenant
	def := newTestDefinition("USD", 1)
	cache.Put(validCtx, "USD", 1, def)

	// Invalidate without tenant should be no-op
	cache.Invalidate(ctx, "USD", 1)

	// Original entry should still exist
	result := cache.Get(validCtx, "USD", 1)
	require.NotNil(t, result)
}

func TestRedisL2Cache_InvalidateCode(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")

	// Put multiple versions of the same code
	for i := 1; i <= 3; i++ {
		cache.Put(ctx, "USD", i, newTestDefinition("USD", i))
	}
	// Put a different code
	cache.Put(ctx, "EUR", 1, newTestDefinition("EUR", 1))

	// Verify all are cached
	for i := 1; i <= 3; i++ {
		assert.NotNil(t, cache.Get(ctx, "USD", i))
	}
	assert.NotNil(t, cache.Get(ctx, "EUR", 1))

	// Invalidate all USD versions
	cache.InvalidateCode(ctx, "USD")

	// All USD versions should be gone
	for i := 1; i <= 3; i++ {
		assert.Nil(t, cache.Get(ctx, "USD", i), "USD v%d should be invalidated", i)
	}

	// EUR should still be cached
	assert.NotNil(t, cache.Get(ctx, "EUR", 1), "EUR should not be affected")
}

func TestRedisL2Cache_InvalidateCode_TenantIsolation(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx1 := newTestContext("tenant1")
	ctx2 := newTestContext("tenant2")

	// Put USD for both tenants
	cache.Put(ctx1, "USD", 1, newTestDefinition("USD", 1))
	cache.Put(ctx1, "USD", 2, newTestDefinition("USD", 2))
	cache.Put(ctx2, "USD", 1, newTestDefinition("USD", 1))
	cache.Put(ctx2, "USD", 2, newTestDefinition("USD", 2))

	// Invalidate USD for tenant1 only
	cache.InvalidateCode(ctx1, "USD")

	// tenant1's USD should be gone
	assert.Nil(t, cache.Get(ctx1, "USD", 1))
	assert.Nil(t, cache.Get(ctx1, "USD", 2))

	// tenant2's USD should still be cached
	assert.NotNil(t, cache.Get(ctx2, "USD", 1), "tenant2 USD v1 should not be affected")
	assert.NotNil(t, cache.Get(ctx2, "USD", 2), "tenant2 USD v2 should not be affected")
}

func TestRedisL2Cache_InvalidateAll(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")

	// Put multiple entries
	cache.Put(ctx, "USD", 1, newTestDefinition("USD", 1))
	cache.Put(ctx, "EUR", 1, newTestDefinition("EUR", 1))
	cache.Put(ctx, "GBP", 1, newTestDefinition("GBP", 1))

	// Verify all are cached
	assert.NotNil(t, cache.Get(ctx, "USD", 1))
	assert.NotNil(t, cache.Get(ctx, "EUR", 1))
	assert.NotNil(t, cache.Get(ctx, "GBP", 1))

	// Invalidate all
	cache.InvalidateAll(ctx)

	// All should be gone
	assert.Nil(t, cache.Get(ctx, "USD", 1))
	assert.Nil(t, cache.Get(ctx, "EUR", 1))
	assert.Nil(t, cache.Get(ctx, "GBP", 1))
}

func TestRedisL2Cache_InvalidateAll_TenantIsolation(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx1 := newTestContext("tenant1")
	ctx2 := newTestContext("tenant2")

	// Put entries for both tenants
	cache.Put(ctx1, "USD", 1, newTestDefinition("USD", 1))
	cache.Put(ctx2, "EUR", 1, newTestDefinition("EUR", 1))

	// Invalidate all for tenant1 only
	cache.InvalidateAll(ctx1)

	// tenant1's cache should be empty
	assert.Nil(t, cache.Get(ctx1, "USD", 1))

	// tenant2's cache should be unaffected
	assert.NotNil(t, cache.Get(ctx2, "EUR", 1))
}

func TestRedisL2Cache_KeyFormat(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client,
		WithRedisKeyPrefix("testprefix"),
	)
	require.NoError(t, err)

	ctx := newTestContext("mytenant")
	def := newTestDefinition("INSTRUMENT", 42)

	cache.Put(ctx, "INSTRUMENT", 42, def)

	// Verify key format by checking directly in Redis
	expectedKey := "testprefix:instrument:mytenant:INSTRUMENT:42"
	exists, err := client.Exists(ctx, expectedKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "expected key format: %s", expectedKey)
}

func TestRedisL2Cache_ProtoRoundtrip(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")

	// Create a fully populated definition
	activatedAt := time.Now().Add(-24 * time.Hour)
	def := &registry.InstrumentDefinition{
		ID:                       uuid.New(),
		Code:                     "CARBON_CREDIT",
		Version:                  3,
		Dimension:                registry.DimensionQuantity,
		Precision:                4,
		Status:                   registry.StatusActive,
		IsSystem:                 true,
		ValidationExpression:     "amount > 0 && has(attributes.vintage)",
		FungibilityKeyExpression: "instrument_code + ':' + attributes.vintage",
		ErrorMessageExpression:   "'Invalid carbon credit amount'",
		AttributeSchema:          []byte(`{"type":"object","properties":{"vintage":{"type":"string"}}}`),
		DisplayName:              "Verified Carbon Credit",
		Description:              "Carbon credit from verified emission reduction projects",
		CreatedAt:                time.Now(),
		ActivatedAt:              &activatedAt,
	}

	// Put and Get
	cache.Put(ctx, def.Code, def.Version, def)
	result := cache.Get(ctx, def.Code, def.Version)

	require.NotNil(t, result)
	assert.Equal(t, def.ID, result.ID)
	assert.Equal(t, def.Code, result.Code)
	assert.Equal(t, def.Version, result.Version)
	assert.Equal(t, def.Dimension, result.Dimension)
	assert.Equal(t, def.Precision, result.Precision)
	assert.Equal(t, def.Status, result.Status)
	assert.Equal(t, def.IsSystem, result.IsSystem)
	assert.Equal(t, def.ValidationExpression, result.ValidationExpression)
	assert.Equal(t, def.FungibilityKeyExpression, result.FungibilityKeyExpression)
	assert.Equal(t, def.ErrorMessageExpression, result.ErrorMessageExpression)
	assert.Equal(t, def.AttributeSchema, result.AttributeSchema)
	assert.Equal(t, def.DisplayName, result.DisplayName)
	assert.Equal(t, def.Description, result.Description)

	// Timestamps may have slight precision differences due to proto conversion
	assert.WithinDuration(t, def.CreatedAt, result.CreatedAt, time.Millisecond)
	require.NotNil(t, result.ActivatedAt)
	assert.WithinDuration(t, *def.ActivatedAt, *result.ActivatedAt, time.Millisecond)
}

func TestRedisL2Cache_DimensionConversions(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")

	dimensions := []registry.Dimension{
		registry.DimensionMonetary,
		registry.DimensionEnergy,
		registry.DimensionMass,
		registry.DimensionVolume,
		registry.DimensionTime,
		registry.DimensionCompute,
		registry.DimensionQuantity,
	}

	for i, dim := range dimensions {
		def := &registry.InstrumentDefinition{
			ID:        uuid.New(),
			Code:      "TEST",
			Version:   i + 1,
			Dimension: dim,
			Status:    registry.StatusActive,
			CreatedAt: time.Now(),
		}

		cache.Put(ctx, def.Code, def.Version, def)
		result := cache.Get(ctx, def.Code, def.Version)

		require.NotNil(t, result, "dimension %s should be cached", dim)
		assert.Equal(t, dim, result.Dimension, "dimension should roundtrip correctly")
	}
}

func TestRedisL2Cache_StatusConversions(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")

	statuses := []registry.Status{
		registry.StatusDraft,
		registry.StatusActive,
		registry.StatusDeprecated,
	}

	for i, status := range statuses {
		def := &registry.InstrumentDefinition{
			ID:        uuid.New(),
			Code:      "TEST",
			Version:   i + 1,
			Dimension: registry.DimensionMonetary,
			Status:    status,
			CreatedAt: time.Now(),
		}

		cache.Put(ctx, def.Code, def.Version, def)
		result := cache.Get(ctx, def.Code, def.Version)

		require.NotNil(t, result, "status %s should be cached", status)
		assert.Equal(t, status, result.Status, "status should roundtrip correctly")
	}
}

func TestRedisL2Cache_CorruptedData(t *testing.T) {
	mr, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	ctx := newTestContext("tenant1")

	// Write corrupted data directly to Redis
	key := "refdata:instrument:tenant1:USD:1"
	mr.Set(key, "not a valid protobuf")

	// Get should return nil (treat as cache miss)
	result := cache.Get(ctx, "USD", 1)
	assert.Nil(t, result, "corrupted data should be treated as cache miss")
}

// Benchmark tests

func BenchmarkRedisL2Cache_Get_Hit(b *testing.B) {
	mr := miniredis.RunT(b)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache, _ := NewRedisL2Cache(client)
	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	def := newTestDefinition("USD", 1)
	cache.Put(ctx, "USD", 1, def)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.Get(ctx, "USD", 1)
	}
}

func BenchmarkRedisL2Cache_Get_Miss(b *testing.B) {
	mr := miniredis.RunT(b)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache, _ := NewRedisL2Cache(client)
	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.Get(ctx, "NONEXISTENT", 1)
	}
}

func BenchmarkRedisL2Cache_Put(b *testing.B) {
	mr := miniredis.RunT(b)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache, _ := NewRedisL2Cache(client)
	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))
	def := newTestDefinition("USD", 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Put(ctx, "USD", i%1000, def)
	}
}

// Test NoOpL2Cache

func TestNoOpL2Cache(t *testing.T) {
	cache := &NoOpL2Cache{}
	ctx := newTestContext("tenant1")

	// All operations should be no-ops
	result := cache.Get(ctx, "USD", 1)
	assert.Nil(t, result)

	// These should not panic
	cache.Put(ctx, "USD", 1, newTestDefinition("USD", 1))
	cache.Invalidate(ctx, "USD", 1)
	cache.InvalidateCode(ctx, "USD")
	cache.InvalidateAll(ctx)

	// Still returns nil
	result = cache.Get(ctx, "USD", 1)
	assert.Nil(t, result)
}

// Test that RedisL2Cache implements L2Cache interface
func TestRedisL2Cache_ImplementsL2Cache(t *testing.T) {
	_, client := setupMiniRedis(t)
	defer client.Close()

	cache, err := NewRedisL2Cache(client)
	require.NoError(t, err)

	// This should compile - proves interface compliance
	var _ L2Cache = cache
}
