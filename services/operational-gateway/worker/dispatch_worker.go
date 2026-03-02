// Package worker contains background workers for the operational gateway service.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// Default configuration values.
const (
	defaultBatchSize    = 50
	defaultPollInterval = 1 * time.Second
)

// DispatchWorkerConfig configures the dispatch worker's polling and processing behavior.
type DispatchWorkerConfig struct {
	// BatchSize is the maximum number of instructions to claim per poll cycle.
	BatchSize int
	// PollInterval is the duration between successive poll cycles.
	PollInterval time.Duration
}

// applyDefaults fills in zero-valued fields with sensible defaults.
func (c *DispatchWorkerConfig) applyDefaults() {
	if c.BatchSize <= 0 {
		c.BatchSize = defaultBatchSize
	}
	if c.PollInterval <= 0 {
		c.PollInterval = defaultPollInterval
	}
}

// DispatchWorker polls for dispatchable instructions and sends them to external
// providers via the Dispatcher port. It integrates with the circuit breaker on each
// ProviderConnection and handles retry/failure transitions on the Instruction aggregate.
//
// FetchDispatchable (in the repository) already marks instructions as DISPATCHING
// within its transaction, so the worker does not call MarkDispatching again.
//
// DispatchWorker is safe for concurrent use; multiple instances can run against the
// same database because FetchDispatchable uses FOR UPDATE SKIP LOCKED.
type DispatchWorker struct {
	instructionRepo ports.InstructionRepository
	connectionRepo  ports.ConnectionRepository
	routeResolver   ports.RouteResolver
	dispatcher      ports.Dispatcher
	config          DispatchWorkerConfig
	logger          *slog.Logger
	shutdown        chan struct{}
	shutdownOnce    sync.Once
	startOnce       sync.Once
	wg              sync.WaitGroup
}

// NewDispatchWorker creates a new DispatchWorker with the given dependencies and config.
func NewDispatchWorker(
	instructionRepo ports.InstructionRepository,
	connectionRepo ports.ConnectionRepository,
	routeResolver ports.RouteResolver,
	dispatcher ports.Dispatcher,
	config DispatchWorkerConfig,
	logger *slog.Logger,
) *DispatchWorker {
	if logger == nil {
		logger = slog.Default()
	}
	config.applyDefaults()

	return &DispatchWorker{
		instructionRepo: instructionRepo,
		connectionRepo:  connectionRepo,
		routeResolver:   routeResolver,
		dispatcher:      dispatcher,
		config:          config,
		logger:          logger.With("component", "dispatch-worker"),
		shutdown:        make(chan struct{}),
	}
}

