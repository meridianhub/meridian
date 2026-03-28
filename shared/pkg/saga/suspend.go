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
	if err := validateSuspendInputs(instance, req); err != nil {
		return nil, err
	}

	timeout := clampTimeout(req.Timeout, s.config)
	now := time.Now()
	timeoutAt := now.Add(timeout)

	stepResult := buildSuspendedStepResult(instance, stepIndex, stepName, req, now)

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.commitSuspension(tx, stepResult, instance.ID, req, timeoutAt, now)
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

// validateSuspendInputs checks that instance and request are non-nil and that the idempotency key is present.
func validateSuspendInputs(instance *SagaInstance, req *SuspendRequest) error {
	if instance == nil {
		return fmt.Errorf("%w: saga instance is required", ErrSuspendSagaNotFound)
	}
	if req == nil {
		return fmt.Errorf("%w: suspend request is required", ErrIdempotencyKeyRequired)
	}
	if req.IdempotencyKey == "" {
		return ErrIdempotencyKeyRequired
	}
	return nil
}

// clampTimeout applies default and maximum bounds to the requested timeout.
func clampTimeout(requested time.Duration, cfg *SuspendConfig) time.Duration {
	timeout := requested
	if timeout <= 0 {
		timeout = cfg.DefaultTimeout
	}
	if timeout > cfg.MaxTimeout {
		timeout = cfg.MaxTimeout
	}
	return timeout
}

// buildSuspendedStepResult creates a SagaStepResult with SUSPENDED status and suspend request data.
func buildSuspendedStepResult(instance *SagaInstance, stepIndex int, stepName string, req *SuspendRequest, now time.Time) *SagaStepResult {
	stepIdempotencyKey := FormatIdempotencyKey(instance.ID, stepIndex)
	causationID := GenerateCausationID(instance.ID, stepIndex)

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

	stepResult.Result = JSONB{
		"idempotency_key": req.IdempotencyKey,
	}
	if req.Data != nil {
		for k, v := range req.Data {
			stepResult.Result[k] = v
		}
	}

	return stepResult
}

// commitSuspension saves the step result and updates the saga instance within a transaction.
func (s *SuspendService) commitSuspension(tx *gorm.DB, stepResult *SagaStepResult, sagaID uuid.UUID, req *SuspendRequest, timeoutAt, now time.Time) error {
	if err := tx.Create(stepResult).Error; err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("%w: %s", ErrDuplicateIdempotencyKey, req.IdempotencyKey)
		}
		return fmt.Errorf("failed to save suspended step result: %w", err)
	}

	updates := map[string]interface{}{
		"status":           SagaStatusWaitingForEvent,
		"suspend_reason":   req.Reason,
		"suspend_data":     JSONB{"idempotency_key": req.IdempotencyKey, "timeout_at": timeoutAt},
		"updated_at":       now,
		"claimed_by_pod":   nil,
		"claimed_at":       nil,
		"lease_expires_at": nil,
	}

	result := tx.Model(&SagaInstance{}).
		Where("id = ?", sagaID).
		Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("failed to update saga instance: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrSuspendSagaNotFound
	}

	return nil
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
		saga, alreadyCompleted, err := s.findSagaByIdempotencyKey(tx, req)
		if err != nil {
			return err
		}
		if alreadyCompleted {
			response.SagaInstanceID = saga.ID
			response.WasAlreadyCompleted = true
			response.NewStatus = saga.Status
			return nil
		}

		response.SagaInstanceID = saga.ID

		// Check current status for idempotency
		if saga.Status != SagaStatusWaitingForEvent {
			response.WasAlreadyCompleted = true
			response.NewStatus = saga.Status
			return nil
		}

		if err := s.resumeSuspendedSaga(tx, saga, req, now); err != nil {
			return err
		}

		response.NewStatus = SagaStatusPending
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &response, nil
}

// findSagaByIdempotencyKey looks up a saga by suspend idempotency key. First tries to find
// a saga still WAITING_FOR_EVENT, then falls back to checking step results for idempotent calls.
// Returns (saga, alreadyCompleted, error).
func (s *SuspendService) findSagaByIdempotencyKey(tx *gorm.DB, req *CompleteSagaStepRequest) (*SagaInstance, bool, error) {
	// Strategy 1: Find saga by suspend idempotency key in suspend_data (still waiting)
	var saga SagaInstance
	query := tx.Where("suspend_data->>'idempotency_key' = ?", req.IdempotencyKey)
	if req.SagaInstanceID != nil {
		query = query.Where("id = ?", *req.SagaInstanceID)
	}

	err := query.Clauses(clause.Locking{Strength: "UPDATE"}).First(&saga).Error

	if err == nil {
		return &saga, false, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, fmt.Errorf("failed to find saga: %w", err)
	}

	// Strategy 2: Check if this idempotency key was already processed via step results
	var stepWithKey SagaStepResult
	stepQuery := tx.Where("result->>'idempotency_key' = ? OR result->>'_suspend_key' = ?",
		req.IdempotencyKey, req.IdempotencyKey)
	if req.SagaInstanceID != nil {
		stepQuery = stepQuery.Where("saga_instance_id = ?", *req.SagaInstanceID)
	}
	lookupErr := stepQuery.Order("created_at DESC").First(&stepWithKey).Error

	if lookupErr == nil {
		if sagaErr := tx.First(&saga, "id = ?", stepWithKey.SagaInstanceID).Error; sagaErr != nil {
			return nil, false, fmt.Errorf("failed to find saga for step result: %w", sagaErr)
		}
		return &saga, true, nil
	}

	return nil, false, fmt.Errorf("%w: no saga found with idempotency key %s", ErrSuspendSagaNotFound, req.IdempotencyKey)
}

// resumeSuspendedSaga updates the step result and saga instance to resume from suspension.
func (s *SuspendService) resumeSuspendedSaga(tx *gorm.DB, saga *SagaInstance, req *CompleteSagaStepRequest, now time.Time) error {
	var stepResult SagaStepResult
	if err := tx.Where("saga_instance_id = ? AND status = ?", saga.ID, StepStatusSuspended).
		Order("step_index DESC").
		First(&stepResult).Error; err != nil {
		return fmt.Errorf("failed to find suspended step result: %w", err)
	}

	resultData := toJSONB(req.Result)
	if resultData == nil {
		resultData = JSONB{}
	}
	resultData["_suspend_key"] = req.IdempotencyKey

	if err := tx.Model(&stepResult).Updates(map[string]interface{}{
		"status":     StepStatusCompleted,
		"result":     resultData,
		"updated_at": now,
	}).Error; err != nil {
		return fmt.Errorf("failed to update step result: %w", err)
	}

	if err := tx.Model(saga).Updates(map[string]interface{}{
		"status":         SagaStatusPending,
		"suspend_reason": nil,
		"suspend_data":   nil,
		"updated_at":     now,
	}).Error; err != nil {
		return fmt.Errorf("failed to update saga instance: %w", err)
	}

	return nil
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
