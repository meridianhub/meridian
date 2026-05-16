// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// ErrNilError is returned when HandleStepFailure is called with a nil error.
var ErrNilError = errors.New("nil error passed to failure handler")

// SagaExecutor orchestrates the execution of saga instances with error classification
// and compensation handling per FR-28.
//
//nolint:revive // SagaExecutor naming is intentional for clarity at call sites
type SagaExecutor struct {
	instanceRepo    SagaInstanceRepository
	stepResultRepo  StepResultRepository
	handlerRegistry *HandlerRegistry
	claimService    *ClaimService
	logger          *slog.Logger
}

// SagaInstanceRepository provides persistence operations for saga instances.
//
//nolint:revive // SagaInstanceRepository naming is intentional for clarity
type SagaInstanceRepository interface {
	// FindByID retrieves a saga instance by ID.
	FindByID(ctx context.Context, id uuid.UUID) (*SagaInstance, error)

	// Update persists changes to a saga instance.
	Update(ctx context.Context, instance *SagaInstance) error

	// UpdateStatus updates the saga status atomically.
	UpdateStatus(ctx context.Context, id uuid.UUID, status SagaStatus) error

	// UpdateStatusWithError updates status and records error information.
	UpdateStatusWithError(ctx context.Context, id uuid.UUID, status SagaStatus, stepIndex int, err error, category ErrorCategory) error

	// ResetReplayCount resets the replay count to 0 (used when moving to next step).
	ResetReplayCount(ctx context.Context, id uuid.UUID) error

	// UpdateNextRetryAt sets the earliest wall-clock time this saga can be reclaimed
	// after a transient failure. Called from handleTransientFailure before releasing
	// the lease. The orphan watcher filters on this column to skip sagas still in backoff.
	UpdateNextRetryAt(ctx context.Context, id uuid.UUID, nextRetryAt time.Time) error

	// ResetReplayCountAndBackoff atomically resets replay_count to 0 AND clears
	// next_retry_at. Called when a step completes successfully so the saga starts the
	// next step with a clean slate. Combining both columns in a single UPDATE avoids
	// any window where the saga could be picked up with mismatched state.
	ResetReplayCountAndBackoff(ctx context.Context, id uuid.UUID) error
}

// NewSagaExecutor creates a new SagaExecutor.
func NewSagaExecutor(
	instanceRepo SagaInstanceRepository,
	stepResultRepo StepResultRepository,
	handlerRegistry *HandlerRegistry,
	claimService *ClaimService,
) *SagaExecutor {
	return &SagaExecutor{
		instanceRepo:    instanceRepo,
		stepResultRepo:  stepResultRepo,
		handlerRegistry: handlerRegistry,
		claimService:    claimService,
		logger:          slog.Default(),
	}
}

// WithLogger sets the logger for the executor.
func (e *SagaExecutor) WithLogger(logger *slog.Logger) *SagaExecutor {
	e.logger = logger
	return e
}

// StepFailureResult captures the outcome of a step failure for decision making.
type StepFailureResult struct {
	// SagaID is the saga instance ID.
	SagaID uuid.UUID

	// StepIndex is the index of the failed step.
	StepIndex int

	// StepName is the name of the failed step.
	StepName string

	// Error is the original error from the step handler.
	Error error

	// ErrorCategory is the classified error category.
	ErrorCategory ErrorCategory

	// Action is the recommended action based on error category.
	Action FailureAction

	// ReplayCount is the current replay count before any increment.
	ReplayCount int
}

// FailureAction represents the action to take after a step failure.
type FailureAction string

const (
	// FailureActionRetry indicates the step should be retried (TRANSIENT error).
	// The saga remains in RUNNING status, replay_count is incremented, lease is released.
	FailureActionRetry FailureAction = "RETRY"

	// FailureActionCompensate indicates compensation should begin immediately (FATAL error).
	// The saga transitions to COMPENSATING status, replay_count is NOT incremented.
	FailureActionCompensate FailureAction = "COMPENSATE"

	// FailureActionManualIntervention indicates the saga requires operator attention.
	// The saga transitions to FAILED_MANUAL_INTERVENTION (max retries exceeded).
	FailureActionManualIntervention FailureAction = "MANUAL_INTERVENTION"
)

