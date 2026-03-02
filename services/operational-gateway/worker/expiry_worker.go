// Package worker contains background workers for the operational gateway service.
package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// Default configuration values for the expiry worker.
const (
	defaultExpiryScanInterval = 30 * time.Second
	defaultExpiryBatchSize    = 100
)

// ExpiryWorkerConfig configures the expiry worker's scan behavior.
type ExpiryWorkerConfig struct {
	// ScanInterval is the duration between successive expiry scan cycles.
	ScanInterval time.Duration
	// BatchSize is the maximum number of expired instructions to process per scan cycle.
	BatchSize int
}

// applyDefaults fills in zero-valued fields with sensible defaults.
func (c *ExpiryWorkerConfig) applyDefaults() {
	if c.ScanInterval <= 0 {
		c.ScanInterval = defaultExpiryScanInterval
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultExpiryBatchSize
	}
}

// ExpiryWorker periodically scans for instructions whose TTL has elapsed and transitions
// them to EXPIRED status. It targets PENDING and RETRYING instructions with a non-null
// expires_at in the past.
//
// ExpiryWorker is safe for concurrent use; multiple instances can run against the same
// database because each instruction is updated with optimistic locking, so concurrent
// workers will produce at most one EXPIRED transition per instruction.
type ExpiryWorker struct {
	instructionRepo ports.InstructionRepository
	config          ExpiryWorkerConfig
	logger          *slog.Logger
	shutdown        chan struct{}
	shutdownOnce    sync.Once
	startOnce       sync.Once
	wg              sync.WaitGroup
}

// NewExpiryWorker creates a new ExpiryWorker with the given dependencies and config.
func NewExpiryWorker(
	instructionRepo ports.InstructionRepository,
	config ExpiryWorkerConfig,
	logger *slog.Logger,
) *ExpiryWorker {
	if logger == nil {
		logger = slog.Default()
	}
	config.applyDefaults()

	return &ExpiryWorker{
		instructionRepo: instructionRepo,
		config:          config,
		logger:          logger.With("component", "expiry-worker"),
		shutdown:        make(chan struct{}),
	}
}

// Start begins the background scan loop. Returns immediately; the loop runs in a separate
// goroutine until Stop is called or ctx is cancelled.
// Calling Start more than once is a no-op.
func (w *ExpiryWorker) Start(ctx context.Context) {
	w.startOnce.Do(func() {
		w.wg.Add(1)
		go w.run(ctx)

		w.logger.InfoContext(ctx, "expiry worker started",
			"batch_size", w.config.BatchSize,
			"scan_interval", w.config.ScanInterval,
		)
	})
}

// Stop signals the worker to shut down and blocks until the current scan completes.
// Safe to call multiple times.
func (w *ExpiryWorker) Stop() {
	w.shutdownOnce.Do(func() {
		w.logger.Info("expiry worker stopping")
		close(w.shutdown)
	})
	w.wg.Wait()
	w.logger.Info("expiry worker stopped")
}

// run is the main scan loop.
func (w *ExpiryWorker) run(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.config.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.InfoContext(ctx, "expiry worker context cancelled")
			return
		case <-w.shutdown:
			w.logger.Info("expiry worker shutdown signal received")
			return
		case <-ticker.C:
			w.scanAndExpire(ctx)
		}
	}
}

// scanAndExpire fetches a batch of expired instructions and transitions each to EXPIRED.
func (w *ExpiryWorker) scanAndExpire(ctx context.Context) {
	instructions, err := w.instructionRepo.FindExpired(ctx, w.config.BatchSize)
	if err != nil {
		w.logger.ErrorContext(ctx, "failed to find expired instructions", "error", err)
		return
	}
	if len(instructions) == 0 {
		return
	}

	w.logger.InfoContext(ctx, "processing expiry batch", "count", len(instructions))

	var expired, skipped, failed int
	for _, instr := range instructions {
		if ctx.Err() != nil {
			w.logger.InfoContext(ctx, "expiry batch interrupted by context cancellation",
				"expired", expired,
				"skipped", skipped,
				"failed", failed,
				"remaining", len(instructions)-expired-skipped-failed,
			)
			return
		}

		if instr.IsTerminal() {
			// The instruction reached a terminal state between the query and now.
			skipped++
			continue
		}

		if err := instr.MarkExpired(); err != nil {
			w.logger.ErrorContext(ctx, "failed to mark instruction expired",
				"instruction_id", instr.ID,
				"status", instr.Status,
				"error", err,
			)
			failed++
			continue
		}

		if err := w.instructionRepo.Save(ctx, instr, ""); err != nil {
			w.logger.ErrorContext(ctx, "failed to save expired instruction",
				"instruction_id", instr.ID,
				"error", err,
			)
			failed++
			continue
		}

		w.logger.InfoContext(ctx, "instruction expired",
			"instruction_id", instr.ID,
			"tenant_id", instr.TenantID,
			"instruction_type", instr.InstructionType,
			"expires_at", instr.ExpiresAt,
		)
		expired++
	}

	w.logger.InfoContext(ctx, "expiry batch completed",
		"expired", expired,
		"skipped", skipped,
		"failed", failed,
		"total", len(instructions),
	)
}
