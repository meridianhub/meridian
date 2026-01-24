// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Errors for step execution.
var (
	// ErrStepPreviouslyFailed is returned when replaying a step that failed in a previous execution.
	ErrStepPreviouslyFailed = errors.New("step previously failed")
)

// StepHandler is a function that executes a saga step with the given input.
// It returns the step output and any error encountered.
type StepHandler func(ctx context.Context, input map[string]interface{}) (interface{}, error)

// StepResultRepository provides persistence operations for saga step results.
type StepResultRepository interface {
	// FindByIdempotencyKey looks up a step result by its idempotency key.
	// Returns nil, nil if not found (not an error condition).
	FindByIdempotencyKey(ctx context.Context, key string) (*SagaStepResult, error)

	// Save persists a step result to the database.
	Save(ctx context.Context, result *SagaStepResult) error
}

// InstanceRepository provides persistence operations for saga instances.
type InstanceRepository interface {
	// FindByID retrieves a saga instance by its ID.
	FindByID(ctx context.Context, id uuid.UUID) (*SagaInstance, error)

	// UpdateStepIndex updates the current step index and resets replay count.
	UpdateStepIndex(ctx context.Context, id uuid.UUID, stepIndex int) error
}

// TxContext represents a transaction context for atomic operations.
// It ensures step result persistence and step index update happen atomically.
type TxContext interface {
	// SaveStepResult persists a step result within the transaction.
	SaveStepResult(ctx context.Context, result *SagaStepResult) error

	// UpdateStepIndex updates the saga instance's current step index within the transaction.
	UpdateStepIndex(ctx context.Context, instanceID uuid.UUID, stepIndex int) error

	// Commit commits the transaction.
	Commit() error

	// Rollback aborts the transaction.
	Rollback() error
}

// TransactionalRepository provides transaction support for step execution.
type TransactionalRepository interface {
	// BeginTx starts a new database transaction.
	BeginTx(ctx context.Context) (TxContext, error)
}

// StepExecutor handles the execution of saga steps with idempotency and caching.
// It implements the replay mechanism specified in FR-13 and FR-17.
type StepExecutor struct {
	stepResultRepo StepResultRepository
	instanceRepo   InstanceRepository
	logger         *slog.Logger
}

// NewStepExecutor creates a new StepExecutor with the given repositories.
func NewStepExecutor(stepResultRepo StepResultRepository, instanceRepo InstanceRepository) *StepExecutor {
	return &StepExecutor{
		stepResultRepo: stepResultRepo,
		instanceRepo:   instanceRepo,
		logger:         slog.Default(),
	}
}

// WithLogger sets the logger for the step executor.
func (e *StepExecutor) WithLogger(logger *slog.Logger) *StepExecutor {
	e.logger = logger
	return e
}

