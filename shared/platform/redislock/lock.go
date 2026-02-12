// Package redislock provides distributed locking backed by Redis.
//
// It supports two patterns:
//
//   - Per-resource locking via [Lock]: multiple locks keyed by tenant and resource,
//     suitable for preventing concurrent execution of the same task.
//   - Leader election via [Leader]: a single lock for leader/follower coordination
//     across replicas.
//
// Both patterns use background renewal goroutines to keep locks alive during
// long-running operations, with automatic cleanup on context cancellation or
// explicit release.
package redislock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"
)

// activeLock tracks a held lock and its renewal goroutine.
type activeLock struct {
	lock   *redislock.Lock
	cancel context.CancelFunc
}

// Lock provides per-resource distributed locking using Redis.
// It manages multiple concurrent locks keyed by tenant and resource ID,
// each with an independent background renewal goroutine.
type Lock struct {
	client *redislock.Client
	config Config
	logger *slog.Logger

	mu    sync.Mutex
	locks map[string]*activeLock
}

// NewLock creates a per-resource distributed lock manager.
func NewLock(redisClient *redis.Client, config Config, logger *slog.Logger) *Lock {
	config = config.withDefaults()
	return &Lock{
		client: redislock.New(redisClient),
		config: config,
		logger: logger.With("component", "redislock"),
		locks:  make(map[string]*activeLock),
	}
}

func (l *Lock) key(tenantID, resourceID string) string {
	return fmt.Sprintf("%s:%s:%s", l.config.KeyPrefix, tenantID, resourceID)
}

// Acquire attempts to acquire a lock for the given tenant and resource.
// Returns true if the lock was acquired, false if another holder has it.
// On success, a background renewal goroutine runs until the returned release
// function is called or the context is cancelled.
func (l *Lock) Acquire(ctx context.Context, tenantID, resourceID string) (bool, func(), error) {
	key := l.key(tenantID, resourceID)

	lock, err := l.client.Obtain(ctx, key, l.config.LockTTL, nil)
	if err != nil {
		if errors.Is(err, redislock.ErrNotObtained) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("obtain lock for %s: %w", key, err)
	}

	renewCtx, cancel := context.WithCancel(ctx)
	al := &activeLock{lock: lock, cancel: cancel}

	l.mu.Lock()
	l.locks[key] = al
	l.mu.Unlock()

	go l.renewLoop(renewCtx, key, al)

	l.logger.Debug("lock acquired", "key", key)

	release := func() {
		if ctx.Err() != nil {
			l.releaseByInstance(context.Background(), key, al) //nolint:contextcheck // fallback when parent cancelled
		} else {
			l.releaseByInstance(ctx, key, al)
		}
	}
	return true, release, nil
}

// releaseByInstance releases a lock only if the stored instance matches expected.
// This prevents a stale release closure from dropping a newer lock for the same key.
func (l *Lock) releaseByInstance(ctx context.Context, key string, expected *activeLock) {
	l.mu.Lock()
	al, ok := l.locks[key]
	if !ok || al != expected {
		l.mu.Unlock()
		return
	}
	delete(l.locks, key)
	l.mu.Unlock()

	al.cancel()
	if err := al.lock.Release(ctx); err != nil {
		l.logger.Warn("failed to release lock", "key", key, "error", err)
	} else {
		l.logger.Debug("lock released", "key", key)
	}
}

// ReleaseAll releases all held locks. Used during graceful shutdown.
func (l *Lock) ReleaseAll(ctx context.Context) {
	l.mu.Lock()
	snapshot := make(map[string]*activeLock, len(l.locks))
	for k, v := range l.locks {
		snapshot[k] = v
	}
	l.locks = make(map[string]*activeLock)
	l.mu.Unlock()

	for key, al := range snapshot {
		al.cancel()
		if err := al.lock.Release(ctx); err != nil {
			l.logger.Warn("failed to release lock during shutdown", "key", key, "error", err)
		}
	}
}

