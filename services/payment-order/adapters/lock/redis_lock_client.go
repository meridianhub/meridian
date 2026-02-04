// Package lock provides distributed locking implementations.
package lock

import (
	"context"
	"errors"
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"

	"github.com/meridianhub/meridian/services/payment-order/service"
)

// redisLock wraps a redislock.Lock to implement the service.Lock interface.
type redisLock struct {
	lock *redislock.Lock
}

// Release releases the Redis lock.
func (l *redisLock) Release(ctx context.Context) error {
	return l.lock.Release(ctx)
}

// RedisLockClient implements service.LockClient using Redis distributed locks.
type RedisLockClient struct {
	client *redislock.Client
}

// NewRedisLockClient creates a new Redis-based lock client.
func NewRedisLockClient(redisClient *redis.Client) *RedisLockClient {
	return &RedisLockClient{
		client: redislock.New(redisClient),
	}
}

// Obtain attempts to acquire a distributed lock with the given key and TTL.
func (c *RedisLockClient) Obtain(ctx context.Context, key string, ttl time.Duration) (service.Lock, error) {
	lock, err := c.client.Obtain(ctx, key, ttl, nil)
	if errors.Is(err, redislock.ErrNotObtained) {
		return nil, service.LockNotObtainedError{}
	}
	if err != nil {
		return nil, err
	}
	return &redisLock{lock: lock}, nil
}