// ExecuteStep executes a saga step with idempotency checking.
// If the step has already been executed (cache hit), returns the cached result.
// Otherwise, executes the handler, persists the result, and returns it.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - instance: The saga instance being executed
//   - stepIndex: The current step index (0-based)
//   - stepName: Human-readable step name for debugging
//   - handler: The function to execute for this step
//   - input: Input data for the step handler
//
// Returns:
//   - The step result (from cache or fresh execution)
//   - Error if execution or persistence fails
//
//nolint:gocognit,gocyclo // Complexity is inherent to idempotency and concurrent execution handling
func (e *StepExecutor) ExecuteStep(
	ctx context.Context,
	instance *SagaInstance,
	stepIndex int,
	stepName string,
	handler StepHandler,
	input map[string]interface{},
) (interface{}, error) {
	// Generate idempotency key: saga_{instance_id}_step_{index} (FR-13)
	idempotencyKey := FormatIdempotencyKey(instance.ID, stepIndex)

	e.logger.Debug("executing saga step",
		"saga_id", instance.ID,
		"step_index", stepIndex,
		"step_name", stepName,
		"idempotency_key", idempotencyKey,
	)

	// Check cache for existing result (replay case)
	cachedResult, err := e.stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("failed to check step result cache: %w", err)
	}

	if cachedResult != nil {
		e.logger.Info("cache hit for saga step, returning cached result",
			"saga_id", instance.ID,
			"step_index", stepIndex,
			"step_name", stepName,
			"cached_status", cachedResult.Status,
		)

		// If the cached result was a failure, return the error
		if cachedResult.Status == StepStatusFailed {
			if cachedResult.Error != nil {
				return nil, fmt.Errorf("%w: %s", ErrStepPreviouslyFailed, *cachedResult.Error)
			}
			return nil, ErrStepPreviouslyFailed
		}

		// Return the cached output snapshot
		// Convert JSONB to interface{} for compatibility
		output, hydrateErr := hydrateOutputSnapshot(cachedResult.Result)
		if hydrateErr != nil {
			return nil, fmt.Errorf("failed to hydrate cached result: %w", hydrateErr)
		}
		return output, nil
	}

	// Cache miss - execute the handler
	e.logger.Debug("cache miss, executing step handler",
		"saga_id", instance.ID,
		"step_index", stepIndex,
	)

	// Execute the handler
	output, handlerErr := handler(ctx, input)

	// Generate deterministic causation_id: UUIDv5 with saga instance as namespace (FR-17)
	causationID := GenerateCausationID(instance.ID, stepIndex)

	// Prepare step result for persistence
	now := time.Now()
	stepResult := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instance.ID,
		StepIndex:      stepIndex,
		StepName:       stepName,
		IdempotencyKey: idempotencyKey,
		CausationID:    &causationID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if handlerErr != nil {
		// Persist failure
		errStr := handlerErr.Error()
		stepResult.Status = StepStatusFailed
		stepResult.Error = &errStr
		stepResult.ErrorCategory = categorizeError(handlerErr)
	} else {
		// Deep-copy output before persistence to prevent pointer leaks (FR-31)
		deepCopiedOutput, copyErr := DeepCopyJSON(output)
		if copyErr != nil {
			return nil, fmt.Errorf("failed to deep-copy step output: %w", copyErr)
		}

		stepResult.Status = StepStatusCompleted
		stepResult.Result = toJSONB(deepCopiedOutput)
	}

	// Persist the step result
	if err := e.stepResultRepo.Save(ctx, stepResult); err != nil {
		// Handle concurrent first runs: if another worker already persisted,
		// re-query cache and return the persisted result (idempotency guarantee)
		cachedResult, cacheErr := e.stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
		if cacheErr != nil {
			return nil, fmt.Errorf("failed to persist step result: %w (cache re-check also failed: %w)", err, cacheErr)
		}
		if cachedResult != nil {
			e.logger.Info("concurrent execution detected, returning winner's result",
				"saga_id", instance.ID,
				"step_index", stepIndex,
				"step_name", stepName,
			)
			if cachedResult.Status == StepStatusFailed {
				if cachedResult.Error != nil {
					return nil, fmt.Errorf("%w: %s", ErrStepPreviouslyFailed, *cachedResult.Error)
				}
				return nil, ErrStepPreviouslyFailed
			}
			output, hydrateErr := hydrateOutputSnapshot(cachedResult.Result)
			if hydrateErr != nil {
				return nil, fmt.Errorf("failed to hydrate cached result after concurrent save: %w", hydrateErr)
			}
			return output, nil
		}
		// No cached result found despite save failure - propagate original error
		return nil, fmt.Errorf("failed to persist step result: %w", err)
	}

	// Update the saga instance's current step index (if successful)
	if handlerErr == nil {
		if err := e.instanceRepo.UpdateStepIndex(ctx, instance.ID, stepIndex+1); err != nil {
			return nil, fmt.Errorf("failed to update saga step index: %w", err)
		}
	}

	if handlerErr != nil {
		return nil, handlerErr
	}

	return output, nil
}

