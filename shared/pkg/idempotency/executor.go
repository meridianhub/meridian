// Package idempotency provides distributed idempotency checking and locking capabilities.
// This file implements the Executor which wraps business logic with atomic idempotency handling.
package idempotency

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// ExecutorError wraps errors from the Executor with additional context.
type ExecutorError struct {
	// Op describes the operation that failed
	Op string
	// Key is the idempotency key involved
	Key Key
	// Err is the underlying error
	Err error
}

func (e *ExecutorError) Error() string {
	return fmt.Sprintf("idempotency executor %s for key %s: %v", e.Op, e.Key.String(), e.Err)
}

func (e *ExecutorError) Unwrap() error {
	return e.Err
}

// ErrOperationInProgress indicates the operation is already being processed by another request.
// This is a transient error - the client should wait and retry.
var ErrOperationInProgress = errors.New("operation already in progress")

// ExecutorConfig holds configuration for the idempotency executor.
type ExecutorConfig struct {
	// DefaultTTL is the default TTL for idempotency keys if not specified per-operation.
	// Default: 1 hour.
	DefaultTTL time.Duration

	// MaxDeadlockRetries is the maximum number of retries on deadlock/contention errors.
	// Default: 3.
	MaxDeadlockRetries int

	// DeadlockRetryDelay is the base delay between deadlock retry attempts.
	// Uses exponential backoff: delay * 2^attempt.
	// Default: 50ms.
	DeadlockRetryDelay time.Duration
}

// DefaultExecutorConfig returns the default executor configuration.
func DefaultExecutorConfig() ExecutorConfig {
	return ExecutorConfig{
		DefaultTTL:         1 * time.Hour,
		MaxDeadlockRetries: 3,
		DeadlockRetryDelay: 50 * time.Millisecond,
	}
}

// OperationFunc is the business logic function to execute within idempotency protection.
// It receives the context and returns the result data (to be cached) and any error.
// If the function returns an error, the idempotency key is cleaned up (or marked as FAILED).
type OperationFunc func(ctx context.Context) (resultData []byte, err error)

// Executor wraps business logic with atomic idempotency handling.
//
// The executor ensures that:
// 1. Duplicate requests return the cached result
// 2. Only one request processes at a time (via PENDING state)
// 3. On business logic error, the PENDING state is cleaned up atomically
// 4. On success, the result is stored with COMPLETED status
//
// This eliminates the gap between MarkPending and StoreResult that could leave
// orphaned PENDING keys if the service crashes mid-operation.
type Executor struct {
	checker      Checker
	stateMachine *StateMachine
	config       ExecutorConfig
	metrics      *MetricsCollector
}

// NewExecutor creates a new idempotency executor.
//
// Parameters:
//   - checker: The idempotency checker/storer (typically RedisService)
//   - config: Optional configuration (nil uses DefaultExecutorConfig)
func NewExecutor(checker Checker, config *ExecutorConfig) *Executor {
	if config == nil {
		c := DefaultExecutorConfig()
		config = &c
	}

	return &Executor{
		checker:      checker,
		stateMachine: NewStateMachine(nil),
		config:       *config,
		metrics:      nil, // No metrics by default for backward compatibility
	}
}

// NewExecutorWithMetrics creates a new idempotency executor with Prometheus metrics.
//
// Parameters:
//   - checker: The idempotency checker/storer (typically RedisService)
//   - config: Optional configuration (nil uses DefaultExecutorConfig)
//   - metrics: Metrics collector for recording operation metrics
func NewExecutorWithMetrics(checker Checker, config *ExecutorConfig, metrics *MetricsCollector) *Executor {
	e := NewExecutor(checker, config)
	e.metrics = metrics
	return e
}

// ExecuteResult contains the outcome of an Execute call.
type ExecuteResult struct {
	// Data is the result data from the operation or cache
	Data []byte

	// FromCache indicates whether this result came from the idempotency cache
	// (true = duplicate request, false = operation was executed)
	FromCache bool

	// Status is the final status of the idempotency key
	Status OperationStatus
}

