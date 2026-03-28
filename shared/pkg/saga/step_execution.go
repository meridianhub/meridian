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
	idempotencyKey := FormatIdempotencyKey(instance.ID, stepIndex)

	e.logger.Debug("executing saga step",
		"saga_id", instance.ID,
		"step_index", stepIndex,
		"step_name", stepName,
		"idempotency_key", idempotencyKey,
	)

	// Check cache for existing result (replay case)
	if output, err := e.returnCachedResult(ctx, instance.ID, stepIndex, stepName, idempotencyKey); err != nil || output != nil {
		return output, err
	}

	e.logger.Debug("cache miss, executing step handler",
		"saga_id", instance.ID,
		"step_index", stepIndex,
	)

	output, handlerErr := handler(ctx, input)

	stepResult := buildStepResult(instance.ID, stepIndex, stepName, idempotencyKey, output, handlerErr)

	// Persist the step result
	if err := e.stepResultRepo.Save(ctx, stepResult); err != nil {
		return e.handleConcurrentSave(ctx, instance.ID, stepIndex, stepName, idempotencyKey, err)
	}

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

// returnCachedResult checks the step result cache and returns the cached output if found.
// Returns (nil, nil) on cache miss, signaling the caller to proceed with execution.
//
//nolint:nilnil // nil, nil signals cache miss
func (e *StepExecutor) returnCachedResult(ctx context.Context, sagaID uuid.UUID, stepIndex int, stepName, idempotencyKey string) (interface{}, error) {
	cachedResult, err := e.stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("failed to check step result cache: %w", err)
	}
	if cachedResult == nil {
		return nil, nil
	}

	e.logger.Info("cache hit for saga step, returning cached result",
		"saga_id", sagaID,
		"step_index", stepIndex,
		"step_name", stepName,
		"cached_status", cachedResult.Status,
	)

	return returnFromCachedStepResult(cachedResult)
}

// returnFromCachedStepResult converts a cached step result into an output or error.
func returnFromCachedStepResult(cachedResult *SagaStepResult) (interface{}, error) {
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

// buildStepResult creates a SagaStepResult from handler output and error.
func buildStepResult(sagaID uuid.UUID, stepIndex int, stepName, idempotencyKey string, output interface{}, handlerErr error) *SagaStepResult {
	causationID := GenerateCausationID(sagaID, stepIndex)
	now := time.Now()
	stepResult := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: sagaID,
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
		stepResult.SetErrorCategory(ClassifyError(handlerErr))
	} else {
		deepCopiedOutput, _ := DeepCopyJSON(output) // Error handled at callsite where relevant
		stepResult.Status = StepStatusCompleted
		stepResult.Result = toJSONB(deepCopiedOutput)
	}

	return stepResult
}

// handleConcurrentSave handles the case where persisting a step result fails due
// to a concurrent worker. It re-queries the cache and returns the winner's result.
func (e *StepExecutor) handleConcurrentSave(ctx context.Context, sagaID uuid.UUID, stepIndex int, stepName, idempotencyKey string, saveErr error) (interface{}, error) {
	cachedResult, cacheErr := e.stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	if cacheErr != nil {
		return nil, fmt.Errorf("failed to persist step result: %w (cache re-check also failed: %w)", saveErr, cacheErr)
	}
	if cachedResult != nil {
		e.logger.Info("concurrent execution detected, returning winner's result",
			"saga_id", sagaID,
			"step_index", stepIndex,
			"step_name", stepName,
		)
		return returnFromCachedStepResult(cachedResult)
	}
	return nil, fmt.Errorf("failed to persist step result: %w", saveErr)
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
	if cached, err := e.checkCache(ctx, instance.ID, stepIndex, idempotencyKey); cached != nil || err != nil {
		return cached, err
	}

	output, handlerErr := handler(ctx, input)

	stepResult, err := buildStepResultWithDeepCopy(instance.ID, stepIndex, stepName, idempotencyKey, output, handlerErr)
	if err != nil {
		return nil, err
	}

	// Begin transaction
	tx, txErr := e.txRepo.BeginTx(ctx)
	if txErr != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", txErr)
	}

	if err := e.commitStepInTx(ctx, tx, instance.ID, stepIndex, stepResult, handlerErr); err != nil {
		return nil, err
	}

	if handlerErr != nil {
		return nil, handlerErr
	}

	return output, nil
}

// checkCache looks up a cached step result if a step result repo is configured.
// Returns (nil, nil) on cache miss.
//
//nolint:nilnil // nil, nil signals cache miss
func (e *TransactionalStepExecutor) checkCache(ctx context.Context, sagaID uuid.UUID, stepIndex int, idempotencyKey string) (interface{}, error) {
	if e.stepResultRepo == nil {
		return nil, nil
	}

	cachedResult, err := e.stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("failed to check step result cache: %w", err)
	}
	if cachedResult == nil {
		return nil, nil
	}

	e.logger.Info("cache hit for saga step (transactional)",
		"saga_id", sagaID,
		"step_index", stepIndex,
	)

	return returnFromCachedStepResult(cachedResult)
}