// HandleStepFailure processes a step failure and determines the appropriate action.
// This is the core decision logic for FATAL vs TRANSIENT error handling (FR-28).
//
// Decision logic:
//   - FATAL errors -> Transition to COMPENSATING immediately, don't increment replay_count
//   - TRANSIENT errors -> Increment replay_count, release lease for retry
//   - If replay_count >= MaxReplays -> Transition to FAILED_MANUAL_INTERVENTION
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - sagaID: The saga instance ID
//   - stepIndex: The index of the failed step
//   - stepName: The name of the failed step
//   - err: The error from the step handler
//   - maxReplays: Maximum replay attempts before zombie detection
//
// Returns:
//   - StepFailureResult with the classified error and recommended action
//   - Error if there was a problem processing the failure
func (e *SagaExecutor) HandleStepFailure(
	ctx context.Context,
	sagaID uuid.UUID,
	stepIndex int,
	stepName string,
	err error,
	maxReplays int,
) (*StepFailureResult, error) {
	if err == nil {
		return nil, ErrNilError
	}

	category := ClassifyError(err)

	instance, fetchErr := e.instanceRepo.FindByID(ctx, sagaID)
	if fetchErr != nil {
		return nil, fmt.Errorf("failed to fetch saga instance: %w", fetchErr)
	}
	if instance == nil {
		return nil, fmt.Errorf("%w: %s", ErrSagaNotFound, sagaID)
	}

	result := &StepFailureResult{
		SagaID:        sagaID,
		StepIndex:     stepIndex,
		StepName:      stepName,
		Error:         err,
		ErrorCategory: category,
		ReplayCount:   instance.ReplayCount,
	}

	e.logger.Info("handling step failure",
		"saga_id", sagaID,
		"step_index", stepIndex,
		"step_name", stepName,
		"error", err.Error(),
		"error_category", category,
		"replay_count", instance.ReplayCount,
		"max_replays", maxReplays,
	)

	switch category {
	case ErrorCategoryFatal:
		return e.handleFatalFailure(ctx, result, instance, err)
	case ErrorCategoryTransient:
		return e.handleTransientFailure(ctx, result, instance, err, maxReplays)
	default:
		return e.handleUnknownCategoryFailure(ctx, result, err)
	}
}

// handleFatalFailure transitions the saga to COMPENSATING immediately without incrementing replay_count (FR-28).
func (e *SagaExecutor) handleFatalFailure(
	ctx context.Context,
	result *StepFailureResult,
	_ *SagaInstance,
	err error,
) (*StepFailureResult, error) {
	result.Action = FailureActionCompensate

	if updateErr := e.instanceRepo.UpdateStatusWithError(
		ctx, result.SagaID, SagaStatusCompensating, result.StepIndex, err, result.ErrorCategory,
	); updateErr != nil {
		return nil, fmt.Errorf("failed to transition saga to COMPENSATING: %w", updateErr)
	}

	RecordStepFailure(result.StepName, string(result.ErrorCategory))

	e.logger.Warn("FATAL error detected, transitioning to COMPENSATING",
		"saga_id", result.SagaID,
		"step_index", result.StepIndex,
		"step_name", result.StepName,
		"error", err.Error(),
	)

	return result, nil
}

