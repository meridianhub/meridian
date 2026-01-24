package saga

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test-specific sentinel errors.
var (
	errStepExecutionFailed   = errors.New("step execution failed")
	errSimulatedCommitFailed = errors.New("simulated commit failure")
)

// TestIdempotencyKeyFormat verifies the idempotency key format is saga_{instance_id}_step_{index} (FR-13).
func TestIdempotencyKeyFormat(t *testing.T) {
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	stepIndex := 5

	key := FormatIdempotencyKey(instanceID, stepIndex)

	expected := "saga_550e8400-e29b-41d4-a716-446655440000_step_5"
	assert.Equal(t, expected, key, "Idempotency key format should be saga_{instance_id}_step_{index}")
}

// TestDeterministicCausationID verifies that causation_id is deterministic using UUIDv5 (FR-17).
func TestDeterministicCausationID(t *testing.T) {
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	stepIndex := 3

	// Generate causation ID twice
	causationID1 := GenerateCausationID(instanceID, stepIndex)
	causationID2 := GenerateCausationID(instanceID, stepIndex)

	// Must be deterministic - same inputs produce same output
	assert.Equal(t, causationID1, causationID2, "Causation IDs should be deterministic (same input = same output)")

	// Different step index should produce different causation ID
	causationID3 := GenerateCausationID(instanceID, 4)
	assert.NotEqual(t, causationID1, causationID3, "Different step indices should produce different causation IDs")

	// Different instance ID should produce different causation ID
	otherInstanceID := uuid.MustParse("660e8400-e29b-41d4-a716-446655440000")
	causationID4 := GenerateCausationID(otherInstanceID, stepIndex)
	assert.NotEqual(t, causationID1, causationID4, "Different instance IDs should produce different causation IDs")
}

// TestCausationIDIsUUIDv5 verifies the causation ID is a valid UUIDv5.
func TestCausationIDIsUUIDv5(t *testing.T) {
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	causationID := GenerateCausationID(instanceID, 0)

	// UUIDv5 should have version 5 (bits 12-15 of time_hi_and_version)
	version := causationID[6] >> 4
	assert.Equal(t, uint8(5), version, "Causation ID should be UUIDv5 (version 5)")
}

// TestDeepCopySerialization verifies output snapshot is deep-copied to prevent pointer leaks (FR-31).
func TestDeepCopySerialization(t *testing.T) {
	original := map[string]interface{}{
		"account_id":  "ACC-123",
		"balance":     1000.50,
		"nested_data": map[string]interface{}{"key": "value"},
	}

	// Deep copy via JSON marshal/unmarshal
	copied, err := DeepCopyJSON(original)
	require.NoError(t, err)

	// Modify the original after copying
	original["account_id"] = "MODIFIED"
	nestedMap := original["nested_data"].(map[string]interface{})
	nestedMap["key"] = "modified_value"

	// Verify the copy is unaffected
	copiedMap := copied.(map[string]interface{})
	assert.Equal(t, "ACC-123", copiedMap["account_id"], "Deep copy should be independent of original")
	copiedNested := copiedMap["nested_data"].(map[string]interface{})
	assert.Equal(t, "value", copiedNested["key"], "Nested deep copy should be independent")
}

// TestDeepCopyHandlesNil verifies DeepCopyJSON handles nil input gracefully.
func TestDeepCopyHandlesNil(t *testing.T) {
	result, err := DeepCopyJSON(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

// --- Step Executor Tests ---

// MockStepHandler is a configurable mock for testing step execution.
type MockStepHandler struct {
	mu            sync.Mutex
	CallCount     int
	ReturnValue   interface{}
	ReturnError   error
	ExecutionTime time.Duration
	OnCall        func(ctx context.Context, input map[string]interface{}) // Optional callback
}

func (m *MockStepHandler) Execute(ctx context.Context, input map[string]interface{}) (interface{}, error) {
	m.mu.Lock()
	m.CallCount++
	m.mu.Unlock()

	if m.OnCall != nil {
		m.OnCall(ctx, input)
	}

	if m.ExecutionTime > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.ExecutionTime):
		}
	}

	return m.ReturnValue, m.ReturnError
}

// MockStepResultRepository is an in-memory repository for testing.
type MockStepResultRepository struct {
	mu      sync.Mutex
	results map[string]*SagaStepResult
	saveErr error
}

func NewMockStepResultRepository() *MockStepResultRepository {
	return &MockStepResultRepository{
		results: make(map[string]*SagaStepResult),
	}
}

