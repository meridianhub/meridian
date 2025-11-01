package db

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// HealthChecker manages health monitoring for database connection pools.
// It provides periodic health checks, connection statistics, and readiness probes
// suitable for Kubernetes liveness/readiness endpoints.
type HealthChecker struct {
	// pool is the database connection pool being monitored
	pool *PostgresPool
	// checkInterval is how often to run background health checks
	checkInterval time.Duration
	// checkTimeout is the maximum duration for a single health check
	checkTimeout time.Duration
	// lastCheckTime is the timestamp of the most recent health check
	lastCheckTime time.Time
	// lastCheckErr is the error from the most recent health check (nil if healthy)
	lastCheckErr error
	// mu protects concurrent access to lastCheckTime and lastCheckErr
	mu sync.RWMutex
	// ctx provides cancellation signal for graceful shutdown
	ctx context.Context
	// cancel triggers shutdown of the periodic health check goroutine
	cancel context.CancelFunc
	// wg tracks the PeriodicHealthCheck goroutine for graceful shutdown
	wg sync.WaitGroup
}

// HealthCheckConfig contains configuration for health monitoring.
type HealthCheckConfig struct {
	// CheckInterval is how often to run background health checks.
	// Default: 30s. Lower values detect failures faster but increase database load.
	CheckInterval time.Duration
	// CheckTimeout is the maximum duration for a single health check.
	// Default: 5s. Should be less than CheckInterval.
	CheckTimeout time.Duration
}

// PoolStats contains statistics about the connection pool.
// These metrics are useful for monitoring pool health and capacity planning.
type PoolStats struct {
	// MaxOpenConnections is the maximum number of open connections to the database.
	MaxOpenConnections int
	// OpenConnections is the number of established connections both in use and idle.
	OpenConnections int
	// InUse is the number of connections currently in use.
	InUse int
	// Idle is the number of idle connections.
	Idle int
	// WaitCount is the total number of connections waited for.
	WaitCount int64
	// WaitDuration is the total time blocked waiting for new connections.
	WaitDuration time.Duration
	// MaxIdleClosed is the total number of connections closed due to max idle.
	MaxIdleClosed int64
	// MaxIdleTimeClosed is the total number of connections closed due to max idle time.
	MaxIdleTimeClosed int64
	// MaxLifetimeClosed is the total number of connections closed due to max lifetime.
	MaxLifetimeClosed int64
}

// NewHealthChecker creates a new health checker for the specified pool.
// It applies default configuration if not provided:
// - CheckInterval: 30s
// - CheckTimeout: 5s
//
// The health checker does NOT start background checks automatically.
// Call PeriodicHealthCheck() to start background monitoring.
func NewHealthChecker(pool *PostgresPool, config *HealthCheckConfig) *HealthChecker {
	if config == nil {
		config = &HealthCheckConfig{}
	}

	// Set defaults
	if config.CheckInterval == 0 {
		config.CheckInterval = 30 * time.Second
	}
	if config.CheckTimeout == 0 {
		config.CheckTimeout = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &HealthChecker{
		pool:          pool,
		checkInterval: config.CheckInterval,
		checkTimeout:  config.CheckTimeout,
		ctx:           ctx,
		cancel:        cancel,
	}
}

// PeriodicHealthCheck runs background health checks at configured intervals.
// This method blocks until Stop() is called or the context is cancelled.
// It performs the following:
// - Executes Ping() every CheckInterval
// - Updates lastCheckTime and lastCheckErr
// - Logs errors for monitoring/alerting integration
//
// Call this in a goroutine to enable continuous health monitoring:
//
//	go healthChecker.PeriodicHealthCheck()
//
// To stop the periodic checks:
//
//	healthChecker.Stop()
func (h *HealthChecker) PeriodicHealthCheck() {
	// Track this goroutine for graceful shutdown
	h.wg.Add(1)
	defer h.wg.Done()

	ticker := time.NewTicker(h.checkInterval)
	defer ticker.Stop()

	// Run initial check immediately
	h.runHealthCheck()

	for {
		select {
		case <-h.ctx.Done():
			log.Printf("INFO: Stopping periodic health checks")
			return
		case <-ticker.C:
			h.runHealthCheck()
		}
	}
}

// runHealthCheck executes a single health check with timeout.
// Updates lastCheckTime and lastCheckErr with the results.
// Logs errors for monitoring integration.
func (h *HealthChecker) runHealthCheck() {
	ctx, cancel := context.WithTimeout(h.ctx, h.checkTimeout)
	defer cancel()

	err := h.pool.Ping(ctx)

	h.mu.Lock()
	h.lastCheckTime = time.Now()
	h.lastCheckErr = err
	h.mu.Unlock()

	if err != nil {
		log.Printf("WARN: Database health check failed: %v", err)
	}
}

// IsHealthy returns true if the pool is healthy based on the most recent check.
// This method is suitable for Kubernetes readiness probes.
//
// A pool is considered healthy if:
// - A health check has been performed
// - The most recent check succeeded (lastCheckErr is nil)
// - The most recent check was within a reasonable timeframe (2x CheckInterval)
//
// Returns false if no checks have been performed or the most recent check failed.
func (h *HealthChecker) IsHealthy() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// If no check has been performed yet, not healthy
	if h.lastCheckTime.IsZero() {
		return false
	}

	// If last check had an error, not healthy
	if h.lastCheckErr != nil {
		return false
	}

	// If last check was too long ago (2x interval), not healthy
	// This catches cases where the periodic check goroutine has stopped
	staleDuration := h.checkInterval * 2
	if time.Since(h.lastCheckTime) > staleDuration {
		return false
	}

	return true
}

// GetLastCheckTime returns the timestamp of the most recent health check.
// Returns zero time if no checks have been performed.
func (h *HealthChecker) GetLastCheckTime() time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastCheckTime
}

// GetLastCheckError returns the error from the most recent health check.
// Returns nil if the last check succeeded or no checks have been performed.
func (h *HealthChecker) GetLastCheckError() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastCheckErr
}

// GetStats returns current connection pool statistics.
// These metrics are useful for:
// - Monitoring pool utilization
// - Capacity planning (do we need more connections?)
// - Detecting connection leaks (high WaitCount, low Idle)
// - Performance tuning (high WaitDuration suggests pool too small)
func (h *HealthChecker) GetStats() PoolStats {
	stats := h.pool.Stats()

	return PoolStats{
		MaxOpenConnections: stats.MaxOpenConnections,
		OpenConnections:    stats.OpenConnections,
		InUse:              stats.InUse,
		Idle:               stats.Idle,
		WaitCount:          stats.WaitCount,
		WaitDuration:       stats.WaitDuration,
		MaxIdleClosed:      stats.MaxIdleClosed,
		MaxIdleTimeClosed:  stats.MaxIdleTimeClosed,
		MaxLifetimeClosed:  stats.MaxLifetimeClosed,
	}
}

// Check performs a synchronous health check with the specified context.
// This is useful for on-demand health checks (e.g., Kubernetes liveness probes)
// rather than relying on the periodic background checks.
//
// Returns nil if the pool is healthy, error otherwise.
func (h *HealthChecker) Check(ctx context.Context) error {
	if err := h.pool.Ping(ctx); err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	return nil
}

// Stop stops the periodic health check goroutine gracefully.
// This triggers the PeriodicHealthCheck() loop to exit and waits for it to finish.
// Safe to call multiple times.
func (h *HealthChecker) Stop() {
	h.cancel()
	h.wg.Wait()
}
