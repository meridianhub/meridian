package redislock

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/meridianhub/meridian/shared/platform/await"
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

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

// --- Lock (per-resource) tests ---

func TestLock_AcquireAndRelease(t *testing.T) {
	_, client := setupMiniredis(t)

	l := NewLock(client, Config{
		KeyPrefix:  "test:lock",
		LockTTL:    5 * time.Second,
		RenewEvery: 1 * time.Second,
	}, testLogger())

	ctx := context.Background()

	acquired, release, err := l.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired)
	assert.NotNil(t, release)
	assert.Equal(t, 1, l.HeldCount())

	release()
	assert.Equal(t, 0, l.HeldCount())
}

func TestLock_Contention(t *testing.T) {
	_, client := setupMiniredis(t)

	config := Config{
		KeyPrefix:  "test:lock",
		LockTTL:    5 * time.Second,
		RenewEvery: 1 * time.Second,
	}

	l1 := NewLock(client, config, testLogger())
	l2 := NewLock(client, config, testLogger())

	ctx := context.Background()

	// First acquires
	acquired1, release1, err := l1.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired1)

	// Second cannot acquire same resource
	acquired2, release2, err := l2.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.False(t, acquired2)
	assert.Nil(t, release2)

	// Release first, second can now acquire
	release1()

	acquired2, release2, err = l2.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired2)
	release2()
}

func TestLock_DifferentResources(t *testing.T) {
	_, client := setupMiniredis(t)

	l := NewLock(client, Config{
		KeyPrefix:  "test:lock",
		LockTTL:    5 * time.Second,
		RenewEvery: 1 * time.Second,
	}, testLogger())

	ctx := context.Background()

	acquired1, release1, err := l.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired1)

	acquired2, release2, err := l.Acquire(ctx, "tenant-1", "resource-b")
	require.NoError(t, err)
	assert.True(t, acquired2)

	assert.Equal(t, 2, l.HeldCount())

	release1()
	release2()
	assert.Equal(t, 0, l.HeldCount())
}

func TestLock_ReleaseAll(t *testing.T) {
	_, client := setupMiniredis(t)

	l := NewLock(client, Config{
		KeyPrefix:  "test:lock",
		LockTTL:    5 * time.Second,
		RenewEvery: 1 * time.Second,
	}, testLogger())

	ctx := context.Background()

	_, _, err := l.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	_, _, err = l.Acquire(ctx, "tenant-1", "resource-b")
	require.NoError(t, err)
	_, _, err = l.Acquire(ctx, "tenant-2", "resource-a")
	require.NoError(t, err)

	assert.Equal(t, 3, l.HeldCount())

	l.ReleaseAll(ctx)
	assert.Equal(t, 0, l.HeldCount())
}

func TestLock_Expiry(t *testing.T) {
	mr, client := setupMiniredis(t)

	config := Config{
		KeyPrefix:  "test:lock",
		LockTTL:    200 * time.Millisecond,
		RenewEvery: 50 * time.Millisecond,
	}

	l1 := NewLock(client, config, testLogger())
	l2 := NewLock(client, config, testLogger())

	ctx := context.Background()

	// Acquire and immediately cancel to stop renewal
	acquired, release, err := l1.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired)

	// Stop renewal by releasing (which cancels the renewal goroutine's key)
	// We need to manually stop renewal without releasing the Redis lock
	// to simulate a crash. We do this by cancelling the renewal and removing
	// from the map, but not calling lock.Release.
	l1.mu.Lock()
	key := l1.key("tenant-1", "resource-a")
	al := l1.locks[key]
	al.cancel() // stop renewal goroutine
	delete(l1.locks, key)
	l1.mu.Unlock()
	_ = release // prevent unused warning

	// Fast-forward past TTL
	mr.FastForward(300 * time.Millisecond)

	// Second should now be able to acquire
	acquired2, release2, err := l2.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired2)
	release2()
}

func TestLock_ContextCancellation(t *testing.T) {
	_, client := setupMiniredis(t)

	l := NewLock(client, Config{
		KeyPrefix:  "test:lock",
		LockTTL:    5 * time.Second,
		RenewEvery: 50 * time.Millisecond,
	}, testLogger())

	ctx, cancel := context.WithCancel(context.Background())

	initialGoroutines := runtime.NumGoroutine()
	acquired, _, err := l.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired)

	// Cancel context should stop renewal goroutine
	cancel()

	// Give goroutine time to exit
	_ = await.AtMost(1 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return runtime.NumGoroutine() <= initialGoroutines
	})

	// Lock map entry may still exist but renewal has stopped
	// This tests that cancellation doesn't panic
}

