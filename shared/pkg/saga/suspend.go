// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Suspend-related errors.
var (
	// ErrSuspended is returned by ctx.Suspend() to signal the runtime to pause execution.
	// This is not a failure - it's the normal control flow for suspending a saga.
	ErrSuspended = errors.New("saga suspended waiting for external event")

	// ErrDuplicateIdempotencyKey is returned when attempting to suspend with a key that's already in use.
	ErrDuplicateIdempotencyKey = errors.New("suspend idempotency key already exists")

	// ErrInvalidTimeout is returned when timeout is not positive.
	ErrInvalidTimeout = errors.New("suspend timeout must be positive")

	// ErrSuspendSagaNotFound is returned when a saga cannot be found for suspension operations.
	ErrSuspendSagaNotFound = errors.New("saga not found")

	// ErrInvalidSagaState is returned when trying to complete a saga that's not waiting for an event.
	ErrInvalidSagaState = errors.New("saga is not waiting for external event")

	// ErrIdempotencyKeyMismatch is returned when the idempotency key doesn't match.
	ErrIdempotencyKeyMismatch = errors.New("idempotency key does not match suspended saga")

	// ErrIdempotencyKeyRequired is returned when idempotency key is empty.
	ErrIdempotencyKeyRequired = errors.New("idempotency key is required")
)

// StepStatusSuspended indicates the step is suspended waiting for external event.
const StepStatusSuspended StepStatus = "SUSPENDED"

// SuspendRequest contains the parameters for suspending a saga.
type SuspendRequest struct {
	// IdempotencyKey is a unique key for this suspension (used by external systems to resume).
	IdempotencyKey string

	// Timeout is the maximum time to wait for the external event before auto-failing.
	Timeout time.Duration

	// Reason is an optional human-readable description of why the saga is suspended.
	Reason string

	// Data contains optional context data to store with the suspension.
	Data map[string]interface{}
}

// SuspendResult contains the result of a successful suspension.
type SuspendResult struct {
	// SagaInstanceID is the ID of the suspended saga.
	SagaInstanceID uuid.UUID

	// IdempotencyKey is the key that external systems use to resume the saga.
	IdempotencyKey string

	// TimeoutAt is when the saga will auto-fail if not resumed.
	TimeoutAt time.Time
}

// CompleteSagaStepRequest contains the parameters for completing a suspended saga step.
type CompleteSagaStepRequest struct {
	// SagaInstanceID is the ID of the saga to resume (optional if IdempotencyKey is unique).
	SagaInstanceID *uuid.UUID

	// IdempotencyKey is the key provided when the saga was suspended.
	IdempotencyKey string

	// Result is the data from the external event (e.g., payment confirmation).
	Result interface{}
}

// CompleteSagaStepResponse contains the result of completing a suspended saga step.
type CompleteSagaStepResponse struct {
	// SagaInstanceID is the ID of the resumed saga.
	SagaInstanceID uuid.UUID

	// WasAlreadyCompleted indicates this is an idempotent no-op (saga already resumed).
	WasAlreadyCompleted bool

	// NewStatus is the saga's status after completion.
	NewStatus SagaStatus
}

// SuspendService handles saga suspension and resumption.
type SuspendService struct {
	db     *gorm.DB
	config *SuspendConfig
}

// SuspendConfig holds configuration for the suspend service.
type SuspendConfig struct {
	// DefaultTimeout is the default suspension timeout if not specified.
	DefaultTimeout time.Duration

	// MaxTimeout is the maximum allowed suspension timeout.
	MaxTimeout time.Duration
}

// DefaultSuspendConfig returns the default suspend configuration.
func DefaultSuspendConfig() *SuspendConfig {
	return &SuspendConfig{
		DefaultTimeout: 24 * time.Hour,     // 1 day default
		MaxTimeout:     7 * 24 * time.Hour, // 1 week maximum
	}
}

// NewSuspendService creates a new SuspendService.
func NewSuspendService(db *gorm.DB, config *SuspendConfig) *SuspendService {
	if config == nil {
		config = DefaultSuspendConfig()
	}
	return &SuspendService{
		db:     db,
		config: config,
	}
}

