// Package worker contains background workers for the operational gateway service.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"github.com/meridianhub/meridian/shared/pkg/dispatch"
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
// resolve route -> select connection (primary, then fallback) -> dispatch -> handle outcome.
//
// The instruction arrives already in DISPATCHING state (set by FetchDispatchable).
func (w *DispatchWorker) processInstruction(ctx context.Context, instr *domain.Instruction) error {
	route, err := w.resolveRoute(ctx, instr)
	if err != nil {
		return err
	}
	// resolveRoute returns (nil, nil) after marking the instruction failed (route not found).
	if route == nil {
		return nil
	}

	conn, err := w.selectConnection(ctx, instr, route)
	if err != nil {
		return err
	}
	// selectConnection returns (nil, nil) after marking the instruction failed or scheduling a
	// retry (no usable connection). Nothing more to do this cycle.
	if conn == nil {
		return nil
	}

	// conn is guaranteed available (circuit closed or half-open) at this point.
	result := w.dispatcher.Dispatch(ctx, instr, conn, route)

	// Handle transport-level error (no response received).
	if result.Error != nil {
		return w.handleDispatchError(ctx, instr, conn, result)
	}

	// Record success on the connection circuit breaker.
	w.recordConnectionSuccess(ctx, conn)

	return w.handleDispatchOutcome(ctx, instr, conn, result)
}

// resolveRoute resolves the dispatch route for the instruction.
// Returns (nil, nil) after marking the instruction permanently failed when no route is
// configured (ROUTE_NOT_FOUND), or a transient error for DB/network issues so the
// stuck-instruction reaper can retry later.
func (w *DispatchWorker) resolveRoute(ctx context.Context, instr *domain.Instruction) (*ports.InstructionRoute, error) {
	route, err := w.routeResolver.Resolve(ctx, instr.TenantID.String(), instr.InstructionType)
	if err != nil {
		if errors.Is(err, ports.ErrRouteNotFound) {
			return nil, w.handleFailure(ctx, instr, fmt.Sprintf("route resolution failed: %v", err), "ROUTE_NOT_FOUND")
		}
		return nil, fmt.Errorf("route resolution transient error: %w", err)
	}
	return route, nil
}

// selectConnection returns a usable provider connection for the instruction, honoring the
// route's primary ConnectionID and falling back to FallbackConnectionID when the primary is
// unavailable (not found, or circuit open). The returned connection is guaranteed available.
//
// Return contract:
//   - (conn, nil): a usable, available connection to dispatch with.
//   - (nil, nil):  the instruction was terminally handled (marked failed) or scheduled for
//     retry; the caller should stop processing it this cycle.
//   - (nil, err):  a transient error (DB/network); the caller should propagate so the
//     stuck-instruction reaper retries later.
func (w *DispatchWorker) selectConnection(ctx context.Context, instr *domain.Instruction, route *ports.InstructionRoute) (*domain.ProviderConnection, error) {
	tenantID := instr.TenantID.String()

	candidates := []struct {
		label string
		id    string
	}{
		{"primary", route.ConnectionID},
	}
	if route.FallbackConnectionID != "" && route.FallbackConnectionID != route.ConnectionID {
		candidates = append(candidates, struct {
			label string
			id    string
		}{"fallback", route.FallbackConnectionID})
	}

	// firstUnavailable is the first connection that exists but whose circuit is open. It is used
	// to drive a retry (the circuit may recover) and to source the backoff RetryPolicy.
	var firstUnavailable *domain.ProviderConnection

	for _, candidate := range candidates {
		conn, found, err := w.lookupConnection(ctx, tenantID, candidate.id)
		if err != nil {
			return nil, fmt.Errorf("%s connection lookup transient error: %w", candidate.label, err)
		}
		if !found {
			continue // not configured or not found — treat as unavailable, try next candidate
		}
		if conn.IsAvailable() {
			if candidate.label == "fallback" {
				w.logger.WarnContext(ctx, "primary connection unavailable; dispatching via fallback",
					"instruction_id", instr.ID,
					"primary_connection_id", route.ConnectionID,
					"fallback_connection_id", route.FallbackConnectionID,
				)
			}
			return conn, nil
		}
		if firstUnavailable == nil {
			firstUnavailable = conn
		}
	}

	// No usable connection. If at least one exists but its circuit is open, retry (it may
	// recover); otherwise the connection is genuinely missing and this is a permanent failure.
	if firstUnavailable != nil {
		return nil, w.handleRetryOrFail(ctx, instr, firstUnavailable,
			"provider connection unavailable: circuit open", "CIRCUIT_OPEN")
	}
	return nil, w.handleFailure(ctx, instr,
		fmt.Sprintf("no provider connection available for route (connection_id=%q, fallback_connection_id=%q)",
			route.ConnectionID, route.FallbackConnectionID),
		"CONNECTION_NOT_FOUND")
}

// lookupConnection fetches a connection by id. It returns found=false (with a nil connection and
// nil error) when the id is empty or the connection does not exist, so the caller can fall through
// to a fallback candidate. Any other error is transient and returned to the caller.
func (w *DispatchWorker) lookupConnection(ctx context.Context, tenantID, connectionID string) (*domain.ProviderConnection, bool, error) {
	if connectionID == "" {
		return nil, false, nil
	}
	conn, err := w.connectionRepo.FindByID(ctx, tenantID, connectionID)
	if err != nil {
		if errors.Is(err, ports.ErrConnectionNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return conn, true, nil
}

// handleDispatchError records a transport-level failure on the connection and retries or fails.
func (w *DispatchWorker) handleDispatchError(ctx context.Context, instr *domain.Instruction, conn *domain.ProviderConnection, result ports.DispatchResult) error {
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

// recordConnectionSuccess records a successful dispatch on the connection circuit breaker.
func (w *DispatchWorker) recordConnectionSuccess(ctx context.Context, conn *domain.ProviderConnection) {
	conn.RecordSuccess()
	if saveErr := w.connectionRepo.UpdateHealth(ctx, conn); saveErr != nil {
		w.logger.ErrorContext(ctx, "failed to persist connection health",
			"connection_id", conn.ConnectionID, "error", saveErr,
		)
	}
}

// handleDispatchOutcome processes the parsed outcome from a successful dispatch.
func (w *DispatchWorker) handleDispatchOutcome(ctx context.Context, instr *domain.Instruction, conn *domain.ProviderConnection, result ports.DispatchResult) error {
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

	// Calculate next retry time using exponential backoff from the shared dispatch package.
	nextRetry := dispatch.CalculateNextRetry(instr.AttemptCount, conn.RetryPolicy)
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