// Execute runs the operation with idempotency protection.
//
// Behavior:
//   - If key exists with COMPLETED status: returns cached result
//   - If key exists with PENDING status: returns ErrOperationInProgress
//   - If key doesn't exist: marks PENDING, runs operation, stores result
//   - On operation error: cleans up PENDING state and returns the error
//   - On operation success: stores COMPLETED status with result data
//
// The ttl parameter specifies how long the idempotency key should be retained.
// If ttl is 0 or negative, the executor's DefaultTTL is used.
//
// Thread-safety: This method is safe for concurrent use. The underlying
// idempotency store (Redis) ensures only one request can hold PENDING status.
func (e *Executor) Execute(ctx context.Context, key Key, ttl time.Duration, fn OperationFunc) (*ExecuteResult, error) {
	if ttl <= 0 {
		ttl = e.config.DefaultTTL
	}

	// Step 1: Check if already processed or in progress
	existingResult, err := e.checker.Check(ctx, key)
	if err != nil {
		if errors.Is(err, ErrOperationAlreadyProcessed) {
			// Already completed - return cached result
			return &ExecuteResult{
				Data:      existingResult.Data,
				FromCache: true,
				Status:    existingResult.Status,
			}, nil
		}
		if !errors.Is(err, ErrResultNotFound) {
			// Unexpected error checking state
			return nil, &ExecutorError{Op: "check", Key: key, Err: err}
		}
		// ErrResultNotFound means key doesn't exist - continue to mark pending
	}

	// If we got a result but it's PENDING, someone else is processing
	if existingResult != nil && existingResult.Status == StatusPending {
		return nil, &ExecutorError{Op: "check", Key: key, Err: ErrOperationInProgress}
	}

	// Step 2: Mark as pending with retry for deadlocks
	pendingStart := time.Now()
	if err := e.markPendingWithRetry(ctx, key, ttl); err != nil {
		return nil, err
	}

	// Record pending metric
	if e.metrics != nil {
		e.metrics.RecordPending(key.Operation)
	}

	// Step 3: Execute business logic with cleanup on failure
	resultData, execErr := fn(ctx)

	if execErr != nil {
		// Record pending duration and failure (key is deleted, not marked failed)
		if e.metrics != nil {
			e.metrics.RecordPendingDuration(key.Operation, time.Since(pendingStart))
		}

		// Business logic failed - clean up PENDING state
		// We delete rather than mark FAILED to allow retries with the same key
		if deleteErr := e.checker.Delete(ctx, key); deleteErr != nil {
			// Log but don't override the original error - cleanup failure is secondary
			slog.Error("failed to cleanup pending idempotency key after operation error",
				"key", key.String(),
				"operation_error", execErr.Error(),
				"cleanup_error", deleteErr.Error())
		}
		return nil, execErr
	}

	// Record pending duration and completion
	if e.metrics != nil {
		e.metrics.RecordPendingDuration(key.Operation, time.Since(pendingStart))
		e.metrics.RecordCompleted(key.Operation)
	}

	// Step 4: Store successful result
	result := Result{
		Key:         key,
		Status:      StatusCompleted,
		Data:        resultData,
		CompletedAt: time.Now(),
		TTL:         ttl,
	}

	if err := e.checker.StoreResult(ctx, result); err != nil {
		// This is problematic - operation succeeded but we can't store the result.
		// Log the error but return success - the operation DID complete.
		// The pending key will eventually expire (TTL) and allow retry.
		slog.Error("failed to store completed idempotency result after successful operation",
			"key", key.String(),
			"error", err.Error())
	}

	return &ExecuteResult{
		Data:      resultData,
		FromCache: false,
		Status:    StatusCompleted,
	}, nil
}

// markPendingWithRetry attempts to mark the key as pending with exponential backoff retry.
func (e *Executor) markPendingWithRetry(ctx context.Context, key Key, ttl time.Duration) error {
	var lastErr error

	for attempt := 0; attempt <= e.config.MaxDeadlockRetries; attempt++ {
		err := e.checker.MarkPending(ctx, key, ttl)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if this is a contention error we should retry
		// Currently we retry on any error except context cancellation
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return &ExecutorError{Op: "mark_pending", Key: key, Err: err}
		}

		// Check if someone else acquired the key (race condition)
		if errors.Is(err, ErrOperationAlreadyProcessed) {
			return &ExecutorError{Op: "mark_pending", Key: key, Err: ErrOperationInProgress}
		}

		// Exponential backoff before retry
		if attempt < e.config.MaxDeadlockRetries {
			delay := e.config.DeadlockRetryDelay * time.Duration(1<<attempt)
			select {
			case <-ctx.Done():
				return &ExecutorError{Op: "mark_pending", Key: key, Err: ctx.Err()}
			case <-time.After(delay):
				continue
			}
		}
	}

	return &ExecutorError{Op: "mark_pending", Key: key, Err: lastErr}
}