// SuspendSaga suspends a saga waiting for an external event.
// This method:
// 1. Creates a step result with SUSPENDED status
// 2. Updates the saga to WAITING_FOR_EVENT status
// 3. Sets the suspend timeout
// 4. CRITICALLY: Releases the pod lease so other work can proceed
//
// All operations happen in a single transaction for atomicity.
func (s *SuspendService) SuspendSaga(
	ctx context.Context,
	instance *SagaInstance,
	stepIndex int,
	stepName string,
	req *SuspendRequest,
) (*SuspendResult, error) {
	// Validate inputs
	if instance == nil {
		return nil, fmt.Errorf("%w: saga instance is required", ErrSuspendSagaNotFound)
	}
	if req == nil {
		return nil, fmt.Errorf("%w: suspend request is required", ErrIdempotencyKeyRequired)
	}
	if req.IdempotencyKey == "" {
		return nil, ErrIdempotencyKeyRequired
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = s.config.DefaultTimeout
	}
	if timeout > s.config.MaxTimeout {
		timeout = s.config.MaxTimeout
	}

	now := time.Now()
	timeoutAt := now.Add(timeout)

	// Generate idempotency key for the step result
	stepIdempotencyKey := FormatIdempotencyKey(instance.ID, stepIndex)

	// Generate causation ID
	causationID := GenerateCausationID(instance.ID, stepIndex)

	// Prepare step result with SUSPENDED status
	stepResult := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instance.ID,
		StepIndex:      stepIndex,
		StepName:       stepName,
		IdempotencyKey: stepIdempotencyKey,
		Status:         StepStatusSuspended,
		CausationID:    &causationID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// Store suspend request data in step result, including the idempotency key for lookups
	stepResult.Result = JSONB{
		"idempotency_key": req.IdempotencyKey,
	}
	if req.Data != nil {
		for k, v := range req.Data {
			stepResult.Result[k] = v
		}
	}

	// Execute in transaction
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Save step result with SUSPENDED status
		if err := tx.Create(stepResult).Error; err != nil {
			// Check for duplicate idempotency key
			if isDuplicateKeyError(err) {
				return fmt.Errorf("%w: %s", ErrDuplicateIdempotencyKey, req.IdempotencyKey)
			}
			return fmt.Errorf("failed to save suspended step result: %w", err)
		}

		// 2. Update saga instance:
		//    - Status = WAITING_FOR_EVENT
		//    - Set suspend fields
		//    - CRITICAL: Release lease (claimed_by_pod = NULL)
		updates := map[string]interface{}{
			"status":         SagaStatusWaitingForEvent,
			"suspend_reason": req.Reason,
			"suspend_data":   JSONB{"idempotency_key": req.IdempotencyKey, "timeout_at": timeoutAt},
			"updated_at":     now,
			// CRITICAL: Release the lease so other sagas can be processed
			"claimed_by_pod":   nil,
			"claimed_at":       nil,
			"lease_expires_at": nil,
		}

		result := tx.Model(&SagaInstance{}).
			Where("id = ?", instance.ID).
			Updates(updates)

		if result.Error != nil {
			return fmt.Errorf("failed to update saga instance: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrSuspendSagaNotFound
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &SuspendResult{
		SagaInstanceID: instance.ID,
		IdempotencyKey: req.IdempotencyKey,
		TimeoutAt:      timeoutAt,
	}, nil
}

// CompleteSagaStep resumes a suspended saga with the result from an external event.
// This method is called by external systems (e.g., webhook handlers) when the
// awaited event occurs.
//
// Idempotency guarantee: If the saga has already been resumed (status != WAITING_FOR_EVENT),
// this method returns success with WasAlreadyCompleted=true.
//
// Lookup strategy:
// 1. First, try to find a saga that's still WAITING_FOR_EVENT with matching idempotency key
// 2. If not found, check if we already processed this by looking at step results
//
func (s *SuspendService) CompleteSagaStep(
	ctx context.Context,
	req *CompleteSagaStepRequest,
) (*CompleteSagaStepResponse, error) {
	if req.IdempotencyKey == "" {
		return nil, ErrIdempotencyKeyRequired
	}

	var response CompleteSagaStepResponse
	now := time.Now()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Strategy 1: Find saga by suspend idempotency key in suspend_data (still waiting)
		var saga SagaInstance
		query := tx.Where("suspend_data->>'idempotency_key' = ?", req.IdempotencyKey)
		if req.SagaInstanceID != nil {
			query = query.Where("id = ?", *req.SagaInstanceID)
		}

		// Use FOR UPDATE to lock the row
		err := query.Clauses(clause.Locking{Strength: "UPDATE"}).First(&saga).Error

		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Strategy 2: Check if this idempotency key was already processed
			// Look for a step result that had this suspend idempotency key
			// The step result's idempotency key format includes the saga ID, but we need to
			// find which saga had this suspend key. We can look it up via the step result's
			// data or find the saga by checking step results with matching suspend data.
			//
			// For idempotency, we need to find any saga where the step result had this key
			// in its original suspension. We store the suspend key in the step result's
			// Result field when suspended.

			// Look for a step result that was previously suspended with this idempotency key
			// We stored the suspend data in Result when suspending
			var stepWithKey SagaStepResult
			stepQuery := tx.Where("result->>'idempotency_key' = ? OR result->>'_suspend_key' = ?",
				req.IdempotencyKey, req.IdempotencyKey)
			// If SagaInstanceID provided, filter by it for consistency
			if req.SagaInstanceID != nil {
				stepQuery = stepQuery.Where("saga_instance_id = ?", *req.SagaInstanceID)
			}
			lookupErr := stepQuery.Order("created_at DESC").First(&stepWithKey).Error

			if lookupErr == nil {
				// Found a step that had this idempotency key - fetch the saga
				if sagaErr := tx.First(&saga, "id = ?", stepWithKey.SagaInstanceID).Error; sagaErr != nil {
					return fmt.Errorf("failed to find saga for step result: %w", sagaErr)
				}
				// This is an idempotent call - saga was already processed
				response.SagaInstanceID = saga.ID
				response.WasAlreadyCompleted = true
				response.NewStatus = saga.Status
				return nil
			}

			// No saga found at all
			return fmt.Errorf("%w: no saga found with idempotency key %s", ErrSuspendSagaNotFound, req.IdempotencyKey)
		}
		if err != nil {
			return fmt.Errorf("failed to find saga: %w", err)
		}

		response.SagaInstanceID = saga.ID

		// Check current status for idempotency
		if saga.Status != SagaStatusWaitingForEvent {
			// Already resumed - this is an idempotent no-op
			response.WasAlreadyCompleted = true
			response.NewStatus = saga.Status
			return nil
		}

		// Find the suspended step result to update
		var stepResult SagaStepResult
		if err := tx.Where("saga_instance_id = ? AND status = ?", saga.ID, StepStatusSuspended).
			Order("step_index DESC").
			First(&stepResult).Error; err != nil {
			return fmt.Errorf("failed to find suspended step result: %w", err)
		}

		// Update step result with callback data
		// Store both the result and the original suspend key for idempotency lookups
		resultData := toJSONB(req.Result)
		if resultData == nil {
			resultData = JSONB{}
		}
		resultData["_suspend_key"] = req.IdempotencyKey

		stepUpdates := map[string]interface{}{
			"status":     StepStatusCompleted,
			"result":     resultData,
			"updated_at": now,
		}
		if err := tx.Model(&stepResult).Updates(stepUpdates).Error; err != nil {
			return fmt.Errorf("failed to update step result: %w", err)
		}

		// Update saga instance:
		// - Status back to PENDING (will be claimed by next worker cycle)
		// - Clear suspend fields
		sagaUpdates := map[string]interface{}{
			"status":         SagaStatusPending,
			"suspend_reason": nil,
			"suspend_data":   nil,
			"updated_at":     now,
		}
		if err := tx.Model(&saga).Updates(sagaUpdates).Error; err != nil {
			return fmt.Errorf("failed to update saga instance: %w", err)
		}

		response.NewStatus = SagaStatusPending
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &response, nil
}

// FindSuspendedByIdempotencyKey finds a saga that's suspended with the given idempotency key.
func (s *SuspendService) FindSuspendedByIdempotencyKey(ctx context.Context, key string) (*SagaInstance, error) {
	var saga SagaInstance
	err := s.db.WithContext(ctx).
		Where("suspend_data->>'idempotency_key' = ?", key).
		Where("status = ?", SagaStatusWaitingForEvent).
		First(&saga).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil //nolint:nilnil // Standard Go pattern for "not found, no error"
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find suspended saga: %w", err)
	}
	return &saga, nil
}

// containsIgnoreCase checks if s contains substr, case-insensitively.
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// isDuplicateKeyError checks if the error is a duplicate key violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// PostgreSQL/CockroachDB duplicate key error codes/messages
	return containsIgnoreCase(errStr, "duplicate key") ||
		containsIgnoreCase(errStr, "unique constraint") ||
		containsIgnoreCase(errStr, "23505") // PostgreSQL unique_violation error code
}