func (r *MockStepResultRepository) FindByIdempotencyKey(_ context.Context, key string) (*SagaStepResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result, exists := r.results[key]
	if !exists {
		return nil, nil // Not found is not an error
	}
	return result, nil
}

func (r *MockStepResultRepository) Save(_ context.Context, result *SagaStepResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	r.results[result.IdempotencyKey] = result
	return nil
}

func (r *MockStepResultRepository) SetSaveError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveErr = err
}

func (r *MockStepResultRepository) GetResult(key string) *SagaStepResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.results[key]
}

// MockSagaInstanceRepository for updating saga instance state.
type MockSagaInstanceRepository struct {
	mu        sync.Mutex
	instances map[uuid.UUID]*SagaInstance
	updateErr error
}

func NewMockSagaInstanceRepository() *MockSagaInstanceRepository {
	return &MockSagaInstanceRepository{
		instances: make(map[uuid.UUID]*SagaInstance),
	}
}

func (r *MockSagaInstanceRepository) FindByID(_ context.Context, id uuid.UUID) (*SagaInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, exists := r.instances[id]
	if !exists {
		return nil, nil
	}
	return instance, nil
}

func (r *MockSagaInstanceRepository) UpdateStepIndex(_ context.Context, id uuid.UUID, stepIndex int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updateErr != nil {
		return r.updateErr
	}
	if instance, exists := r.instances[id]; exists {
		instance.CurrentStepIndex = stepIndex
		instance.ReplayCount = 0
	}
	return nil
}

func (r *MockSagaInstanceRepository) Add(instance *SagaInstance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances[instance.ID] = instance
}

func (r *MockSagaInstanceRepository) SetUpdateError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateErr = err
}

// TestStepExecutor_FirstExecution verifies first execution calls handler and persists result.
func TestStepExecutor_FirstExecution(t *testing.T) {
	stepResultRepo := NewMockStepResultRepository()
	instanceRepo := NewMockSagaInstanceRepository()

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	instanceRepo.Add(instance)

	handler := &MockStepHandler{
		ReturnValue: map[string]interface{}{"result": "success", "amount": 100},
	}

	executor := NewStepExecutor(stepResultRepo, instanceRepo)

	ctx := context.Background()
	result, err := executor.ExecuteStep(ctx, instance, 0, "step_1", handler.Execute, nil)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 1, handler.CallCount, "Handler should be called exactly once")

	// Verify result was persisted
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	persistedResult := stepResultRepo.GetResult(idempotencyKey)
	require.NotNil(t, persistedResult, "Step result should be persisted")
	assert.Equal(t, StepStatusCompleted, persistedResult.Status)
}

// TestStepExecutor_CacheHitSkipsExecution verifies replay returns cached result without re-executing.
func TestStepExecutor_CacheHitSkipsExecution(t *testing.T) {
	stepResultRepo := NewMockStepResultRepository()
	instanceRepo := NewMockSagaInstanceRepository()

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	instanceRepo.Add(instance)

	// Pre-populate cache with a previous result
	cachedOutput := JSONB{"cached_result": "from_previous_run"}
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	existingResult := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0,
		IdempotencyKey: idempotencyKey,
		Status:         StepStatusCompleted,
		Result:         cachedOutput,
	}
	_ = stepResultRepo.Save(context.Background(), existingResult)

	handler := &MockStepHandler{
		ReturnValue: map[string]interface{}{"new_result": "should_not_see_this"},
	}

	executor := NewStepExecutor(stepResultRepo, instanceRepo)

	ctx := context.Background()
	result, err := executor.ExecuteStep(ctx, instance, 0, "step_1", handler.Execute, nil)

	require.NoError(t, err)
	assert.Equal(t, 0, handler.CallCount, "Handler should NOT be called on cache hit")

	// Verify the cached result is returned
	resultMap, ok := result.(map[string]interface{})
	require.True(t, ok, "Result should be a map")
	assert.Equal(t, "from_previous_run", resultMap["cached_result"])
}

// TestStepExecutor_HandlerErrorDoesNotPersist verifies that handler errors don't persist completed status.
func TestStepExecutor_HandlerErrorPersistsFailure(t *testing.T) {
	stepResultRepo := NewMockStepResultRepository()
	instanceRepo := NewMockSagaInstanceRepository()

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	instanceRepo.Add(instance)

	handler := &MockStepHandler{
		ReturnError: errStepExecutionFailed,
	}

	executor := NewStepExecutor(stepResultRepo, instanceRepo)

	ctx := context.Background()
	_, err := executor.ExecuteStep(ctx, instance, 0, "failing_step", handler.Execute, nil)

	require.Error(t, err)
	assert.Equal(t, 1, handler.CallCount)

	// Verify failed result was persisted
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	persistedResult := stepResultRepo.GetResult(idempotencyKey)
	require.NotNil(t, persistedResult, "Failed step result should be persisted")
	assert.Equal(t, StepStatusFailed, persistedResult.Status)
	require.NotNil(t, persistedResult.Error)
	assert.Contains(t, *persistedResult.Error, "step execution failed")
}