// HeldCount returns the number of currently held locks.
func (l *Lock) HeldCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.locks)
}

// renewLoop periodically refreshes a lock until the context is cancelled.
func (l *Lock) renewLoop(ctx context.Context, key string, al *activeLock) {
	ticker := time.NewTicker(l.config.RenewEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := al.lock.Refresh(ctx, l.config.LockTTL, nil); err != nil {
				if ctx.Err() != nil {
					return
				}
				l.logger.Error("lock renewal failed", "key", key, "error", err)
				l.mu.Lock()
				if l.locks[key] == al {
					delete(l.locks, key)
				}
				l.mu.Unlock()
				return
			}
		}
	}
}

// Leader provides single-lock leader election using Redis.
// Only one Leader instance across all replicas can hold the lock at a time.
type Leader struct {
	client  *redislock.Client
	lockKey string
	config  Config
	logger  *slog.Logger

	mu       sync.Mutex
	lock     *redislock.Lock
	isLeader bool
	cancel   context.CancelFunc
}

// NewLeader creates a new Redis-based leader elector.
// The Config.KeyPrefix is used as the lock key.
func NewLeader(redisClient *redis.Client, config Config, logger *slog.Logger) *Leader {
	config = config.withDefaults()
	return &Leader{
		client:  redislock.New(redisClient),
		lockKey: config.KeyPrefix,
		config:  config,
		logger:  logger.With("component", "redislock_leader"),
	}
}

// TryAcquire attempts to acquire or renew the leader lock.
// Returns true if this instance is (or remains) the leader.
func (l *Leader) TryAcquire(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// If we already hold the lock, try to refresh it
	if l.lock != nil {
		err := l.lock.Refresh(ctx, l.config.LockTTL, nil)
		if err == nil {
			l.isLeader = true
			if l.cancel == nil {
				l.startRenewal(ctx)
			}
			return true, nil
		}
		l.logger.Warn("leader lock refresh failed, attempting re-acquire", "error", err)
		l.isLeader = false
		l.lock = nil
		l.stopRenewal()
	}

	lock, err := l.client.Obtain(ctx, l.lockKey, l.config.LockTTL, nil)
	if err != nil {
		if errors.Is(err, redislock.ErrNotObtained) {
			l.isLeader = false
			return false, nil
		}
		return false, err
	}

	l.lock = lock
	l.isLeader = true
	l.startRenewal(ctx)

	l.logger.Info("acquired leader lock")
	return true, nil
}

// Release releases the leader lock.
func (l *Leader) Release(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.stopRenewal()

	if l.lock != nil {
		err := l.lock.Release(ctx)
		l.lock = nil
		l.isLeader = false
		if err != nil {
			return err
		}
		l.logger.Info("released leader lock")
	}

	return nil
}

// IsLeader returns whether this instance currently holds the leader lock.
func (l *Leader) IsLeader() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.isLeader
}

// startRenewal begins the background lock renewal goroutine.
// Must be called with l.mu held.
func (l *Leader) startRenewal(parent context.Context) {
	l.stopRenewal()

	renewCtx, cancel := context.WithCancel(parent)
	l.cancel = cancel

	go func() {
		ticker := time.NewTicker(l.config.RenewEvery)
		defer ticker.Stop()

		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				l.mu.Lock()
				if l.lock == nil {
					l.mu.Unlock()
					return
				}
				err := l.lock.Refresh(renewCtx, l.config.LockTTL, nil)
				if err != nil {
					l.logger.Error("failed to renew leader lock", "error", err)
					l.isLeader = false
					l.lock = nil
					l.mu.Unlock()
					return
				}
				l.mu.Unlock()
			}
		}
	}()
}

// stopRenewal cancels the background renewal goroutine.
// Must be called with l.mu held.
func (l *Leader) stopRenewal() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
}