// TransactionalStepExecutor extends StepExecutor with transaction support.
// It ensures step result and step index updates happen atomically.
type TransactionalStepExecutor struct {
	txRepo         TransactionalRepository
	stepResultRepo StepResultRepository
	logger         *slog.Logger
}

// NewTransactionalStepExecutor creates a new TransactionalStepExecutor.
func NewTransactionalStepExecutor(txRepo TransactionalRepository) *TransactionalStepExecutor {
	return &TransactionalStepExecutor{
		txRepo: txRepo,
		logger: slog.Default(),
	}
}

// WithStepResultRepo sets the step result repository for cache lookups.
func (e *TransactionalStepExecutor) WithStepResultRepo(repo StepResultRepository) *TransactionalStepExecutor {
	e.stepResultRepo = repo
	return e
}

// ExecuteStepInTx executes a saga step within a transaction.
// This ensures atomic persistence of both the step result and the step index update.
//
// Transaction affinity (CRITICAL per task requirements):
//  1. Insert saga_step_results
//  2. Update saga_instances.current_step_index
//  3. Both in SAME transaction
//
// On commit failure, no partial state is persisted.
//
//nolint:gocognit,gocyclo // Complexity is inherent to transaction handling; extraction would reduce clarity
func (e *TransactionalStepExecutor) ExecuteStepInTx(
	ctx context.Context,
	instance *SagaInstance,
	stepIndex int,
	stepName string,
	handler StepHandler,
	input map[string]interface{},
) (interface{}, error) {
	idempotencyKey := FormatIdempotencyKey(instance.ID, stepIndex)

	// Check cache first (outside transaction for efficiency)
	if e.stepResultRepo != nil {
		cachedResult, err := e.stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
		if err != nil {
			return nil, fmt.Errorf("failed to check step result cache: %w", err)
		}
		if cachedResult != nil {
			e.logger.Info("cache hit for saga step (transactional)",
				"saga_id", instance.ID,
				"step_index", stepIndex,
			)
			if cachedResult.Status == StepStatusFailed {
				if cachedResult.Error != nil {
					return nil, fmt.Errorf("%w: %s", ErrStepPreviouslyFailed, *cachedResult.Error)
				}
				return nil, ErrStepPreviouslyFailed
			}
			output, hydrateErr := hydrateOutputSnapshot(cachedResult.Result)
			if hydrateErr != nil {
				return nil, fmt.Errorf("failed to hydrate cached result: %w", hydrateErr)
			}
			return output, nil
		}
	}

	// Execute the handler
	output, handlerErr := handler(ctx, input)

	// Generate deterministic causation_id (FR-17)
	causationID := GenerateCausationID(instance.ID, stepIndex)

	// Prepare step result
	now := time.Now()
	stepResult := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instance.ID,
		StepIndex:      stepIndex,
		StepName:       stepName,
		IdempotencyKey: idempotencyKey,
		CausationID:    &causationID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if handlerErr != nil {
		errStr := handlerErr.Error()
		stepResult.Status = StepStatusFailed
		stepResult.Error = &errStr
		stepResult.ErrorCategory = categorizeError(handlerErr)
	} else {
		// Deep-copy output (FR-31)
		deepCopiedOutput, copyErr := DeepCopyJSON(output)
		if copyErr != nil {
			return nil, fmt.Errorf("failed to deep-copy step output: %w", copyErr)
		}
		stepResult.Status = StepStatusCompleted
		stepResult.Result = toJSONB(deepCopiedOutput)
	}

	// Begin transaction
	tx, err := e.txRepo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Ensure cleanup on panic or error
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 1. Save step result in transaction
	if err := tx.SaveStepResult(ctx, stepResult); err != nil {
		return nil, fmt.Errorf("failed to save step result in transaction: %w", err)
	}

	// 2. Update step index in transaction (only on success)
	if handlerErr == nil {
		if err := tx.UpdateStepIndex(ctx, instance.ID, stepIndex+1); err != nil {
			return nil, fmt.Errorf("failed to update step index in transaction: %w", err)
		}
	}

	// 3. Commit the transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true

	if handlerErr != nil {
		return nil, handlerErr
	}

	return output, nil
}

