// Package worker contains background workers for the operational gateway service.
package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// Default configuration values for the expiry worker.
const (
	defaultExpiryScanInterval = 30 * time.Second
	defaultExpiryBatchSize    = 100
	defaultLeaseTimeout       = 5 * time.Minute
)

// Reason and error code recorded when a stuck DISPATCHING instruction is reclaimed.
const (
	leaseExpiredReason    = "dispatch lease expired: reclaimed by reaper"
	leaseExpiredErrorCode = "DISPATCH_LEASE_EXPIRED"
)

// ExpiryWorkerConfig configures the expiry worker's scan behavior.
type ExpiryWorkerConfig struct {
	// ScanInterval is the duration between successive expiry scan cycles.
	ScanInterval time.Duration
	// BatchSize is the maximum number of expired instructions to process per scan cycle.
	BatchSize int
	// LeaseTimeout is how long an instruction may remain in DISPATCHING before it is
	// considered stuck (the claiming worker crashed) and reclaimed to RETRYING.
	LeaseTimeout time.Duration
}

// applyDefaults fills in zero-valued fields with sensible defaults.
func (c *ExpiryWorkerConfig) applyDefaults() {
	if c.ScanInterval <= 0 {
		c.ScanInterval = defaultExpiryScanInterval
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultExpiryBatchSize
	}
	if c.LeaseTimeout <= 0 {
		c.LeaseTimeout = defaultLeaseTimeout
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
			"lease_timeout", w.config.LeaseTimeout,
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
			w.reapStuckDispatching(ctx)
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

		outcome := w.expireInstruction(ctx, instr)
		switch outcome {
		case expiryExpired:
			expired++
		case expirySkipped:
			skipped++
		case expiryFailed:
			failed++
		}
	}

	w.logger.InfoContext(ctx, "expiry batch completed",
		"expired", expired,
		"skipped", skipped,
		"failed", failed,
		"total", len(instructions),
	)
}

// expiryOutcome represents the result of attempting to expire a single instruction.
type expiryOutcome int

const (
	expiryExpired expiryOutcome = iota
	expirySkipped
	expiryFailed
)

// expireInstruction attempts to transition a single instruction to EXPIRED status.
func (w *ExpiryWorker) expireInstruction(ctx context.Context, instr *domain.Instruction) expiryOutcome {
	if instr.IsTerminal() {
		return expirySkipped
	}

	if err := instr.MarkExpired(); err != nil {
		w.logger.ErrorContext(ctx, "failed to mark instruction expired",
			"instruction_id", instr.ID,
			"status", instr.Status,
			"error", err,
		)
		return expiryFailed
	}

	if err := w.instructionRepo.Save(ctx, instr, ""); err != nil {
		w.logger.ErrorContext(ctx, "failed to save expired instruction",
			"instruction_id", instr.ID,
			"error", err,
		)
		return expiryFailed
	}

	w.logger.InfoContext(ctx, "instruction expired",
		"instruction_id", instr.ID,
		"tenant_id", instr.TenantID,
		"instruction_type", instr.InstructionType,
		"expires_at", instr.ExpiresAt,
	)
	return expiryExpired
}

// reapStuckDispatching finds instructions stuck in DISPATCHING past the lease timeout -
// evidence that the worker which claimed them crashed or stalled - and reclaims each for
// another dispatch attempt, or marks it FAILED when its retry budget is exhausted.
func (w *ExpiryWorker) reapStuckDispatching(ctx context.Context) {
	instructions, err := w.instructionRepo.FindStuckDispatching(ctx, w.config.LeaseTimeout, w.config.BatchSize)
	if err != nil {
		w.logger.ErrorContext(ctx, "failed to find stuck dispatching instructions", "error", err)
		return
	}
	if len(instructions) == 0 {
		return
	}

	w.logger.InfoContext(ctx, "processing stuck dispatch batch", "count", len(instructions))

	var retrying, failed, errored int
	for _, instr := range instructions {
		if ctx.Err() != nil {
			w.logger.InfoContext(ctx, "stuck dispatch batch interrupted by context cancellation")
			return
		}
		switch w.reclaimInstruction(ctx, instr) {
		case reclaimRetrying:
			retrying++
		case reclaimExhausted:
			failed++
		case reclaimError:
			errored++
		case reclaimSkipped:
			// No longer DISPATCHING (e.g. concurrently progressed); nothing to count.
		}
	}

	w.logger.InfoContext(ctx, "stuck dispatch batch completed",
		"retrying", retrying,
		"failed", failed,
		"errored", errored,
		"total", len(instructions),
	)
}

// reclaimOutcome represents the result of reclaiming a single stuck instruction.
type reclaimOutcome int

const (
	reclaimRetrying  reclaimOutcome = iota // reclaimed to RETRYING for another attempt
	reclaimExhausted                       // retry budget exhausted; marked FAILED
	reclaimError                           // transition or save failed
	reclaimSkipped                         // no longer DISPATCHING; nothing to do
)

// reclaimInstruction transitions a single stuck DISPATCHING instruction back to RETRYING
// (eligible for immediate re-dispatch) or to FAILED when no retry attempts remain.
func (w *ExpiryWorker) reclaimInstruction(ctx context.Context, instr *domain.Instruction) reclaimOutcome {
	if instr.Status != domain.InstructionStatusDispatching {
		return reclaimSkipped
	}

	outcome := reclaimRetrying
	if instr.CanRetry() {
		if err := instr.MarkRetrying(leaseExpiredReason, leaseExpiredErrorCode); err != nil {
			w.logger.ErrorContext(ctx, "failed to reclaim stuck instruction",
				"instruction_id", instr.ID, "error", err)
			return reclaimError
		}
		instr.NextRetryAt = nil // reclaimed instructions are eligible for immediate re-dispatch
	} else {
		if err := instr.MarkFailed(leaseExpiredReason, leaseExpiredErrorCode); err != nil {
			w.logger.ErrorContext(ctx, "failed to fail stuck instruction",
				"instruction_id", instr.ID, "error", err)
			return reclaimError
		}
		outcome = reclaimExhausted
	}

	if err := w.instructionRepo.Save(ctx, instr, ""); err != nil {
		w.logger.ErrorContext(ctx, "failed to save reclaimed instruction",
			"instruction_id", instr.ID, "error", err)
		return reclaimError
	}

	w.logger.InfoContext(ctx, "reclaimed stuck dispatching instruction",
		"instruction_id", instr.ID,
		"status", instr.Status,
		"attempt_count", instr.AttemptCount,
	)
	return outcome
}