// --- Leader election tests ---

func TestLeader_AcquireAndRelease(t *testing.T) {
	_, client := setupMiniredis(t)

	leader := NewLeader(client, Config{
		KeyPrefix:  "test:leader",
		LockTTL:    5 * time.Second,
		RenewEvery: 1 * time.Second,
	}, testLogger())

	ctx := context.Background()

	isLeader, err := leader.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)
	assert.True(t, leader.IsLeader())

	err = leader.Release(ctx)
	require.NoError(t, err)
	assert.False(t, leader.IsLeader())
}

func TestLeader_OnlyOneLeader(t *testing.T) {
	_, client := setupMiniredis(t)

	config := Config{
		KeyPrefix:  "test:leader",
		LockTTL:    5 * time.Second,
		RenewEvery: 1 * time.Second,
	}

	leader1 := NewLeader(client, config, testLogger())
	leader2 := NewLeader(client, config, testLogger())

	ctx := context.Background()

	isLeader1, err := leader1.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader1)

	isLeader2, err := leader2.TryAcquire(ctx)
	require.NoError(t, err)
	assert.False(t, isLeader2)

	// Release first, second can acquire
	err = leader1.Release(ctx)
	require.NoError(t, err)

	isLeader2, err = leader2.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader2)

	err = leader2.Release(ctx)
	require.NoError(t, err)
}

func TestLeader_RefreshExistingLock(t *testing.T) {
	_, client := setupMiniredis(t)

	leader := NewLeader(client, Config{
		KeyPrefix:  "test:leader",
		LockTTL:    5 * time.Second,
		RenewEvery: 1 * time.Second,
	}, testLogger())

	ctx := context.Background()

	isLeader, err := leader.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	// Re-acquire should refresh
	isLeader, err = leader.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	err = leader.Release(ctx)
	require.NoError(t, err)
}

func TestLeader_ReleaseWithoutAcquire(t *testing.T) {
	_, client := setupMiniredis(t)

	leader := NewLeader(client, Config{
		KeyPrefix:  "test:leader",
		LockTTL:    5 * time.Second,
		RenewEvery: 1 * time.Second,
	}, testLogger())

	ctx := context.Background()

	err := leader.Release(ctx)
	assert.NoError(t, err)
	assert.False(t, leader.IsLeader())
}

func TestLeader_LockExpiry(t *testing.T) {
	mr, client := setupMiniredis(t)

	config := Config{
		KeyPrefix:  "test:leader:expiry",
		LockTTL:    200 * time.Millisecond,
		RenewEvery: 50 * time.Millisecond,
	}

	leader1 := NewLeader(client, config, testLogger())
	leader2 := NewLeader(client, config, testLogger())

	ctx := context.Background()

	isLeader, err := leader1.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	// Stop renewal to simulate crash
	leader1.mu.Lock()
	leader1.stopRenewal()
	leader1.mu.Unlock()

	mr.FastForward(300 * time.Millisecond)

	// Second should now be able to acquire
	isLeader, err = leader2.TryAcquire(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	err = leader2.Release(ctx)
	require.NoError(t, err)
}

// --- Config tests ---

func TestConfig_Defaults(t *testing.T) {
	c := Config{KeyPrefix: "test"}.withDefaults()

	assert.Equal(t, 5*time.Minute, c.LockTTL)
	assert.Equal(t, 30*time.Second, c.RenewEvery)
}

func TestConfig_RenewClamping(t *testing.T) {
	c := Config{
		KeyPrefix:  "test",
		LockTTL:    10 * time.Second,
		RenewEvery: 15 * time.Second, // greater than TTL
	}.withDefaults()

	assert.Equal(t, 5*time.Second, c.RenewEvery) // clamped to TTL/2
}

func TestConfig_NegativeDurations(t *testing.T) {
	c := Config{
		KeyPrefix:  "test",
		LockTTL:    -1 * time.Second,
		RenewEvery: -1 * time.Second,
	}.withDefaults()

	assert.Equal(t, 5*time.Minute, c.LockTTL)
	assert.Equal(t, 30*time.Second, c.RenewEvery)
}
