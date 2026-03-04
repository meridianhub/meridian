// Package worker provides background workers for the event-router service.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sharedidempotency "github.com/meridianhub/meridian/shared/pkg/idempotency"
)

// IdempotencyCleanupConfig holds configuration for the idempotency cleanup worker.
type IdempotencyCleanupConfig struct {
	// CleanupInterval is how often expired idempotency keys are purged.
	// Default: 60s
	CleanupInterval time.Duration
}

// DefaultIdempotencyCleanupConfig returns the default cleanup configuration.
func DefaultIdempotencyCleanupConfig() IdempotencyCleanupConfig {
	return IdempotencyCleanupConfig{
		CleanupInterval: 60 * time.Second,
	}
}

// Errors returned by the cleanup worker.
var (
	ErrNilPool         = errors.New("pool cannot be nil")
	ErrNilLogger       = errors.New("logger cannot be nil")
	ErrInvalidInterval = errors.New("cleanup interval must be greater than zero")
	ErrAlreadyRunning  = errors.New("worker is already running")
)

// IdempotencyCleanupWorker periodically purges expired idempotency keys from the
// _idempotency_keys table. Keys are created with an expires_at TTL; this worker
// ensures they are removed promptly rather than accumulating indefinitely.
type IdempotencyCleanupWorker struct {
	svc    *sharedidempotency.PostgresService
	config IdempotencyCleanupConfig
	logger *slog.Logger

	mu      sync.Mutex
	running bool
	done    chan struct{}
}

// NewIdempotencyCleanupWorker creates a new cleanup worker.
//
// Parameters:
//   - pool: pgxpool.Pool connected to the CockroachDB/PostgreSQL database.
//   - cfg:  Worker configuration.
//   - logger: Structured logger.
func NewIdempotencyCleanupWorker(
	pool *pgxpool.Pool,
	cfg IdempotencyCleanupConfig,
	logger *slog.Logger,
) (*IdempotencyCleanupWorker, error) {
	if pool == nil {
		return nil, ErrNilPool
	}
	if logger == nil {
		return nil, ErrNilLogger
	}
	if cfg.CleanupInterval <= 0 {
		return nil, ErrInvalidInterval
	}

	svc := sharedidempotency.NewPostgresService(
		pool,
		sharedidempotency.WithCleanupInterval(cfg.CleanupInterval),
	)

	return &IdempotencyCleanupWorker{
		svc:    svc,
		config: cfg,
		logger: logger.With("component", "idempotency_cleanup_worker"),
		done:   make(chan struct{}),
	}, nil
}

// Start begins the background cleanup loop.
// It blocks until ctx is cancelled or Stop() is called.
// The cleanup runs immediately on first tick and then at each CleanupInterval.
//
// Returns ErrAlreadyRunning if Start is called while already running.
// Start can be called again after Stop() returns; the done channel is
// re-initialized so the worker is restartable.
func (w *IdempotencyCleanupWorker) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return ErrAlreadyRunning
	}
	// Re-initialize the done channel so the worker is restartable after Stop().
	w.done = make(chan struct{})
	w.running = true
	w.mu.Unlock()

	w.logger.Info("idempotency cleanup worker started",
		"cleanup_interval", w.config.CleanupInterval,
	)

	// Use a child context so that Stop() can cancel the cleanup goroutine
	// independently of the parent context.
	cleanupCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Wire the done channel to cancel the cleanup context.
	go func() {
		select {
		case <-w.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	// StartCleanup runs the periodic deletion in its own goroutine and returns immediately.
	// It stops when cleanupCtx is cancelled.
	w.svc.StartCleanup(cleanupCtx)

	// Block until shutdown signal.
	select {
	case <-cleanupCtx.Done():
	case <-w.done:
	}

	w.mu.Lock()
	w.running = false
	w.mu.Unlock()

	w.logger.Info("idempotency cleanup worker stopped")
	return nil
}

// Running reports whether the worker is currently running.
// Safe to call from any goroutine.
func (w *IdempotencyCleanupWorker) Running() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running
}

// Stop signals the worker to shut down.
// It is safe to call Stop multiple times.
func (w *IdempotencyCleanupWorker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	select {
	case <-w.done:
		// Already closed
	default:
		close(w.done)
	}
}
