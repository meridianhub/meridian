package worker

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMiniredis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	t.Cleanup(func() {
		client.Close()
	})
	return mr, client
}

func TestRedisLeaderElector_AcquireAndRelease(t *testing.T) {
	_, client := setupMiniredis(t)

	elector := NewRedisLeaderElector(client, RedisLeaderConfig{
		LockTTL:       5 * time.Second,
		RenewInterval: 1 * time.Second,
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx := context.Background()

	// Acquire
	isLeader, err := elector.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)
	assert.True(t, elector.IsLeader())

	// Release
	err = elector.Release(ctx)
	require.NoError(t, err)
	assert.False(t, elector.IsLeader())
}

func TestRedisLeaderElector_OnlyOneLeader(t *testing.T) {
	_, client := setupMiniredis(t)

	elector1 := NewRedisLeaderElector(client, RedisLeaderConfig{
		LockTTL:       5 * time.Second,
		RenewInterval: 1 * time.Second,
		LockKey:       "test:leader",
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	elector2 := NewRedisLeaderElector(client, RedisLeaderConfig{
		LockTTL:       5 * time.Second,
		RenewInterval: 1 * time.Second,
		LockKey:       "test:leader",
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx := context.Background()

	// First elector acquires
	isLeader1, err := elector1.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader1)

	// Second elector cannot acquire (same key)
	isLeader2, err := elector2.TryAcquire(ctx)
	require.NoError(t, err)
	assert.False(t, isLeader2)

	// Release first, second can now acquire
	err = elector1.Release(ctx)
	require.NoError(t, err)

	isLeader2, err = elector2.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader2)

	err = elector2.Release(ctx)
	require.NoError(t, err)
}

func TestRedisLeaderElector_RefreshExistingLock(t *testing.T) {
	_, client := setupMiniredis(t)

	elector := NewRedisLeaderElector(client, RedisLeaderConfig{
		LockTTL:       5 * time.Second,
		RenewInterval: 1 * time.Second,
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx := context.Background()

	// Acquire
	isLeader, err := elector.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	// Re-acquire should refresh
	isLeader, err = elector.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	err = elector.Release(ctx)
	require.NoError(t, err)
}

func TestRedisLeaderElector_DefaultLockKey(t *testing.T) {
	_, client := setupMiniredis(t)

	elector := NewRedisLeaderElector(client, RedisLeaderConfig{
		LockTTL:       5 * time.Second,
		RenewInterval: 1 * time.Second,
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx := context.Background()

	isLeader, err := elector.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	err = elector.Release(ctx)
	require.NoError(t, err)
}

func TestRedisLeaderElector_ReleaseWithoutAcquire(t *testing.T) {
	_, client := setupMiniredis(t)

	elector := NewRedisLeaderElector(client, RedisLeaderConfig{
		LockTTL:       5 * time.Second,
		RenewInterval: 1 * time.Second,
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx := context.Background()

	// Release without acquire should be safe
	err := elector.Release(ctx)
	assert.NoError(t, err)
	assert.False(t, elector.IsLeader())
}

func TestRedisLeaderElector_LockExpiry(t *testing.T) {
	mr, client := setupMiniredis(t)

	elector1 := NewRedisLeaderElector(client, RedisLeaderConfig{
		LockTTL:       200 * time.Millisecond,
		RenewInterval: 50 * time.Millisecond,
		LockKey:       "test:expiry",
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	elector2 := NewRedisLeaderElector(client, RedisLeaderConfig{
		LockTTL:       200 * time.Millisecond,
		RenewInterval: 50 * time.Millisecond,
		LockKey:       "test:expiry",
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx := context.Background()

	// First acquires
	isLeader, err := elector1.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	// Stop renewal and fast-forward time to expire the lock
	elector1.mu.Lock()
	elector1.stopRenewal()
	elector1.mu.Unlock()

	mr.FastForward(300 * time.Millisecond)

	// Second should now be able to acquire
	isLeader, err = elector2.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	err = elector2.Release(ctx)
	require.NoError(t, err)
}
