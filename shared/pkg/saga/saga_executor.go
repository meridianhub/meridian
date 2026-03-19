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

	// Classify the error
	category := ClassifyError(err)

	// Get current saga state
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

	// Decision logic based on error category
	switch category {
	case ErrorCategoryFatal:
		// FATAL errors: Transition to COMPENSATING immediately
		// Do NOT increment replay_count (FR-28)
		result.Action = FailureActionCompensate

		if updateErr := e.instanceRepo.UpdateStatusWithError(
			ctx, sagaID, SagaStatusCompensating, stepIndex, err, category,
		); updateErr != nil {
			return nil, fmt.Errorf("failed to transition saga to COMPENSATING: %w", updateErr)
		}

		RecordStepFailure(stepName, string(category))

		e.logger.Warn("FATAL error detected, transitioning to COMPENSATING",
			"saga_id", sagaID,
			"step_index", stepIndex,
			"step_name", stepName,
			"error", err.Error(),
		)

	case ErrorCategoryTransient:
		// TRANSIENT errors: Check if max replays exceeded
		if instance.ReplayCount >= maxReplays {
			// Max replays exceeded - zombie detected
			result.Action = FailureActionManualIntervention

			if updateErr := e.instanceRepo.UpdateStatusWithError(
				ctx, sagaID, SagaStatusFailedManualIntervention, stepIndex, err, category,
			); updateErr != nil {
				return nil, fmt.Errorf("failed to transition saga to FAILED_MANUAL_INTERVENTION: %w", updateErr)
			}

			RecordZombieSagaDetected(instance.SagaDefinitionID.String())

			e.logger.Error("max replays exceeded, transitioning to FAILED_MANUAL_INTERVENTION",
				"saga_id", sagaID,
				"step_index", stepIndex,
				"replay_count", instance.ReplayCount,
				"max_replays", maxReplays,
			)
		} else {
			// Retry: Release lease and let claiming service pick it up
			result.Action = FailureActionRetry

			// Release the lease so another worker can claim it
			// The claiming service will increment replay_count when claiming
			if e.claimService != nil {
				if releaseErr := e.claimService.ReleaseLease(ctx, sagaID); releaseErr != nil {
					e.logger.Error("failed to release lease for retry",
						"saga_id", sagaID,
						"error", releaseErr,
					)
					// Don't fail the operation, just log the error
				}
			}

			RecordStepFailure(stepName, string(category))

			e.logger.Info("TRANSIENT error, releasing lease for retry",
				"saga_id", sagaID,
				"step_index", stepIndex,
				"step_name", stepName,
				"replay_count", instance.ReplayCount,
			)
		}

	default:
		// Unknown category - treat as FATAL (fail-safe)
		result.Action = FailureActionCompensate
		result.ErrorCategory = ErrorCategoryFatal

		if updateErr := e.instanceRepo.UpdateStatusWithError(
			ctx, sagaID, SagaStatusCompensating, stepIndex, err, ErrorCategoryFatal,
		); updateErr != nil {
			return nil, fmt.Errorf("failed to transition saga to COMPENSATING: %w", updateErr)
		}

		e.logger.Warn("unknown error category, treating as FATAL",
			"saga_id", sagaID,
			"step_index", stepIndex,
			"error", err.Error(),
		)
	}

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
		// Success - reset replay count and move to next step
		if resetErr := e.instanceRepo.ResetReplayCount(ctx, instance.ID); resetErr != nil {
			return nil, fmt.Errorf("failed to reset replay count: %w", resetErr)
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
