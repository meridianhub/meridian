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

// TransactionalRepositoryWithOutbox extends TransactionalRepository with outbox support.
// Use this when you need to write saga events atomically with step results (FR-31).
type TransactionalRepositoryWithOutbox interface {
	// BeginTxWithOutbox starts a new database transaction with outbox writing capability.
	BeginTxWithOutbox(ctx context.Context) (TxContextWithOutbox, error)
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
		// Persist failure with error classification (FR-28)
		errStr := handlerErr.Error()
		stepResult.Status = StepStatusFailed
		stepResult.Error = &errStr
		stepResult.SetErrorCategory(ClassifyError(handlerErr))
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
	eventPublisher EventPublisher
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

// WithEventPublisher sets the event publisher for saga event emission (FR-24, FR-25).
// When set, step completion and failure events are published via the outbox pattern.
func (e *TransactionalStepExecutor) WithEventPublisher(publisher EventPublisher) *TransactionalStepExecutor {
	e.eventPublisher = publisher
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
		// Persist failure with error classification (FR-28)
		errStr := handlerErr.Error()
		stepResult.Status = StepStatusFailed
		stepResult.Error = &errStr
		stepResult.SetErrorCategory(ClassifyError(handlerErr))
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

// ExecuteStepWithOutbox executes a saga step with atomic outbox event publishing.
// This method extends ExecuteStepInTx by also writing saga events to the outbox
// within the same database transaction, ensuring exactly-once event delivery (FR-31).
//
// Transaction affinity (CRITICAL):
//  1. Insert saga_step_results
//  2. Insert event_outbox entry for step completion/failure event
//  3. Update saga_instances.current_step_index
//  4. ALL in SAME transaction
//
// The outbox entry is processed by a background worker that publishes to Kafka.
// If the pod crashes between Kafka publish and marking the entry as processed,
// the entry will be republished (at-least-once, with idempotency).
func (e *TransactionalStepExecutor) ExecuteStepWithOutbox(
	ctx context.Context,
	instance *SagaInstance,
	stepIndex int,
	stepName string,
	handler StepHandler,
	input map[string]interface{},
	txWithOutbox TxContextWithOutbox,
) (interface{}, error) {
	idempotencyKey := FormatIdempotencyKey(instance.ID, stepIndex)

	// Check cache first (outside transaction for efficiency)
	if e.stepResultRepo != nil {
		cachedResult, err := e.stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
		if err != nil {
			return nil, fmt.Errorf("failed to check step result cache: %w", err)
		}
		if cachedResult != nil {
			e.logger.Info("cache hit for saga step (with outbox)",
				"saga_id", instance.ID,
				"step_index", stepIndex,
				"correlation_id", instance.CorrelationID,
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

	// Prepare the saga event for the outbox
	var sagaEvent Event

	if handlerErr != nil {
		errStr := handlerErr.Error()
		stepResult.Status = StepStatusFailed
		stepResult.Error = &errStr
		stepResult.SetErrorCategory(ClassifyError(handlerErr))

		// Create step failed event
		errCat := ErrorCategoryFatal
		if stepResult.ErrorCategory != nil && *stepResult.ErrorCategory == string(ErrorCategoryTransient) {
			errCat = ErrorCategoryTransient
		}
		sagaEvent = NewStepFailedEvent(
			instance.ID,
			instance.CorrelationID,
			causationID,
			stepIndex,
			stepName,
			errStr,
			errCat,
		)
	} else {
		// Deep-copy output (FR-31)
		deepCopiedOutput, copyErr := DeepCopyJSON(output)
		if copyErr != nil {
			return nil, fmt.Errorf("failed to deep-copy step output: %w", copyErr)
		}
		stepResult.Status = StepStatusCompleted
		stepResult.Result = toJSONB(deepCopiedOutput)

		// Create step completed event
		sagaEvent = NewStepCompletedEvent(
			instance.ID,
			instance.CorrelationID,
			causationID,
			stepIndex,
			stepName,
			deepCopiedOutput,
		)
	}

	// Ensure cleanup on panic or error
	committed := false
	defer func() {
		if !committed {
			_ = txWithOutbox.Rollback()
		}
	}()

	// 1. Save step result in transaction
	if err := txWithOutbox.SaveStepResult(ctx, stepResult); err != nil {
		return nil, fmt.Errorf("failed to save step result in transaction: %w", err)
	}

	// 2. Write outbox entry in SAME transaction (FR-31 atomicity guarantee)
	outboxEntry := createOutboxEntry(sagaEvent, instance.CorrelationID, causationID)
	if err := txWithOutbox.WriteOutboxEntry(ctx, outboxEntry); err != nil {
		return nil, fmt.Errorf("failed to write outbox entry in transaction: %w", err)
	}

	e.logger.Debug("outbox entry written atomically with step result",
		"saga_id", instance.ID,
		"step_index", stepIndex,
		"event_type", sagaEvent.EventType(),
		"correlation_id", instance.CorrelationID,
	)

	// 3. Update step index in transaction (only on success)
	if handlerErr == nil {
		if err := txWithOutbox.UpdateStepIndex(ctx, instance.ID, stepIndex+1); err != nil {
			return nil, fmt.Errorf("failed to update step index in transaction: %w", err)
		}
	}

	// 4. Commit the transaction - step result, outbox entry, and step index all committed together
	if err := txWithOutbox.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true

	if handlerErr != nil {
		return nil, handlerErr
	}

	return output, nil
}

// createOutboxEntry creates an OutboxEntry from an Event.
func createOutboxEntry(event Event, correlationID, causationID uuid.UUID) *OutboxEntry {
	payload, _ := json.Marshal(event) // Event types are designed to be JSON-serializable

	entry := &OutboxEntry{
		ID:            uuid.New(),
		EventType:     string(event.EventType()),
		AggregateID:   event.SagaID().String(),
		AggregateType: "SagaInstance",
		EventPayload:  payload,
		CorrelationID: correlationID.String(),
		CausationID:   causationID.String(),
		Topic:         "saga.events.v1", // Default topic, can be configured
		ServiceName:   "saga",           // Default service name, can be configured
	}

	return entry
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