// TestStepExecutor_CausationIDIsDeterministic verifies the persisted causation_id is deterministic.
func TestStepExecutor_CausationIDIsDeterministic(t *testing.T) {
	stepResultRepo := NewMockStepResultRepository()
	instanceRepo := NewMockSagaInstanceRepository()

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	instanceRepo.Add(instance)

	handler := &MockStepHandler{
		ReturnValue: "test",
	}

	executor := NewStepExecutor(stepResultRepo, instanceRepo)

	ctx := context.Background()
	_, err := executor.ExecuteStep(ctx, instance, 5, "step_5", handler.Execute, nil)
	require.NoError(t, err)

	idempotencyKey := FormatIdempotencyKey(instanceID, 5)
	persistedResult := stepResultRepo.GetResult(idempotencyKey)
	require.NotNil(t, persistedResult)
	require.NotNil(t, persistedResult.CausationID)

	// Verify the causation ID matches the expected deterministic value
	expectedCausationID := GenerateCausationID(instanceID, 5)
	assert.Equal(t, expectedCausationID, *persistedResult.CausationID)
}

// TestStepExecutor_DeepCopyPreventsPointerLeaks verifies output is deep-copied before persistence.
func TestStepExecutor_DeepCopyPreventsPointerLeaks(t *testing.T) {
	stepResultRepo := NewMockStepResultRepository()
	instanceRepo := NewMockSagaInstanceRepository()

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	instanceRepo.Add(instance)

	// Return a mutable result
	mutableResult := map[string]interface{}{
		"balance": 1000,
	}
	handler := &MockStepHandler{
		ReturnValue: mutableResult,
	}

	executor := NewStepExecutor(stepResultRepo, instanceRepo)

	ctx := context.Background()
	_, err := executor.ExecuteStep(ctx, instance, 0, "step_0", handler.Execute, nil)
	require.NoError(t, err)

	// Modify the original mutable result after execution
	mutableResult["balance"] = 9999

	// Verify the persisted result is unaffected
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	persistedResult := stepResultRepo.GetResult(idempotencyKey)
	require.NotNil(t, persistedResult)

	persistedData := map[string]interface{}(persistedResult.Result)
	// The balance should still be 1000, not 9999
	balance, ok := persistedData["balance"].(float64) // JSON unmarshals numbers as float64
	require.True(t, ok)
	assert.Equal(t, float64(1000), balance, "Persisted result should be independent of original (deep-copied)")
}