// Start begins the background polling loop. Returns immediately; the loop runs
// in a separate goroutine until Stop is called or ctx is cancelled.
// Calling Start more than once is a no-op.
func (w *DispatchWorker) Start(ctx context.Context) {
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
func (w *DispatchWorker) Stop() {
	w.shutdownOnce.Do(func() {
		w.logger.Info("dispatch worker stopping")
		close(w.shutdown)
	})
	w.wg.Wait()
	w.logger.Info("dispatch worker stopped")
}

// run is the main polling loop.
func (w *DispatchWorker) run(ctx context.Context) {
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

// processBatch fetches a batch of dispatchable instructions and processes each one.
func (w *DispatchWorker) processBatch(ctx context.Context) {
	instructions, err := w.instructionRepo.FetchDispatchable(ctx, ports.FetchDispatchableParams{
		Limit: w.config.BatchSize,
	})
	if err != nil {
		w.logger.ErrorContext(ctx, "failed to fetch dispatchable instructions", "error", err)
		return
	}
	if len(instructions) == 0 {
		return
	}

	w.logger.InfoContext(ctx, "processing dispatch batch", "count", len(instructions))

	var processed, failed int
	for _, instr := range instructions {
		if ctx.Err() != nil {
			w.logger.InfoContext(ctx, "batch interrupted by context cancellation",
				"processed", processed,
				"failed", failed,
				"remaining", len(instructions)-processed-failed,
			)
			return
		}

		if err := w.processInstruction(ctx, instr); err != nil {
			w.logger.ErrorContext(ctx, "failed to process instruction",
				"instruction_id", instr.ID,
				"instruction_type", instr.InstructionType,
				"error", err,
			)
			failed++
		} else {
			processed++
		}
	}

	w.logger.InfoContext(ctx, "dispatch batch completed",
		"processed", processed,
		"failed", failed,
		"total", len(instructions),
	)
}

// processInstruction dispatches a single instruction through the full flow:
// resolve route -> look up connection -> check circuit breaker -> dispatch -> handle outcome.
//
// The instruction arrives already in DISPATCHING state (set by FetchDispatchable).
func (w *DispatchWorker) processInstruction(ctx context.Context, instr *domain.Instruction) error {
	// 1. Resolve route for this instruction type.
	route, err := w.routeResolver.Resolve(ctx, instr.TenantID.String(), instr.InstructionType)
	if err != nil {
		if errors.Is(err, ports.ErrRouteNotFound) {
			return w.handleFailure(ctx, instr, fmt.Sprintf("route resolution failed: %v", err), "ROUTE_NOT_FOUND")
		}
		// Transient error (e.g., DB/network) — return error so the instruction stays
		// DISPATCHING and will be picked up by the stuck-instruction reaper for retry.
		return fmt.Errorf("route resolution transient error: %w", err)
	}

	// 2. Look up the provider connection.
	conn, err := w.connectionRepo.FindByID(ctx, instr.TenantID.String(), instr.ProviderConnectionID)
	if err != nil {
		if errors.Is(err, ports.ErrConnectionNotFound) {
			return w.handleFailure(ctx, instr, fmt.Sprintf("connection lookup failed: %v", err), "CONNECTION_NOT_FOUND")
		}
		// Transient error — leave instruction in DISPATCHING for reaper.
		return fmt.Errorf("connection lookup transient error: %w", err)
	}

	// 3. Check circuit breaker.
	if !conn.IsAvailable() {
		return w.handleRetryOrFail(ctx, instr, conn, "circuit breaker open", "CIRCUIT_OPEN")
	}

	// 4. Dispatch to external provider.
	result := w.dispatcher.Dispatch(ctx, instr, conn, route)

	// 5. Handle transport-level error (no response received).
	if result.Error != nil {
		if err := conn.RecordFailure(conn.RetryPolicy.MaxAttempts); err != nil {
			w.logger.ErrorContext(ctx, "failed to record connection failure",
				"connection_id", conn.ConnectionID, "error", err,
			)
		}
		if saveErr := w.connectionRepo.UpdateHealth(ctx, conn); saveErr != nil {
			w.logger.ErrorContext(ctx, "failed to persist connection health",
				"connection_id", conn.ConnectionID, "error", saveErr,
			)
		}
		return w.handleRetryOrFail(ctx, instr, conn, fmt.Sprintf("dispatch error: %v", result.Error), "DISPATCH_ERROR")
	}

	// 6. Record success on the connection circuit breaker.
	conn.RecordSuccess()
	if saveErr := w.connectionRepo.UpdateHealth(ctx, conn); saveErr != nil {
		w.logger.ErrorContext(ctx, "failed to persist connection health",
			"connection_id", conn.ConnectionID, "error", saveErr,
		)
	}

	// 7. Handle the parsed outcome.
	if result.Outcome == nil {
		return w.handleFailure(ctx, instr, "dispatch returned no outcome", "NO_OUTCOME")
	}

	outcome := result.Outcome
	if outcome.ShouldRetry {
		return w.handleRetryOrFail(ctx, instr, conn, outcome.FailureReason, "PROVIDER_RETRY")
	}
	if outcome.FailureReason != "" {
		return w.handleFailure(ctx, instr, outcome.FailureReason, "PROVIDER_REJECTED")
	}

	// 8. Mark delivered.
	if err := instr.MarkDelivered(); err != nil {
		return fmt.Errorf("marking delivered: %w", err)
	}
	if err := w.instructionRepo.Save(ctx, instr, ""); err != nil {
		return fmt.Errorf("saving delivered instruction: %w", err)
	}

	w.logger.InfoContext(ctx, "instruction delivered",
		"instruction_id", instr.ID,
		"external_id", outcome.ExternalID,
		"duration_ms", result.Duration.Milliseconds(),
	)
	return nil
}

// handleRetryOrFail attempts to schedule a retry; if retries are exhausted it marks the
// instruction as permanently failed.
func (w *DispatchWorker) handleRetryOrFail(ctx context.Context, instr *domain.Instruction, conn *domain.ProviderConnection, reason string, errorCode string) error {
	if instr.CanRetry() {
		return w.handleRetry(ctx, instr, conn, reason, errorCode)
	}
	return w.handleFailure(ctx, instr, reason, errorCode)
}

// handleRetry transitions the instruction to RETRYING and schedules the next retry
// using exponential backoff derived from the connection's RetryPolicy.
func (w *DispatchWorker) handleRetry(ctx context.Context, instr *domain.Instruction, conn *domain.ProviderConnection, reason string, errorCode string) error {
	if err := instr.MarkRetrying(reason, errorCode); err != nil {
		// MarkRetrying can return ErrMaxAttemptsExhausted if the domain model
		// detects exhaustion — fall through to failure.
		if errors.Is(err, domain.ErrMaxAttemptsExhausted) {
			return w.handleFailure(ctx, instr, reason, errorCode)
		}
		return fmt.Errorf("marking retrying: %w", err)
	}

	// Calculate next retry time using exponential backoff.
	nextRetry := calculateNextRetry(instr.AttemptCount, conn.RetryPolicy)
	instr.NextRetryAt = &nextRetry

	if err := w.instructionRepo.Save(ctx, instr, ""); err != nil {
		return fmt.Errorf("saving retrying instruction: %w", err)
	}

	w.logger.InfoContext(ctx, "instruction scheduled for retry",
		"instruction_id", instr.ID,
		"attempt", instr.AttemptCount,
		"max_attempts", instr.MaxAttempts,
		"next_retry_at", nextRetry,
		"reason", reason,
	)
	return nil
}

// handleFailure transitions the instruction to FAILED and persists it.
func (w *DispatchWorker) handleFailure(ctx context.Context, instr *domain.Instruction, reason string, errorCode string) error {
	if err := instr.MarkFailed(reason, errorCode); err != nil {
		return fmt.Errorf("marking failed: %w", err)
	}
	if err := w.instructionRepo.Save(ctx, instr, ""); err != nil {
		return fmt.Errorf("saving failed instruction: %w", err)
	}

	w.logger.WarnContext(ctx, "instruction failed permanently",
		"instruction_id", instr.ID,
		"attempt", instr.AttemptCount,
		"reason", reason,
		"error_code", errorCode,
	)
	return nil
}

// calculateNextRetry computes the next retry time using exponential backoff with a cap.
// attempt is the 1-based attempt number that just failed.
func calculateNextRetry(attempt int, policy domain.RetryPolicy) time.Time {
	backoff := policy.InitialBackoff
	if backoff <= 0 {
		backoff = 1 * time.Second
	}

	multiplier := policy.BackoffMultiplier
	if multiplier < 1.0 {
		multiplier = 2.0
	}

	maxBackoff := policy.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 5 * time.Minute
	}

	// Exponential backoff: initialBackoff * multiplier^(attempt-1)
	// attempt is 1-based; first retry (attempt=1) uses initialBackoff directly.
	factor := math.Pow(multiplier, float64(attempt-1))
	wait := time.Duration(float64(backoff) * factor)
	if wait > maxBackoff {
		wait = maxBackoff
	}

	return time.Now().Add(wait)
}