// handleTransientFailure either retries (releasing the lease) or escalates to manual intervention
// if max replays have been exceeded.
//
// Before releasing the lease, this method writes next_retry_at = now + backoff(replay_count)
// to saga_instances. The orphan watcher's predicate (next_retry_at IS NULL OR <= now)
// will then skip this saga until the backoff window elapses, preventing immediate
// re-claim and breaking thundering herd when many sagas fail simultaneously.
func (e *SagaExecutor) handleTransientFailure(
	ctx context.Context,
	result *StepFailureResult,
	instance *SagaInstance,
	err error,
	maxReplays int,
) (*StepFailureResult, error) {
	if instance.ReplayCount >= maxReplays {
		return e.handleZombieDetected(ctx, result, instance, err)
	}

	result.Action = FailureActionRetry

	// Compute next_retry_at = now + backoff. Per-handler retry policy overrides
	// global ClaimConfig defaults if present.
	baseDelay, maxDelay := e.resolveRetryBounds(result.StepName)
	delay := CalculateBackoffDelay(instance.ReplayCount, baseDelay, maxDelay)
	nextRetryAt := time.Now().Add(delay)

	// Set next_retry_at FIRST. If the subsequent lease release fails or the pod
	// crashes, the orphan watcher still respects the backoff window. The
	// alternative (release first, then set) opens a race where another pod
	// reclaims immediately before the backoff is recorded.
	if updateErr := e.instanceRepo.UpdateNextRetryAt(ctx, result.SagaID, nextRetryAt); updateErr != nil {
		e.logger.Error("failed to set next_retry_at - retry will not respect backoff",
			"saga_id", result.SagaID,
			"error", updateErr,
		)
		// Continue: a missed backoff is preferable to leaving the lease held.
	}

	if e.claimService != nil {
		if releaseErr := e.claimService.ReleaseLease(ctx, result.SagaID); releaseErr != nil {
			e.logger.Error("failed to release lease for retry",
				"saga_id", result.SagaID,
				"error", releaseErr,
			)
		}
	}

	RecordStepFailure(result.StepName, string(result.ErrorCategory))

	e.logger.Info("TRANSIENT error, scheduled for retry with backoff",
		"saga_id", result.SagaID,
		"step_index", result.StepIndex,
		"step_name", result.StepName,
		"replay_count", instance.ReplayCount,
		"backoff_delay", delay,
		"next_retry_at", nextRetryAt,
	)

	return result, nil
}

// resolveRetryBounds returns the (baseDelay, maxDelay) pair to use for a given
// step. Order of precedence:
//  1. Per-handler retry policy declared in handlers.yaml (HandlerMetadata.RetryPolicy)
//  2. Global ClaimConfig defaults (SAGA_RETRY_BASE_DELAY / SAGA_RETRY_MAX_DELAY)
//  3. Package-level constants (DefaultRetryBaseDelay / DefaultRetryMaxDelay)
//
// This is forgiving by design: any layer is optional. If the executor has no
// ClaimService wired in (some unit tests construct executors with nil), we fall
// straight to the package defaults so behavior stays predictable.
func (e *SagaExecutor) resolveRetryBounds(stepName string) (time.Duration, time.Duration) {
	baseDelay := DefaultRetryBaseDelay
	maxDelay := DefaultRetryMaxDelay

	if e.claimService != nil && e.claimService.config != nil {
		if e.claimService.config.RetryBaseDelay > 0 {
			baseDelay = e.claimService.config.RetryBaseDelay
		}
		if e.claimService.config.RetryMaxDelay > 0 {
			maxDelay = e.claimService.config.RetryMaxDelay
		}
	}

	// Per-handler override via registry metadata (populated from handlers.yaml).
	if e.handlerRegistry != nil && stepName != "" {
		if _, meta, err := e.handlerRegistry.GetWithMetadata(stepName); err == nil && meta != nil {
			if meta.RetryPolicy != nil {
				if meta.RetryPolicy.BaseDelay > 0 {
					baseDelay = meta.RetryPolicy.BaseDelay
				}
				if meta.RetryPolicy.MaxDelay > 0 {
					maxDelay = meta.RetryPolicy.MaxDelay
				}
			}
		}
	}

	return baseDelay, maxDelay
}

// handleZombieDetected transitions the saga to FAILED_MANUAL_INTERVENTION when max replays are exceeded.
func (e *SagaExecutor) handleZombieDetected(
	ctx context.Context,
	result *StepFailureResult,
	instance *SagaInstance,
	err error,
) (*StepFailureResult, error) {
	result.Action = FailureActionManualIntervention

	if updateErr := e.instanceRepo.UpdateStatusWithError(
		ctx, result.SagaID, SagaStatusFailedManualIntervention, result.StepIndex, err, result.ErrorCategory,
	); updateErr != nil {
		return nil, fmt.Errorf("failed to transition saga to FAILED_MANUAL_INTERVENTION: %w", updateErr)
	}

	RecordZombieSagaDetected(instance.SagaDefinitionID.String())

	e.logger.Error("max replays exceeded, transitioning to FAILED_MANUAL_INTERVENTION",
		"saga_id", result.SagaID,
		"step_index", result.StepIndex,
		"replay_count", instance.ReplayCount,
		"max_replays", instance.ReplayCount,
	)

	return result, nil
}

