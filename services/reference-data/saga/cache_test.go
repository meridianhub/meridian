package saga

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMiniRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func tenantCtx(tenantID string) context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(tenantID))
}

func testDefinition() *Definition {
	now := time.Now().UTC().Truncate(time.Second)
	activated := now.Add(-time.Hour)
	successorID := uuid.New()
	return &Definition{
		ID:                      uuid.New(),
		Name:                    "test-saga",
		Version:                 1,
		Script:                  "def run(): pass",
		Status:                  StatusActive,
		IsSystem:                false,
		PreconditionsExpression: "amount > 0",
		DisplayName:             "Test Saga",
		Description:             "A test saga definition",
		CreatedAt:               now,
		UpdatedAt:               now,
		ActivatedAt:             &activated,
		SuccessorID:             &successorID,
	}
}

// --- NewCache tests ---

func TestNewCache_NilClient(t *testing.T) {
	_, err := NewCache(nil)
	require.ErrorIs(t, err, ErrCacheClientRequired)
}

func TestNewCache_Defaults(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)
	assert.Equal(t, DefaultCacheKeyPrefix, c.keyPrefix)
	assert.Equal(t, DefaultCacheTTL, c.baseTTL)
	assert.Equal(t, DefaultCacheTTLJitter, c.ttlJitter)
}

func TestNewCache_WithOptions(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client,
		WithCacheKeyPrefix("custom"),
		WithCacheTTL(30*time.Minute, 2*time.Minute),
	)
	require.NoError(t, err)
	assert.Equal(t, "custom", c.keyPrefix)
	assert.Equal(t, 30*time.Minute, c.baseTTL)
	assert.Equal(t, 2*time.Minute, c.ttlJitter)
}

// --- Serialize/Deserialize round-trip ---

func TestSerializeDeserialize_RoundTrip(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	def := testDefinition()
	data := c.serialize(def)
	require.NotNil(t, data)

	result := c.deserialize(data)
	require.NotNil(t, result)

	assert.Equal(t, def.ID, result.ID)
	assert.Equal(t, def.Name, result.Name)
	assert.Equal(t, def.Version, result.Version)
	assert.Equal(t, def.Script, result.Script)
	assert.Equal(t, def.Status, result.Status)
	assert.Equal(t, def.IsSystem, result.IsSystem)
	assert.Equal(t, def.PreconditionsExpression, result.PreconditionsExpression)
	assert.Equal(t, def.DisplayName, result.DisplayName)
	assert.Equal(t, def.Description, result.Description)
	assert.Equal(t, def.CreatedAt.Unix(), result.CreatedAt.Unix())
	assert.Equal(t, def.UpdatedAt.Unix(), result.UpdatedAt.Unix())
	require.NotNil(t, result.ActivatedAt)
	assert.Equal(t, def.ActivatedAt.Unix(), result.ActivatedAt.Unix())
	assert.Nil(t, result.DeprecatedAt)
	require.NotNil(t, result.SuccessorID)
	assert.Equal(t, *def.SuccessorID, *result.SuccessorID)
}

func TestSerialize_NilSuccessorID(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	def := testDefinition()
	def.SuccessorID = nil

	data := c.serialize(def)
	require.NotNil(t, data)

	result := c.deserialize(data)
	require.NotNil(t, result)
	assert.Nil(t, result.SuccessorID)
}

func TestDeserialize_InvalidJSON(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	result := c.deserialize([]byte("not json"))
	assert.Nil(t, result)
}

func TestDeserialize_InvalidUUID(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	// Invalid ID should be parsed as zero UUID
	data := []byte(`{"id":"not-a-uuid","name":"test","version":1,"status":"ACTIVE","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}`)
	result := c.deserialize(data)
	require.NotNil(t, result)
	assert.Equal(t, uuid.Nil, result.ID)
}