// buildStepResultWithDeepCopy builds a step result, deep-copying the output on success.
func buildStepResultWithDeepCopy(sagaID uuid.UUID, stepIndex int, stepName, idempotencyKey string, output interface{}, handlerErr error) (*SagaStepResult, error) {
	if handlerErr == nil {
		deepCopiedOutput, copyErr := DeepCopyJSON(output)
		if copyErr != nil {
			return nil, fmt.Errorf("failed to deep-copy step output: %w", copyErr)
		}
		output = deepCopiedOutput
	}
	return buildStepResult(sagaID, stepIndex, stepName, idempotencyKey, output, handlerErr), nil
}

// commitStepInTx saves the step result and updates the step index within a transaction.
func (e *TransactionalStepExecutor) commitStepInTx(ctx context.Context, tx TxContext, instanceID uuid.UUID, stepIndex int, stepResult *SagaStepResult, handlerErr error) error {
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := tx.SaveStepResult(ctx, stepResult); err != nil {
		return fmt.Errorf("failed to save step result in transaction: %w", err)
	}

	if handlerErr == nil {
		if err := tx.UpdateStepIndex(ctx, instanceID, stepIndex+1); err != nil {
			return fmt.Errorf("failed to update step index in transaction: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true

	return nil
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
	if cached, err := e.checkCache(ctx, instance.ID, stepIndex, idempotencyKey); cached != nil || err != nil {
		return cached, err
	}

	output, handlerErr := handler(ctx, input)
	causationID := GenerateCausationID(instance.ID, stepIndex)

	stepResult, sagaEvent, err := buildStepResultWithEvent(instance, stepIndex, stepName, idempotencyKey, causationID, output, handlerErr)
	if err != nil {
		return nil, err
	}

	if err := e.commitStepWithOutbox(ctx, txWithOutbox, instance, stepIndex, stepResult, sagaEvent, causationID, handlerErr); err != nil {
		return nil, err
	}

	if handlerErr != nil {
		return nil, handlerErr
	}

	return output, nil
}

// buildStepResultWithEvent constructs both the step result and the saga event for outbox publishing.
func buildStepResultWithEvent(
	instance *SagaInstance,
	stepIndex int,
	stepName, idempotencyKey string,
	causationID uuid.UUID,
	output interface{},
	handlerErr error,
) (*SagaStepResult, Event, error) {
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
		stepResult.SetErrorCategory(ClassifyError(handlerErr))

		errCat := ErrorCategoryFatal
		if stepResult.ErrorCategory != nil && *stepResult.ErrorCategory == string(ErrorCategoryTransient) {
			errCat = ErrorCategoryTransient
		}
		event := NewStepFailedEvent(instance.ID, instance.CorrelationID, causationID, stepIndex, stepName, errStr, errCat)
		return stepResult, event, nil
	}

	deepCopiedOutput, copyErr := DeepCopyJSON(output)
	if copyErr != nil {
		return nil, nil, fmt.Errorf("failed to deep-copy step output: %w", copyErr)
	}
	stepResult.Status = StepStatusCompleted
	stepResult.Result = toJSONB(deepCopiedOutput)

	event := NewStepCompletedEvent(instance.ID, instance.CorrelationID, causationID, stepIndex, stepName, deepCopiedOutput)
	return stepResult, event, nil
}

// commitStepWithOutbox saves step result, writes outbox entry, and updates step index within a transaction.
func (e *TransactionalStepExecutor) commitStepWithOutbox(
	ctx context.Context,
	txWithOutbox TxContextWithOutbox,
	instance *SagaInstance,
	stepIndex int,
	stepResult *SagaStepResult,
	sagaEvent Event,
	causationID uuid.UUID,
	handlerErr error,
) error {
	committed := false
	defer func() {
		if !committed {
			_ = txWithOutbox.Rollback()
		}
	}()

	if err := txWithOutbox.SaveStepResult(ctx, stepResult); err != nil {
		return fmt.Errorf("failed to save step result in transaction: %w", err)
	}

	outboxEntry := createOutboxEntry(sagaEvent, instance.CorrelationID, causationID)
	if err := txWithOutbox.WriteOutboxEntry(ctx, outboxEntry); err != nil {
		return fmt.Errorf("failed to write outbox entry in transaction: %w", err)
	}

	e.logger.Debug("outbox entry written atomically with step result",
		"saga_id", instance.ID,
		"step_index", stepIndex,
		"event_type", sagaEvent.EventType(),
		"correlation_id", instance.CorrelationID,
	)

	if handlerErr == nil {
		if err := txWithOutbox.UpdateStepIndex(ctx, instance.ID, stepIndex+1); err != nil {
			return fmt.Errorf("failed to update step index in transaction: %w", err)
		}
	}

	if err := txWithOutbox.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true

	return nil
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