// handleUnknownCategoryFailure treats unknown error categories as FATAL (fail-safe).
func (e *SagaExecutor) handleUnknownCategoryFailure(
	ctx context.Context,
	result *StepFailureResult,
	err error,
) (*StepFailureResult, error) {
	result.Action = FailureActionCompensate
	result.ErrorCategory = ErrorCategoryFatal

	if updateErr := e.instanceRepo.UpdateStatusWithError(
		ctx, result.SagaID, SagaStatusCompensating, result.StepIndex, err, ErrorCategoryFatal,
	); updateErr != nil {
		return nil, fmt.Errorf("failed to transition saga to COMPENSATING: %w", updateErr)
	}

	e.logger.Warn("unknown error category, treating as FATAL",
		"saga_id", result.SagaID,
		"step_index", result.StepIndex,
		"error", err.Error(),
	)

	return result, nil
}

// ShouldRetry returns true if the error category indicates the step should be retried.
func ShouldRetry(category ErrorCategory) bool {
	return category == ErrorCategoryTransient
}

// ShouldCompensate returns true if the error category indicates compensation should begin.
func ShouldCompensate(category ErrorCategory) bool {
	return category == ErrorCategoryFatal
}

// DetermineFailureAction determines the action to take based on error category and replay state.
// This is a stateless helper function for testing decision logic.
func DetermineFailureAction(category ErrorCategory, replayCount, maxReplays int) FailureAction {
	switch category {
	case ErrorCategoryFatal:
		return FailureActionCompensate
	case ErrorCategoryTransient:
		if replayCount >= maxReplays {
			return FailureActionManualIntervention
		}
		return FailureActionRetry
	default:
		// Unknown category - fail-safe to compensation
		return FailureActionCompensate
	}
}

// RecordStepFailure records a step failure metric.
// This is a placeholder that calls the metrics system.
func RecordStepFailure(_, _ string) {
	// The metrics package already has recording functions
	// This is a convenience wrapper for step-specific failures
}

// ProcessStepResult processes the result of a step execution and handles errors.
// This is a convenience method that combines step execution and failure handling.
//
// Returns:
//   - (*StepFailureResult, nil) on step failure (with classification)
//   - (nil, error) on infrastructure error (e.g., failed to reset replay count)
//   - (nil, nil) should not occur - on success, the first return is always nil
//
//nolint:nilnil // Returns nil, nil on success - this is intentional behavior
func (e *SagaExecutor) ProcessStepResult(
	ctx context.Context,
	instance *SagaInstance,
	stepIndex int,
	stepName string,
	_ interface{}, // result is unused, kept for API consistency
	err error,
	maxReplays int,
) (*StepFailureResult, error) {
	if err == nil {
		// Success - reset replay count AND clear any pending backoff atomically.
		// Combining both columns in one UPDATE ensures the orphan watcher cannot
		// observe a half-updated row (replay_count cleared but next_retry_at
		// stale, or the inverse).
		if resetErr := e.instanceRepo.ResetReplayCountAndBackoff(ctx, instance.ID); resetErr != nil {
			return nil, fmt.Errorf("failed to reset replay count and backoff: %w", resetErr)
		}
		return nil, nil
	}

	// Handle failure
	return e.HandleStepFailure(ctx, instance.ID, stepIndex, stepName, err, maxReplays)
}

// UpdateSagaError updates the saga instance with error information.
// This is a helper for updating the error fields on the saga instance.
func UpdateSagaError(instance *SagaInstance, stepIndex int, err error, category ErrorCategory) {
	now := time.Now()
	instance.UpdatedAt = now
	instance.FailedStepIndex = &stepIndex

	if err != nil {
		errMsg := err.Error()
		instance.ErrorMessage = &errMsg
	}

	catStr := string(category)
	instance.ErrorCategory = &catStr
}
