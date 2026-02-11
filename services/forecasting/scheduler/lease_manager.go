package scheduler

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

// LeaseManager provides distributed locking for strategy execution using Redis.
// It prevents multiple pods from executing the same strategy concurrently.
// Orphan detection is handled automatically via Redis TTL expiry.
type LeaseManager struct {
	client     *redislock.Client
	lockTTL    time.Duration
	renewEvery time.Duration
	logger     *slog.Logger

	mu    sync.Mutex
	locks map[string]*activeLease
}

// activeLease tracks a held lock and its renewal goroutine.
type activeLease struct {
	lock   *redislock.Lock
	cancel context.CancelFunc
}

// LeaseConfig holds configuration for the LeaseManager.
type LeaseConfig struct {
	// LockTTL is how long each strategy lease is held before expiring.
	// Default: 5 minutes.
	LockTTL time.Duration
	// RenewInterval is how often to renew the lease during execution.
	// Must be less than LockTTL. Default: 30 seconds.
	RenewInterval time.Duration
}

// NewLeaseManager creates a new Redis-based lease manager.
func NewLeaseManager(
	redisClient *redis.Client,
	config LeaseConfig,
	logger *slog.Logger,
) *LeaseManager {
	if config.LockTTL == 0 {
		config.LockTTL = 5 * time.Minute
	}
	if config.RenewInterval == 0 {
		config.RenewInterval = 30 * time.Second
	}
	if config.RenewInterval >= config.LockTTL {
		config.RenewInterval = config.LockTTL / 2
	}

	return &LeaseManager{
		client:     redislock.New(redisClient),
		lockTTL:    config.LockTTL,
		renewEvery: config.RenewInterval,
		logger:     logger.With("component", "lease_manager"),
		locks:      make(map[string]*activeLease),
	}
}

// leaseKey returns the Redis key for a strategy lease.
func leaseKey(tenantID string, strategyID string) string {
	return fmt.Sprintf("meridian:forecasting:strategy:%s:%s", tenantID, strategyID)
}

// Acquire attempts to acquire a lease for the given strategy.
// Returns true if the lease was acquired, false if another pod holds it.
// On success, starts a background renewal goroutine that runs until Release is called.
func (lm *LeaseManager) Acquire(ctx context.Context, tenantID, strategyID string) (bool, error) {
	key := leaseKey(tenantID, strategyID)

	lock, err := lm.client.Obtain(ctx, key, lm.lockTTL, nil)
	if err != nil {
		if errors.Is(err, redislock.ErrNotObtained) {
			return false, nil
		}
		return false, fmt.Errorf("obtain lease for %s: %w", key, err)
	}

	renewCtx, cancel := context.WithCancel(ctx)
	lease := &activeLease{
		lock:   lock,
		cancel: cancel,
	}

	lm.mu.Lock()
	lm.locks[key] = lease
	lm.mu.Unlock()

	go lm.renewLoop(renewCtx, key, lock)

	lm.logger.Debug("lease acquired", "key", key)
	return true, nil
}

// Release releases the lease for the given strategy, stopping renewal.
func (lm *LeaseManager) Release(ctx context.Context, tenantID, strategyID string) error {
	key := leaseKey(tenantID, strategyID)

	lm.mu.Lock()
	lease, ok := lm.locks[key]
	if !ok {
		lm.mu.Unlock()
		return nil
	}
	delete(lm.locks, key)
	lm.mu.Unlock()

	lease.cancel()

	if err := lease.lock.Release(ctx); err != nil {
		lm.logger.Warn("failed to release lease", "key", key, "error", err)
		return fmt.Errorf("release lease for %s: %w", key, err)
	}

	lm.logger.Debug("lease released", "key", key)
	return nil
}

// ReleaseAll releases all held leases. Used during graceful shutdown.
func (lm *LeaseManager) ReleaseAll(ctx context.Context) {
	lm.mu.Lock()
	leases := make(map[string]*activeLease, len(lm.locks))
	for k, v := range lm.locks {
		leases[k] = v
	}
	lm.locks = make(map[string]*activeLease)
	lm.mu.Unlock()

	for key, lease := range leases {
		lease.cancel()
		if err := lease.lock.Release(ctx); err != nil {
			lm.logger.Warn("failed to release lease during shutdown", "key", key, "error", err)
		}
	}
}

// HeldCount returns the number of currently held leases.
func (lm *LeaseManager) HeldCount() int {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return len(lm.locks)
}

// renewLoop periodically refreshes the lock until the context is cancelled.
func (lm *LeaseManager) renewLoop(ctx context.Context, key string, lock *redislock.Lock) {
	ticker := time.NewTicker(lm.renewEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := lock.Refresh(ctx, lm.lockTTL, nil); err != nil {
				if ctx.Err() != nil {
					return
				}
				lm.logger.Error("lease renewal failed", "key", key, "error", err)
				lm.mu.Lock()
				delete(lm.locks, key)
				lm.mu.Unlock()
				return
			}
		}
	}
}
