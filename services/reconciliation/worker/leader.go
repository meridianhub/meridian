package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"
)

const defaultLeaderLockKey = "meridian:reconciliation:scheduler:leader"

// RedisLeaderElector implements LeaderElector using a Redis distributed lock
// with automatic renewal.
type RedisLeaderElector struct {
	client     *redislock.Client
	lockKey    string
	lockTTL    time.Duration
	renewEvery time.Duration
	logger     *slog.Logger

	mu       sync.Mutex
	lock     *redislock.Lock
	isLeader bool
	cancel   context.CancelFunc
}

// RedisLeaderConfig holds configuration for the Redis leader elector.
type RedisLeaderConfig struct {
	// LockTTL is how long the lock is held before expiring.
	LockTTL time.Duration
	// RenewInterval is how often to renew the lock (must be less than TTL).
	RenewInterval time.Duration
	// LockKey overrides the default lock key.
	LockKey string
}

// NewRedisLeaderElector creates a new Redis-based leader elector.
func NewRedisLeaderElector(
	redisClient *redis.Client,
	config RedisLeaderConfig,
	logger *slog.Logger,
) *RedisLeaderElector {
	lockKey := config.LockKey
	if lockKey == "" {
		lockKey = defaultLeaderLockKey
	}

	return &RedisLeaderElector{
		client:     redislock.New(redisClient),
		lockKey:    lockKey,
		lockTTL:    config.LockTTL,
		renewEvery: config.RenewInterval,
		logger:     logger.With("component", "leader_elector"),
	}
}

// TryAcquire attempts to acquire or renew the leader lock.
func (e *RedisLeaderElector) TryAcquire(ctx context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// If we already hold the lock, try to refresh it
	if e.lock != nil {
		err := e.lock.Refresh(ctx, e.lockTTL, nil)
		if err == nil {
			return true, nil
		}
		// Lock lost or expired, try to re-acquire
		e.logger.Warn("leader lock refresh failed, attempting re-acquire", "error", err)
		e.stopRenewal()
	}

	// Try to obtain the lock
	lock, err := e.client.Obtain(ctx, e.lockKey, e.lockTTL, nil)
	if err != nil {
		if errors.Is(err, redislock.ErrNotObtained) {
			e.isLeader = false
			return false, nil
		}
		return false, err
	}

	e.lock = lock
	e.isLeader = true
	e.startRenewal(ctx)

	e.logger.Info("acquired leader lock")
	return true, nil
}

// Release releases the leader lock.
func (e *RedisLeaderElector) Release(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.stopRenewal()

	if e.lock != nil {
		err := e.lock.Release(ctx)
		e.lock = nil
		e.isLeader = false
		if err != nil {
			return err
		}
		e.logger.Info("released leader lock")
	}

	return nil
}

// IsLeader returns whether this instance currently holds the leader lock.
func (e *RedisLeaderElector) IsLeader() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isLeader
}

// startRenewal begins the background lock renewal goroutine.
// The parent context is used to derive a cancellable context for the renewal loop.
// Must be called with e.mu held.
func (e *RedisLeaderElector) startRenewal(parent context.Context) {
	e.stopRenewal()

	renewCtx, cancel := context.WithCancel(parent)
	e.cancel = cancel

	go func() {
		ticker := time.NewTicker(e.renewEvery)
		defer ticker.Stop()

		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				e.mu.Lock()
				if e.lock == nil {
					e.mu.Unlock()
					return
				}
				err := e.lock.Refresh(renewCtx, e.lockTTL, nil)
				if err != nil {
					e.logger.Error("failed to renew leader lock", "error", err)
					e.isLeader = false
					e.lock = nil
					e.mu.Unlock()
					return
				}
				e.mu.Unlock()
			}
		}
	}()
}

// stopRenewal cancels the background renewal goroutine.
// Must be called with e.mu held.
func (e *RedisLeaderElector) stopRenewal() {
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
}
