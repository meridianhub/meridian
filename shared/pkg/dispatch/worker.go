package dispatch

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Default worker configuration values.
const (
	DefaultBatchSize    = 50
	DefaultPollInterval = 1 * time.Second
)

// WorkerConfig configures the dispatch worker's polling and processing behavior.
type WorkerConfig struct {
	// BatchSize is the maximum number of instructions to claim per poll cycle.
	BatchSize int
	// PollInterval is the duration between successive poll cycles.
	PollInterval time.Duration
}

// applyDefaults fills in zero-valued fields with sensible defaults.
func (c *WorkerConfig) applyDefaults() {
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultBatchSize
	}
	if c.PollInterval <= 0 {
		c.PollInterval = DefaultPollInterval
	}
}

// BatchProcessor is the callback invoked by the Worker for each batch of instructions.
// Implementations contain the domain-specific dispatch logic (route resolution,
// circuit breaker checks, actual dispatch, outcome handling).
type BatchProcessor[I DispatchableInstruction] func(ctx context.Context, instructions []I)

// Worker implements the generic poll-dispatch-ack loop. It periodically fetches
// batches of dispatchable instructions and delegates processing to a BatchProcessor
// callback. The Worker manages the polling lifecycle (start, stop, shutdown) while
// leaving domain-specific dispatch logic to the callback.
//
// Worker is safe for concurrent use; multiple instances can run against the same
// database when the InstructionFetcher uses row-level locking (e.g., FOR UPDATE SKIP LOCKED).
type Worker[I DispatchableInstruction] struct {
	fetcher      InstructionFetcher[I]
	processor    BatchProcessor[I]
	config       WorkerConfig
	logger       *slog.Logger
	shutdown     chan struct{}
	shutdownOnce sync.Once
	startOnce    sync.Once
	wg           sync.WaitGroup
}

// NewWorker creates a new Worker with the given fetcher, processor callback, and config.
func NewWorker[I DispatchableInstruction](
	fetcher InstructionFetcher[I],
	processor BatchProcessor[I],
	config WorkerConfig,
	logger *slog.Logger,
) *Worker[I] {
	if logger == nil {
		logger = slog.Default()
	}
	config.applyDefaults()

	return &Worker[I]{
		fetcher:   fetcher,
		processor: processor,
		config:    config,
		logger:    logger.With("component", "dispatch-worker"),
		shutdown:  make(chan struct{}),
	}
}

// Start begins the background polling loop. Returns immediately; the loop runs
// in a separate goroutine until Stop is called or ctx is cancelled.
// Calling Start more than once is a no-op.
func (w *Worker[I]) Start(ctx context.Context) {
	w.startOnce.Do(func() {
		w.wg.Add(1)
		go w.run(ctx)

		w.logger.InfoContext(ctx, "dispatch worker started",
			"batch_size", w.config.BatchSize,
			"poll_interval", w.config.PollInterval,
		)
	})
}

// Stop signals the worker to shut down and blocks until the current batch completes.
// Safe to call multiple times.
func (w *Worker[I]) Stop() {
	w.shutdownOnce.Do(func() {
		w.logger.Info("dispatch worker stopping")
		close(w.shutdown)
	})
	w.wg.Wait()
	w.logger.Info("dispatch worker stopped")
}

// run is the main polling loop.
func (w *Worker[I]) run(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.InfoContext(ctx, "dispatch worker context cancelled")
			return
		case <-w.shutdown:
			w.logger.Info("dispatch worker shutdown signal received")
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

// processBatch fetches a batch of dispatchable instructions and delegates to the processor.
func (w *Worker[I]) processBatch(ctx context.Context) {
	instructions, err := w.fetcher.FetchDispatchable(ctx, w.config.BatchSize)
	if err != nil {
		w.logger.ErrorContext(ctx, "failed to fetch dispatchable instructions", "error", err)
		return
	}
	if len(instructions) == 0 {
		return
	}

	w.logger.InfoContext(ctx, "processing dispatch batch", "count", len(instructions))
	w.processor(ctx, instructions)
}
