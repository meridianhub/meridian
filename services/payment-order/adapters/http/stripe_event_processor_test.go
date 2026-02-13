package http

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client, mr
}

func setupTestEventProcessor(t *testing.T) (*StripeEventProcessor, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	client, mr := setupTestRedis(t)
	proc, err := NewStripeEventProcessor(StripeEventProcessorConfig{
		RedisClient: client,
	})
	require.NoError(t, err)
	return proc, client, mr
}

func TestNewStripeEventProcessor(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		client, _ := setupTestRedis(t)
		proc, err := NewStripeEventProcessor(StripeEventProcessorConfig{
			RedisClient: client,
		})
		assert.NoError(t, err)
		assert.NotNil(t, proc)
	})

	t.Run("nil redis client", func(t *testing.T) {
		proc, err := NewStripeEventProcessor(StripeEventProcessorConfig{
			RedisClient: nil,
		})
		assert.ErrorIs(t, err, ErrNilRedisClient)
		assert.Nil(t, proc)
	})
}

func TestStripeEventProcessor_PreProcess(t *testing.T) {
	ctx := context.Background()

	t.Run("empty event ID is a no-op", func(t *testing.T) {
		proc, client, _ := setupTestEventProcessor(t)

		err := proc.PreProcess(ctx, "")
		assert.NoError(t, err)

		// Verify no key was set in Redis
		keys, err := client.Keys(ctx, processedWebhookKeyPrefix+"*").Result()
		require.NoError(t, err)
		assert.Empty(t, keys)
	})

	t.Run("new event is accepted", func(t *testing.T) {
		proc, client, _ := setupTestEventProcessor(t)

		err := proc.PreProcess(ctx, "evt_new_123")
		assert.NoError(t, err)

		// Verify the key was set in Redis
		exists, err := client.Exists(ctx, processedWebhookKeyPrefix+"evt_new_123").Result()
		require.NoError(t, err)
		assert.Equal(t, int64(1), exists)
	})

	t.Run("duplicate event is rejected", func(t *testing.T) {
		proc, _, _ := setupTestEventProcessor(t)

		// First call should succeed
		err := proc.PreProcess(ctx, "evt_dup_456")
		assert.NoError(t, err)

		// Second call with same event ID should return ErrEventAlreadyProcessed
		err = proc.PreProcess(ctx, "evt_dup_456")
		assert.ErrorIs(t, err, ErrEventAlreadyProcessed)
	})

	t.Run("different events are independent", func(t *testing.T) {
		proc, _, _ := setupTestEventProcessor(t)

		err := proc.PreProcess(ctx, "evt_a")
		assert.NoError(t, err)

		err = proc.PreProcess(ctx, "evt_b")
		assert.NoError(t, err)
	})

	t.Run("redis failure allows processing to continue", func(t *testing.T) {
		proc, _, mr := setupTestEventProcessor(t)

		// Close miniredis to simulate failure
		mr.Close()

		err := proc.PreProcess(ctx, "evt_fail")
		// Should return nil (not an error) to allow processing to continue
		assert.NoError(t, err)
	})

	t.Run("ttl is set on processed key", func(t *testing.T) {
		proc, _, mr := setupTestEventProcessor(t)

		err := proc.PreProcess(ctx, "evt_ttl_check")
		assert.NoError(t, err)

		ttl := mr.TTL(processedWebhookKeyPrefix + "evt_ttl_check")
		assert.Equal(t, processedWebhookTTL, ttl)
	})
}

func TestStripeEventProcessor_ScheduleDunning(t *testing.T) {
	ctx := context.Background()
	const testTenantID = "test_tenant"
	zsetKey := dunningRetryZSetPrefix + testTenantID

	t.Run("schedules dunning for payment order", func(t *testing.T) {
		proc, client, _ := setupTestEventProcessor(t)

		err := proc.ScheduleDunning(ctx, testTenantID, "po-123")
		assert.NoError(t, err)

		// Verify the entry was added to the tenant-scoped ZSET
		members, err := client.ZRangeByScore(ctx, zsetKey, &redis.ZRangeBy{
			Min: "-inf",
			Max: "+inf",
		}).Result()
		require.NoError(t, err)
		assert.Len(t, members, 1)
		assert.Equal(t, "stripe:po-123", members[0])
	})

	t.Run("empty payment order ID is a no-op", func(t *testing.T) {
		proc, client, _ := setupTestEventProcessor(t)

		err := proc.ScheduleDunning(ctx, testTenantID, "")
		assert.NoError(t, err)

		// Verify nothing was added to the ZSET
		count, err := client.ZCard(ctx, zsetKey).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
	})

	t.Run("empty tenant ID returns error", func(t *testing.T) {
		proc, _, _ := setupTestEventProcessor(t)

		err := proc.ScheduleDunning(ctx, "", "po-123")
		assert.ErrorIs(t, err, ErrDunningMissingTenant)
	})

	t.Run("multiple dunning entries are independent", func(t *testing.T) {
		proc, client, _ := setupTestEventProcessor(t)

		err := proc.ScheduleDunning(ctx, testTenantID, "po-aaa")
		assert.NoError(t, err)

		err = proc.ScheduleDunning(ctx, testTenantID, "po-bbb")
		assert.NoError(t, err)

		count, err := client.ZCard(ctx, zsetKey).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(2), count)
	})

	t.Run("redis failure returns error", func(t *testing.T) {
		proc, _, mr := setupTestEventProcessor(t)

		mr.Close()

		err := proc.ScheduleDunning(ctx, testTenantID, "po-fail")
		assert.Error(t, err)
	})

	t.Run("tenant isolation between tenants", func(t *testing.T) {
		proc, client, _ := setupTestEventProcessor(t)

		const tenantA = "tenant_a"
		const tenantB = "tenant_b"

		err := proc.ScheduleDunning(ctx, tenantA, "po-a1")
		assert.NoError(t, err)
		err = proc.ScheduleDunning(ctx, tenantB, "po-b1")
		assert.NoError(t, err)

		// Verify tenant A's key only has tenant A's entry
		keyA := dunningRetryZSetPrefix + tenantA
		membersA, err := client.ZRangeByScore(ctx, keyA, &redis.ZRangeBy{
			Min: "-inf",
			Max: "+inf",
		}).Result()
		require.NoError(t, err)
		assert.Len(t, membersA, 1)
		assert.Equal(t, "stripe:po-a1", membersA[0])

		// Verify tenant B's key only has tenant B's entry
		keyB := dunningRetryZSetPrefix + tenantB
		membersB, err := client.ZRangeByScore(ctx, keyB, &redis.ZRangeBy{
			Min: "-inf",
			Max: "+inf",
		}).Result()
		require.NoError(t, err)
		assert.Len(t, membersB, 1)
		assert.Equal(t, "stripe:po-b1", membersB[0])
	})
}
