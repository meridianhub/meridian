package lock

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/meridianhub/meridian/services/payment-order/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRedisLockClient(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { redisClient.Close() })

	lockClient := NewRedisLockClient(redisClient)
	assert.NotNil(t, lockClient)
}

func TestRedisLockClient_Obtain_Success(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { redisClient.Close() })

	lockClient := NewRedisLockClient(redisClient)

	lock, err := lockClient.Obtain(context.Background(), "test-lock-key", 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, lock)

	// Release the lock
	err = lock.Release(context.Background())
	assert.NoError(t, err)
}

func TestRedisLockClient_Obtain_AlreadyHeld(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { redisClient.Close() })

	lockClient := NewRedisLockClient(redisClient)

	// Acquire lock
	lock1, err := lockClient.Obtain(context.Background(), "contested-lock", 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, lock1)
	defer func() { _ = lock1.Release(context.Background()) }()

	// Try to acquire the same lock - should fail with LockNotObtainedError
	_, err = lockClient.Obtain(context.Background(), "contested-lock", 5*time.Second)
	require.Error(t, err)

	var lockErr service.LockNotObtainedError
	assert.ErrorAs(t, err, &lockErr)
}

func TestRedisLockClient_Obtain_DifferentKeys(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { redisClient.Close() })

	lockClient := NewRedisLockClient(redisClient)

	// Different keys should not conflict
	lock1, err := lockClient.Obtain(context.Background(), "lock-a", 5*time.Second)
	require.NoError(t, err)
	defer func() { _ = lock1.Release(context.Background()) }()

	lock2, err := lockClient.Obtain(context.Background(), "lock-b", 5*time.Second)
	require.NoError(t, err)
	defer func() { _ = lock2.Release(context.Background()) }()
}

func TestRedisLock_Release(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { redisClient.Close() })

	lockClient := NewRedisLockClient(redisClient)

	// Acquire and release
	lock, err := lockClient.Obtain(context.Background(), "release-test", 5*time.Second)
	require.NoError(t, err)

	err = lock.Release(context.Background())
	require.NoError(t, err)

	// Now should be able to acquire again
	lock2, err := lockClient.Obtain(context.Background(), "release-test", 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, lock2)
	defer func() { _ = lock2.Release(context.Background()) }()
}