// FormatIdempotencyKey generates an idempotency key in the format: saga_{instance_id}_step_{index}
// This format is specified in FR-13 of the PRD.
func FormatIdempotencyKey(instanceID uuid.UUID, stepIndex int) string {
	return fmt.Sprintf("saga_%s_step_%d", instanceID.String(), stepIndex)
}

// GenerateCausationID creates a deterministic UUIDv5 causation ID.
// The saga instance ID is used as the namespace, and the step index as the name.
// This ensures replay produces identical causation_ids (FR-17).
func GenerateCausationID(instanceID uuid.UUID, stepIndex int) uuid.UUID {
	// Use the instance ID as the namespace
	// Use the step index (as string) as the name
	name := strconv.Itoa(stepIndex)
	return uuid.NewSHA1(instanceID, []byte(name))
}

// DeepCopyJSON performs a deep copy of a value by marshaling to JSON and back.
// This ensures the copied value is completely independent of the original,
// preventing Go pointer leaks where Starlark could modify persisted state (FR-31).
//
//nolint:nilnil // Returning nil, nil is intentional for nil input
func DeepCopyJSON(v interface{}) (interface{}, error) {
	if v == nil {
		return nil, nil
	}

	// Marshal to JSON
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal for deep copy: %w", err)
	}

	// Unmarshal back to a new value
	var result interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal for deep copy: %w", err)
	}

	return result, nil
}

// hydrateOutputSnapshot converts a persisted JSONB result back to interface{}.
// This is used when returning cached results during replay.
// It performs a deep copy to ensure nested maps/slices are not shared,
// preserving immutability of the cached data.
//
//nolint:nilnil // Returning nil, nil is intentional for nil input (consistent with DeepCopyJSON)
func hydrateOutputSnapshot(jsonb JSONB) (interface{}, error) {
	if jsonb == nil {
		return nil, nil
	}
	// Use DeepCopyJSON to ensure nested structures are fully copied
	// A shallow copy would leave nested maps/slices shared, allowing mutation
	return DeepCopyJSON(jsonb)
}

// toJSONB converts an interface{} to JSONB for persistence.
func toJSONB(v interface{}) JSONB {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case map[string]interface{}:
		result := make(JSONB, len(val))
		for k, v := range val {
			result[k] = v
		}
		return result
	case JSONB:
		return val
	default:
		// For non-map types, wrap in a result key
		return JSONB{"_value": v}
	}
}

// categorizeError determines if an error is TRANSIENT (retryable) or FATAL (FR-28).
// Returns a pointer to the error category string, or nil if uncategorized.
func categorizeError(err error) *string {
	if err == nil {
		return nil
	}

	// Check for common transient error patterns
	errStr := err.Error()

	// Network/timeout errors are transient
	transientPatterns := []string{
		"timeout",
		"deadline exceeded",
		"connection refused",
		"connection reset",
		"temporary failure",
		"unavailable",
		"retry",
		"EAGAIN",
		"ETIMEDOUT",
	}

	for _, pattern := range transientPatterns {
		if containsIgnoreCase(errStr, pattern) {
			cat := string(ErrorCategoryTransient)
			return &cat
		}
	}

	// Default to fatal for unrecognized errors
	cat := string(ErrorCategoryFatal)
	return &cat
}

// containsIgnoreCase checks if s contains substr (case-insensitive).
func containsIgnoreCase(s, substr string) bool {
	sLower := make([]byte, len(s))
	substrLower := make([]byte, len(substr))

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		sLower[i] = c
	}

	for i := 0; i < len(substr); i++ {
		c := substr[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		substrLower[i] = c
	}

	return containsBytes(sLower, substrLower)
}

func containsBytes(s, substr []byte) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