func TestDeserialize_InvalidSuccessorUUID(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	data := []byte(`{"id":"` + uuid.New().String() + `","name":"test","version":1,"status":"ACTIVE","successor_id":"bad-uuid","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}`)
	result := c.deserialize(data)
	require.NotNil(t, result)
	assert.Nil(t, result.SuccessorID)
}

// --- GetByID / PutByID ---

func TestGetByID_CacheMiss(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")
	result := c.GetByID(ctx, uuid.New())
	assert.Nil(t, result)
}

func TestGetByID_NoTenantContext(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	result := c.GetByID(context.Background(), uuid.New())
	assert.Nil(t, result)
}

func TestPutByID_AndGetByID(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheTTL(time.Hour, 0))
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")
	def := testDefinition()

	c.PutByID(ctx, def)
	result := c.GetByID(ctx, def.ID)
	require.NotNil(t, result)
	assert.Equal(t, def.ID, result.ID)
	assert.Equal(t, def.Name, result.Name)
}

func TestPutByID_NilDefinition(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")
	c.PutByID(ctx, nil) // should not panic
}

func TestPutByID_NoTenantContext(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	c.PutByID(context.Background(), testDefinition()) // should not panic
}

// --- Get / Put (by name+version) ---

func TestPut_AndGet(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheTTL(time.Hour, 0))
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")
	def := testDefinition()

	c.Put(ctx, def)
	result := c.Get(ctx, def.Name, def.Version)
	require.NotNil(t, result)
	assert.Equal(t, def.Name, result.Name)
	assert.Equal(t, def.Version, result.Version)
}

func TestGet_NoTenantContext(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	result := c.Get(context.Background(), "test", 1)
	assert.Nil(t, result)
}

func TestPut_NilDefinition(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")
	c.Put(ctx, nil) // should not panic
}

func TestPut_NoTenantContext(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	c.Put(context.Background(), testDefinition()) // should not panic
}

// --- GetActive / PutActive ---

func TestPutActive_AndGetActive(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheTTL(time.Hour, 0))
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")
	def := testDefinition()

	c.PutActive(ctx, def)
	result := c.GetActive(ctx, def.Name)
	require.NotNil(t, result)
	assert.Equal(t, def.Name, result.Name)
}

func TestGetActive_NoTenantContext(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	result := c.GetActive(context.Background(), "test")
	assert.Nil(t, result)
}

func TestPutActive_NilDefinition(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")
	c.PutActive(ctx, nil) // should not panic
}

func TestPutActive_NoTenantContext(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	c.PutActive(context.Background(), testDefinition()) // should not panic
}

// --- Invalidation ---

func TestInvalidateByID(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheTTL(time.Hour, 0))
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")
	def := testDefinition()

	c.PutByID(ctx, def)
	require.NotNil(t, c.GetByID(ctx, def.ID))

	c.InvalidateByID(ctx, def.ID)
	assert.Nil(t, c.GetByID(ctx, def.ID))
}

func TestInvalidateByID_NoTenantContext(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	c.InvalidateByID(context.Background(), uuid.New()) // should not panic
}

func TestInvalidate(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheTTL(time.Hour, 0))
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")
	def := testDefinition()

	c.Put(ctx, def)
	require.NotNil(t, c.Get(ctx, def.Name, def.Version))

	c.Invalidate(ctx, def.Name, def.Version)
	assert.Nil(t, c.Get(ctx, def.Name, def.Version))
}

func TestInvalidate_NoTenantContext(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	c.Invalidate(context.Background(), "test", 1) // should not panic
}

func TestInvalidateName(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheTTL(time.Hour, 0))
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")

	// Store multiple versions + active
	def1 := testDefinition()
	def1.Name = "multi-version"
	def1.Version = 1

	def2 := testDefinition()
	def2.Name = "multi-version"
	def2.Version = 2

	c.Put(ctx, def1)
	c.Put(ctx, def2)
	c.PutActive(ctx, def2)

	require.NotNil(t, c.Get(ctx, "multi-version", 1))
	require.NotNil(t, c.Get(ctx, "multi-version", 2))
	require.NotNil(t, c.GetActive(ctx, "multi-version"))

	c.InvalidateName(ctx, "multi-version")

	assert.Nil(t, c.Get(ctx, "multi-version", 1))
	assert.Nil(t, c.Get(ctx, "multi-version", 2))
	assert.Nil(t, c.GetActive(ctx, "multi-version"))
}