// ExecuteWithFailedState is like Execute but marks the key as FAILED instead of
// deleting it when the operation fails. This is useful when you want to prevent
// retries with the same idempotency key.
//
// Use cases:
//   - Non-retryable business errors (e.g., insufficient funds)
//   - Operations where retry could cause inconsistent state
func (e *Executor) ExecuteWithFailedState(ctx context.Context, key Key, ttl time.Duration, fn OperationFunc) (*ExecuteResult, error) {
	if ttl <= 0 {
		ttl = e.config.DefaultTTL
	}

	// Step 1: Check if already processed or in progress
	existingResult, err := e.checker.Check(ctx, key)
	if err != nil {
		if errors.Is(err, ErrOperationAlreadyProcessed) {
			return &ExecuteResult{
				Data:      existingResult.Data,
				FromCache: true,
				Status:    existingResult.Status,
			}, nil
		}
		if !errors.Is(err, ErrResultNotFound) {
			return nil, &ExecutorError{Op: "check", Key: key, Err: err}
		}
	}

	if existingResult != nil {
		if existingResult.Status == StatusPending {
			return nil, &ExecutorError{Op: "check", Key: key, Err: ErrOperationInProgress}
		}
		if existingResult.Status == StatusFailed {
			// Previously failed - return the cached failed result
			return &ExecuteResult{
				Data:      existingResult.Data,
				FromCache: true,
				Status:    existingResult.Status,
			}, nil
		}
	}

	// Step 2: Mark as pending
	pendingStart := time.Now()
	if err := e.markPendingWithRetry(ctx, key, ttl); err != nil {
		return nil, err
	}

	// Record pending metric
	if e.metrics != nil {
		e.metrics.RecordPending(key.Operation)
	}

	// Step 3: Execute business logic
	resultData, execErr := fn(ctx)

	if execErr != nil {
		return nil, e.handleFailedExecution(ctx, key, ttl, pendingStart, execErr)
	}

	return e.handleSuccessfulExecution(ctx, key, ttl, pendingStart, resultData), nil
}

// handleFailedExecution records failure metrics and persists the failed state.
func (e *Executor) handleFailedExecution(ctx context.Context, key Key, ttl time.Duration, pendingStart time.Time, execErr error) error {
	if e.metrics != nil {
		e.metrics.RecordPendingDuration(key.Operation, time.Since(pendingStart))
		e.metrics.RecordFailed(key.Operation, MetricReasonInternal)
	}

	failedResult := Result{
		Key:         key,
		Status:      StatusFailed,
		Error:       execErr.Error(),
		CompletedAt: time.Now(),
		TTL:         ttl,
	}

	if storeErr := e.checker.StoreResult(ctx, failedResult); storeErr != nil {
		slog.Error("failed to store failed idempotency result",
			"key", key.String(),
			"operation_error", execErr.Error(),
			"store_error", storeErr.Error())
	}

	return execErr
}

// handleSuccessfulExecution records completion metrics and persists the result.
func (e *Executor) handleSuccessfulExecution(ctx context.Context, key Key, ttl time.Duration, pendingStart time.Time, resultData []byte) *ExecuteResult {
	if e.metrics != nil {
		e.metrics.RecordPendingDuration(key.Operation, time.Since(pendingStart))
		e.metrics.RecordCompleted(key.Operation)
	}

	result := Result{
		Key:         key,
		Status:      StatusCompleted,
		Data:        resultData,
		CompletedAt: time.Now(),
		TTL:         ttl,
	}

	if err := e.checker.StoreResult(ctx, result); err != nil {
		slog.Error("failed to store completed idempotency result after successful operation",
			"key", key.String(),
			"error", err.Error())
	}

	return &ExecuteResult{
		Data:      resultData,
		FromCache: false,
		Status:    StatusCompleted,
	}
}