// TestStepExecutor_ReplayIdempotency verifies that replaying a saga multiple times is idempotent.
func TestStepExecutor_ReplayIdempotency(t *testing.T) {
	stepResultRepo := NewMockStepResultRepository()
	instanceRepo := NewMockSagaInstanceRepository()

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	instanceRepo.Add(instance)

	executionCount := 0
	handler := &MockStepHandler{
		ReturnValue: map[string]interface{}{"exec_count": 1},
		OnCall: func(_ context.Context, _ map[string]interface{}) {
			executionCount++
		},
	}

	executor := NewStepExecutor(stepResultRepo, instanceRepo)
	ctx := context.Background()

	// Execute step 3 times (simulating replay)
	for i := 0; i < 3; i++ {
		result, err := executor.ExecuteStep(ctx, instance, 0, "step_0", handler.Execute, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
	}

	// Handler should only be called ONCE
	assert.Equal(t, 1, executionCount, "Handler should only execute once across multiple replays")
}

// TestStepExecutor_ReplayProducesSameCausationID verifies determinism test.
func TestStepExecutor_ReplayProducesSameCausationID(t *testing.T) {
	// First execution
	instanceID := uuid.New()
	stepIndex := 7

	causationID1 := GenerateCausationID(instanceID, stepIndex)

	// Simulate saga crash and restart...

	// Second execution (replay)
	causationID2 := GenerateCausationID(instanceID, stepIndex)

	assert.Equal(t, causationID1, causationID2, "Causation IDs must be identical across replays")
}

// --- Integration-style tests using mock transaction support ---

// MockTransactionalRepository simulates transaction affinity behavior.
type MockTransactionalRepository struct {
	stepResultRepo *MockStepResultRepository
	instanceRepo   *MockSagaInstanceRepository
	commitErr      error
	committed      bool
	rolledBack     bool
}

func NewMockTransactionalRepository() *MockTransactionalRepository {
	return &MockTransactionalRepository{
		stepResultRepo: NewMockStepResultRepository(),
		instanceRepo:   NewMockSagaInstanceRepository(),
	}
}

func (r *MockTransactionalRepository) SetCommitError(err error) {
	r.commitErr = err
}

func (r *MockTransactionalRepository) BeginTx(_ context.Context) (TxContext, error) {
	return &mockTxContext{
		repo:          r,
		stepResultTx:  make(map[string]*SagaStepResult),
		stepIndexTx:   make(map[uuid.UUID]int),
		stepResultErr: r.stepResultRepo.saveErr,
	}, nil
}

type mockTxContext struct {
	repo          *MockTransactionalRepository
	stepResultTx  map[string]*SagaStepResult
	stepIndexTx   map[uuid.UUID]int
	stepResultErr error
}

func (tx *mockTxContext) SaveStepResult(_ context.Context, result *SagaStepResult) error {
	if tx.stepResultErr != nil {
		return tx.stepResultErr
	}
	tx.stepResultTx[result.IdempotencyKey] = result
	return nil
}

func (tx *mockTxContext) UpdateStepIndex(_ context.Context, instanceID uuid.UUID, stepIndex int) error {
	tx.stepIndexTx[instanceID] = stepIndex
	return nil
}

func (tx *mockTxContext) Commit() error {
	if tx.repo.commitErr != nil {
		return tx.repo.commitErr
	}
	// Apply changes to main repository
	for _, v := range tx.stepResultTx {
		_ = tx.repo.stepResultRepo.Save(context.Background(), v)
	}
	for id, idx := range tx.stepIndexTx {
		_ = tx.repo.instanceRepo.UpdateStepIndex(context.Background(), id, idx)
	}
	tx.repo.committed = true
	return nil
}

func (tx *mockTxContext) Rollback() error {
	tx.repo.rolledBack = true
	return nil
}

// TestTransactionAffinity_CommitFailureRollsBack verifies no partial state on commit failure.
func TestTransactionAffinity_CommitFailureRollsBack(t *testing.T) {
	txRepo := NewMockTransactionalRepository()

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	txRepo.instanceRepo.Add(instance)

	handler := &MockStepHandler{
		ReturnValue: "success",
	}

	// Simulate commit failure
	txRepo.SetCommitError(errSimulatedCommitFailed)

	executor := NewTransactionalStepExecutor(txRepo)

	ctx := context.Background()
	_, err := executor.ExecuteStepInTx(ctx, instance, 0, "step_0", handler.Execute, nil)

	// Should get an error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit")

	// Verify no partial state persisted
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	result := txRepo.stepResultRepo.GetResult(idempotencyKey)
	assert.Nil(t, result, "No step result should be persisted on commit failure")
}

// TestTransactionAffinity_AtomicStepResultAndIndexUpdate verifies both operations happen atomically.
func TestTransactionAffinity_AtomicStepResultAndIndexUpdate(t *testing.T) {
	txRepo := NewMockTransactionalRepository()

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	txRepo.instanceRepo.Add(instance)

	handler := &MockStepHandler{
		ReturnValue: map[string]interface{}{"status": "done"},
	}

	executor := NewTransactionalStepExecutor(txRepo)

	ctx := context.Background()
	_, err := executor.ExecuteStepInTx(ctx, instance, 0, "step_0", handler.Execute, nil)
	require.NoError(t, err)

	// Verify both step result AND step index were updated
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	result := txRepo.stepResultRepo.GetResult(idempotencyKey)
	require.NotNil(t, result, "Step result should be persisted")

	savedInstance, _ := txRepo.instanceRepo.FindByID(ctx, instanceID)
	require.NotNil(t, savedInstance)
	assert.Equal(t, 1, savedInstance.CurrentStepIndex, "Step index should be incremented to 1")
}

// --- Serialization helpers test ---

func TestJSONBSerialization(t *testing.T) {
	original := JSONB{
		"key":    "value",
		"number": float64(42), // JSON numbers are float64
		"nested": map[string]interface{}{
			"inner": "data",
		},
	}

	// Serialize
	data, err := json.Marshal(original)
	require.NoError(t, err)

	// Deserialize
	var restored JSONB
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "value", restored["key"])
	assert.Equal(t, float64(42), restored["number"])
}
