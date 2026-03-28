// Package worker provides background workers for the financial-accounting service.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/financial-accounting/config"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

// Default service name for metrics when namespace cannot be determined.
const defaultMetricsServiceName = "financial-accounting"

// IdempotencyCleanupWorker is a background worker that detects and marks
// timed-out PENDING idempotency keys as FAILED.
//
// This prevents keys from being stuck in PENDING state indefinitely when
// the original request failed without completing (e.g., service crash,
// network failure, or panic during processing).
type IdempotencyCleanupWorker struct {
	cleaner idempotency.Cleaner
	config  config.IdempotencyCleanupConfig
	logger  *slog.Logger
	metrics *idempotency.MetricsCollector
	done    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	running bool
	stopped bool // guards wg.Add/Wait race
}

// Errors returned by the cleanup worker.
var (
	ErrNilCleaner       = errors.New("cleaner cannot be nil")
	ErrNilLogger        = errors.New("logger cannot be nil")
	ErrInvalidInterval  = errors.New("run interval must be greater than zero")
	ErrInvalidThreshold = errors.New("stale threshold must be greater than zero")
	ErrInvalidBatchSize = errors.New("batch size must be greater than zero")
	ErrAlreadyRunning   = errors.New("worker is already running")
)

// NewIdempotencyCleanupWorker creates a new cleanup worker.
//
// Parameters:
//   - cleaner: The idempotency cleaner (typically RedisService)
//   - cfg: Worker configuration
//   - logger: Structured logger
//
// Returns an error if any required parameter is invalid.
func NewIdempotencyCleanupWorker(
	cleaner idempotency.Cleaner,
	cfg config.IdempotencyCleanupConfig,
	logger *slog.Logger,
) (*IdempotencyCleanupWorker, error) {
	return NewIdempotencyCleanupWorkerWithMetrics(cleaner, cfg, logger, nil)
}

// NewIdempotencyCleanupWorkerWithMetrics creates a new cleanup worker with Prometheus metrics.
//
// Parameters:
//   - cleaner: The idempotency cleaner (typically RedisService)
//   - cfg: Worker configuration
//   - logger: Structured logger
//   - metrics: Optional metrics collector (nil disables metrics)
//
// Returns an error if any required parameter is invalid.
func NewIdempotencyCleanupWorkerWithMetrics(
	cleaner idempotency.Cleaner,
	cfg config.IdempotencyCleanupConfig,
	logger *slog.Logger,
	metrics *idempotency.MetricsCollector,
) (*IdempotencyCleanupWorker, error) {
	if cleaner == nil {
		return nil, ErrNilCleaner
	}
	if logger == nil {
		return nil, ErrNilLogger
	}
	if cfg.RunInterval <= 0 {
		return nil, ErrInvalidInterval
	}
	if cfg.StaleThreshold <= 0 {
		return nil, ErrInvalidThreshold
	}
	if cfg.BatchSize <= 0 {
		return nil, ErrInvalidBatchSize
	}

	return &IdempotencyCleanupWorker{
		cleaner: cleaner,
		config:  cfg,
		logger:  logger.With("component", "idempotency_cleanup_worker"),
		metrics: metrics,
		done:    make(chan struct{}),
	}, nil
}

// Start begins the background cleanup loop.
// It runs until ctx is cancelled or Stop() is called.
// The method blocks and should be run in a separate goroutine.
//
// Returns ErrAlreadyRunning if Start is called while already running.
func (w *IdempotencyCleanupWorker) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return ErrAlreadyRunning
	}
	w.running = true
	w.mu.Unlock()

	w.logger.Info("idempotency cleanup worker started",
		"run_interval", w.config.RunInterval,
		"stale_threshold", w.config.StaleThreshold,
		"batch_size", w.config.BatchSize,
		"key_pattern", w.config.KeyPattern)

	ticker := time.NewTicker(w.config.RunInterval)
	defer ticker.Stop()

	// Run initial cleanup immediately
	// Add to WaitGroup before calling to prevent race with Stop()
	w.wg.Add(1)
	w.runCleanupIteration(ctx)
	w.wg.Done()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("idempotency cleanup worker stopped: context cancelled")
			w.markStopped()
			return nil
		case <-w.done:
			w.logger.Info("idempotency cleanup worker stopped: explicit shutdown")
			w.markStopped()
			return nil
		case <-ticker.C:
			// Use tryStartIteration to safely add to WaitGroup only if not stopped
			if w.tryStartIteration() {
				w.runCleanupIteration(ctx)
				w.wg.Done()
			} else {
				// Worker is stopping, exit the loop
				w.logger.Info("idempotency cleanup worker stopped: explicit shutdown")
				w.markStopped()
				return nil
			}
		}
	}
}

// markStopped safely marks the worker as not running.
func (w *IdempotencyCleanupWorker) markStopped() {
	w.mu.Lock()
	w.running = false
	w.mu.Unlock()
}

// tryStartIteration attempts to start a new cleanup iteration.
// Returns true if the iteration can proceed (wg.Add(1) was called).
// Returns false if the worker is stopping (do not proceed with iteration).
// This method prevents the race between wg.Add and wg.Wait.
func (w *IdempotencyCleanupWorker) tryStartIteration() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return false
	}
	w.wg.Add(1)
	return true
}

// Stop signals the worker to shut down gracefully.
// It waits for the current cleanup iteration to complete.
// It is safe to call Stop multiple times.
func (w *IdempotencyCleanupWorker) Stop() {
	// Mark as stopped under lock to prevent new iterations from starting
	w.mu.Lock()
	alreadyStopped := w.stopped
	w.stopped = true
	w.mu.Unlock()

	if !alreadyStopped {
		select {
		case <-w.done:
			// Already closed
		default:
			close(w.done)
		}
	}

	// Wait for in-flight cleanup operations to complete
	// This is safe because tryStartIteration won't call wg.Add after stopped=true
	w.wg.Wait()
	w.logger.Info("idempotency cleanup worker shutdown complete")
}