func TestInvalidateName_NoTenantContext(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	c.InvalidateName(context.Background(), "test") // should not panic
}

func TestInvalidateAll(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheTTL(time.Hour, 0))
	require.NoError(t, err)

	ctx := tenantCtx("tenant-1")
	def := testDefinition()

	c.PutByID(ctx, def)
	c.Put(ctx, def)
	c.PutActive(ctx, def)

	require.NotNil(t, c.GetByID(ctx, def.ID))
	require.NotNil(t, c.Get(ctx, def.Name, def.Version))
	require.NotNil(t, c.GetActive(ctx, def.Name))

	c.InvalidateAll(ctx)

	assert.Nil(t, c.GetByID(ctx, def.ID))
	assert.Nil(t, c.Get(ctx, def.Name, def.Version))
	assert.Nil(t, c.GetActive(ctx, def.Name))
}

func TestInvalidateAll_NoTenantContext(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client)
	require.NoError(t, err)

	c.InvalidateAll(context.Background()) // should not panic
}

// --- Tenant isolation ---

func TestTenantIsolation(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheTTL(time.Hour, 0))
	require.NoError(t, err)

	ctx1 := tenantCtx("tenant-1")
	ctx2 := tenantCtx("tenant-2")

	def := testDefinition()
	c.PutByID(ctx1, def)
	c.Put(ctx1, def)
	c.PutActive(ctx1, def)

	// tenant-2 should not see tenant-1's data
	assert.Nil(t, c.GetByID(ctx2, def.ID))
	assert.Nil(t, c.Get(ctx2, def.Name, def.Version))
	assert.Nil(t, c.GetActive(ctx2, def.Name))

	// tenant-1 should still see its data
	assert.NotNil(t, c.GetByID(ctx1, def.ID))
	assert.NotNil(t, c.Get(ctx1, def.Name, def.Version))
	assert.NotNil(t, c.GetActive(ctx1, def.Name))
}

// --- Key format tests ---

func TestKeyFormats(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheKeyPrefix("test"))
	require.NoError(t, err)

	tid := tenant.TenantID("t1")
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	assert.Equal(t, "test:id:t1:11111111-1111-1111-1111-111111111111", c.idKey(tid, id))
	assert.Equal(t, "test:def:t1:saga-name:3", c.definitionKey(tid, "saga-name", 3))
	assert.Equal(t, "test:active:t1:saga-name", c.activeKey(tid, "saga-name"))
	assert.Equal(t, "test:def:t1:saga-name:*", c.definitionKeyPattern(tid, "saga-name"))
	assert.Equal(t, "test:id:t1:*", c.tenantKeyPattern(tid, "id"))
}

// --- Jittered TTL ---

func TestJitteredTTL_NoJitter(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheTTL(time.Hour, 0))
	require.NoError(t, err)

	assert.Equal(t, time.Hour, c.jitteredTTL())
}

func TestJitteredTTL_WithJitter(t *testing.T) {
	client := setupMiniRedis(t)
	c, err := NewCache(client, WithCacheTTL(time.Hour, 5*time.Minute))
	require.NoError(t, err)

	seenDifferent := false
	for i := 0; i < 100; i++ {
		ttl := c.jitteredTTL()
		assert.GreaterOrEqual(t, ttl, 55*time.Minute)
		assert.LessOrEqual(t, ttl, 65*time.Minute)
		if ttl != time.Hour {
			seenDifferent = true
		}
	}
	assert.True(t, seenDifferent, "jitter should produce at least one value != base TTL over 100 samples")
}