// runCleanupIteration performs one cleanup pass.
// It scans for stale PENDING keys and marks them as FAILED.
// The caller must manage WaitGroup to prevent races with Stop().
func (w *IdempotencyCleanupWorker) runCleanupIteration(ctx context.Context) {
	// Check for context cancellation before starting
	select {
	case <-ctx.Done():
		return
	default:
	}

	w.logger.Debug("starting cleanup iteration")
	start := time.Now()

	var totalProcessed, totalFailed int
	var iterationErrors []error
	staleCountByService := make(map[string]int)

	// Process in batches until no more stale keys found
	for {
		if w.shouldStopIteration(ctx) {
			break
		}

		staleKeys, err := w.cleaner.ScanStalePendingKeys(
			ctx, w.config.KeyPattern, w.config.StaleThreshold, w.config.BatchSize,
		)
		if err != nil {
			w.logger.Error("failed to scan for stale keys", "error", err)
			iterationErrors = append(iterationErrors, err)
			break
		}
		if len(staleKeys) == 0 {
			break
		}

		processed, failed, errs := w.processStaleKeyBatch(ctx, staleKeys, staleCountByService)
		totalProcessed += processed
		totalFailed += failed
		iterationErrors = append(iterationErrors, errs...)

		if len(staleKeys) < w.config.BatchSize {
			break
		}
	}

	w.updateStaleGauges(staleCountByService)
	w.logIterationComplete(start, totalProcessed, totalFailed, iterationErrors)
}

// shouldStopIteration checks if the cleanup loop should stop due to shutdown signals.
func (w *IdempotencyCleanupWorker) shouldStopIteration(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	case <-w.done:
		return true
	default:
		return false
	}
}

// processStaleKeyBatch processes a batch of stale keys and updates service counts.
// Returns the number of successfully processed keys, failed keys, and any errors.
func (w *IdempotencyCleanupWorker) processStaleKeyBatch(
	ctx context.Context,
	staleKeys []idempotency.StalePendingKey,
	staleCountByService map[string]int,
) (processed, failed int, errs []error) {
	w.logger.Info("found stale PENDING keys",
		"count", len(staleKeys),
		"batch_size", w.config.BatchSize)

	// Count stale keys by service for metrics
	for _, staleKey := range staleKeys {
		service := w.getServiceFromKey(staleKey)
		staleCountByService[service]++
	}

	for _, staleKey := range staleKeys {
		if err := w.processStaleKey(ctx, staleKey); err != nil {
			failed++
			errs = append(errs, err)
		} else {
			processed++
			service := w.getServiceFromKey(staleKey)
			staleCountByService[service]--
		}
	}
	return processed, failed, errs
}

// processStaleKey marks a single stale key as FAILED.
func (w *IdempotencyCleanupWorker) processStaleKey(ctx context.Context, staleKey idempotency.StalePendingKey) error {
	reason := "timeout: operation exceeded stale threshold"

	if err := w.cleaner.MarkStaleAsFailed(ctx, staleKey, reason); err != nil {
		w.logger.Error("failed to mark stale key as FAILED",
			"redis_key", staleKey.RedisKey,
			"age", staleKey.Age,
			"error", err)
		return err
	}

	// Record cleanup metric
	service := w.getServiceFromKey(staleKey)
	if w.metrics != nil {
		w.metrics.RecordCleanedUp(service)
	} else {
		// Use global function if no collector is configured
		idempotency.RecordIdempotencyCleanedUp(service)
	}

	w.logger.Info("marked stale PENDING key as FAILED",
		"redis_key", staleKey.RedisKey,
		"age", staleKey.Age,
		"namespace", staleKey.Result.Key.Namespace,
		"operation", staleKey.Result.Key.Operation,
		"entity_id", staleKey.Result.Key.EntityID)

	return nil
}

// getServiceFromKey extracts the service name from a stale key.
// Falls back to default service name if namespace is not available.
func (w *IdempotencyCleanupWorker) getServiceFromKey(staleKey idempotency.StalePendingKey) string {
	if staleKey.Result != nil && staleKey.Result.Key.Namespace != "" {
		return staleKey.Result.Key.Namespace
	}
	return defaultMetricsServiceName
}

// updateStaleGauges updates the stale pending gauge for each service.
func (w *IdempotencyCleanupWorker) updateStaleGauges(countByService map[string]int) {
	if w.metrics == nil {
		// Use global function if no collector is configured
		for service, count := range countByService {
			if count > 0 {
				idempotency.SetIdempotencyStalePendingCount(service, count)
			}
		}
		return
	}

	for service, count := range countByService {
		if count > 0 {
			w.metrics.SetStalePendingCount(service, count)
		}
	}
}

// logIterationComplete logs the summary of a cleanup iteration.
func (w *IdempotencyCleanupWorker) logIterationComplete(
	start time.Time,
	processed, failed int,
	errs []error,
) {
	duration := time.Since(start)

	if processed == 0 && failed == 0 {
		w.logger.Debug("cleanup iteration complete: no stale keys found",
			"duration", duration)
		return
	}

	if failed > 0 {
		w.logger.Warn("cleanup iteration complete with errors",
			"processed", processed,
			"failed", failed,
			"error_count", len(errs),
			"duration", duration)
	} else {
		w.logger.Info("cleanup iteration complete",
			"processed", processed,
			"duration", duration)
	}
}
