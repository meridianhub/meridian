package saga

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Integration test sentinel errors.
var errInsufficientFunds = errors.New("insufficient funds")

// GormStepResultRepository implements StepResultRepository using GORM.
type GormStepResultRepository struct {
	db *gorm.DB
}

func NewGormStepResultRepository(db *gorm.DB) *GormStepResultRepository {
	return &GormStepResultRepository{db: db}
}

func (r *GormStepResultRepository) FindByIdempotencyKey(ctx context.Context, key string) (*SagaStepResult, error) {
	var result SagaStepResult
	err := r.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *GormStepResultRepository) Save(ctx context.Context, result *SagaStepResult) error {
	return r.db.WithContext(ctx).Create(result).Error
}

// GormSagaInstanceRepository implements SagaInstanceRepository using GORM.
type GormSagaInstanceRepository struct {
	db *gorm.DB
}

func NewGormSagaInstanceRepository(db *gorm.DB) *GormSagaInstanceRepository {
	return &GormSagaInstanceRepository{db: db}
}

func (r *GormSagaInstanceRepository) FindByID(ctx context.Context, id uuid.UUID) (*SagaInstance, error) {
	var instance SagaInstance
	err := r.db.WithContext(ctx).First(&instance, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

func (r *GormSagaInstanceRepository) UpdateStepIndex(ctx context.Context, id uuid.UUID, stepIndex int) error {
	return r.db.WithContext(ctx).Model(&SagaInstance{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"current_step_index": stepIndex,
			"replay_count":       0,
		}).Error
}

func (r *GormSagaInstanceRepository) Create(ctx context.Context, instance *SagaInstance) error {
	return r.db.WithContext(ctx).Create(instance).Error
}

// GormTransactionalRepository implements TransactionalRepository using GORM.
type GormTransactionalRepository struct {
	db *gorm.DB
}

func NewGormTransactionalRepository(db *gorm.DB) *GormTransactionalRepository {
	return &GormTransactionalRepository{db: db}
}

func (r *GormTransactionalRepository) BeginTx(ctx context.Context) (TxContext, error) {
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	return &gormTxContext{tx: tx}, nil
}

type gormTxContext struct {
	tx *gorm.DB
}

func (t *gormTxContext) SaveStepResult(ctx context.Context, result *SagaStepResult) error {
	return t.tx.WithContext(ctx).Create(result).Error
}

func (t *gormTxContext) UpdateStepIndex(ctx context.Context, instanceID uuid.UUID, stepIndex int) error {
	return t.tx.WithContext(ctx).Model(&SagaInstance{}).
		Where("id = ?", instanceID).
		Updates(map[string]interface{}{
			"current_step_index": stepIndex,
			"replay_count":       0,
		}).Error
}

func (t *gormTxContext) Commit() error {
	return t.tx.Commit().Error
}

func (t *gormTxContext) Rollback() error {
	return t.tx.Rollback().Error
}

// --- Integration Tests ---

// TestIntegration_StepExecution_FirstExecution tests first execution with a real database.
func TestIntegration_StepExecution_FirstExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	stepResultRepo := NewGormStepResultRepository(db)
	instanceRepo := NewGormSagaInstanceRepository(db)

	// Create saga instance
	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = instanceRepo.Create(context.Background(), instance)
	require.NoError(t, err)

	// Create executor and handler
	executor := NewStepExecutor(stepResultRepo, instanceRepo)
	handler := func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{
			"account_id": "ACC-12345",
			"balance":    float64(1000),
		}, nil
	}

	// Execute step
	ctx := context.Background()
	result, err := executor.ExecuteStep(ctx, instance, 0, "init_account", handler, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify step result was persisted
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	persistedResult, err := stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	require.NoError(t, err)
	require.NotNil(t, persistedResult)
	assert.Equal(t, StepStatusCompleted, persistedResult.Status)
	assert.Equal(t, "init_account", persistedResult.StepName)

	// Verify causation ID was set
	require.NotNil(t, persistedResult.CausationID)
	expectedCausationID := GenerateCausationID(instanceID, 0)
	assert.Equal(t, expectedCausationID, *persistedResult.CausationID)

	// Verify result data
	assert.Equal(t, "ACC-12345", persistedResult.Result["account_id"])

	// Verify saga instance step index was updated
	updatedInstance, err := instanceRepo.FindByID(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, 1, updatedInstance.CurrentStepIndex)
}

// TestIntegration_StepExecution_Replay tests that replay returns cached result.
func TestIntegration_StepExecution_Replay(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	stepResultRepo := NewGormStepResultRepository(db)
	instanceRepo := NewGormSagaInstanceRepository(db)

	// Create saga instance
	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = instanceRepo.Create(context.Background(), instance)
	require.NoError(t, err)

	executor := NewStepExecutor(stepResultRepo, instanceRepo)

	executionCount := int32(0)
	handler := func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		atomic.AddInt32(&executionCount, 1)
		return map[string]interface{}{"exec_id": uuid.New().String()}, nil
	}

	ctx := context.Background()

	// First execution
	result1, err := executor.ExecuteStep(ctx, instance, 0, "step_0", handler, nil)
	require.NoError(t, err)
	require.NotNil(t, result1)

	// Simulate saga restart (replay)
	result2, err := executor.ExecuteStep(ctx, instance, 0, "step_0", handler, nil)
	require.NoError(t, err)
	require.NotNil(t, result2)

	// Third replay
	result3, err := executor.ExecuteStep(ctx, instance, 0, "step_0", handler, nil)
	require.NoError(t, err)
	require.NotNil(t, result3)

	// Handler should only be called ONCE
	assert.Equal(t, int32(1), atomic.LoadInt32(&executionCount), "Handler should only execute once")

	// All results should be identical
	result1Map := result1.(map[string]interface{})
	result2Map := result2.(map[string]interface{})
	result3Map := result3.(map[string]interface{})
	assert.Equal(t, result1Map["exec_id"], result2Map["exec_id"], "Replay should return same result")
	assert.Equal(t, result1Map["exec_id"], result3Map["exec_id"], "Replay should return same result")
}

// TestIntegration_StepExecution_DeterministicCausationID tests replay produces same causation ID.
func TestIntegration_StepExecution_DeterministicCausationID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	stepResultRepo := NewGormStepResultRepository(db)
	instanceRepo := NewGormSagaInstanceRepository(db)

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = instanceRepo.Create(context.Background(), instance)
	require.NoError(t, err)

	executor := NewStepExecutor(stepResultRepo, instanceRepo)
	handler := func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		return "done", nil
	}

	ctx := context.Background()

	// Execute step
	_, err = executor.ExecuteStep(ctx, instance, 5, "step_5", handler, nil)
	require.NoError(t, err)

	// Retrieve persisted result
	idempotencyKey := FormatIdempotencyKey(instanceID, 5)
	result, err := stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	require.NoError(t, err)
	require.NotNil(t, result.CausationID)

	// Verify causation ID matches expected deterministic value
	expectedCausationID := GenerateCausationID(instanceID, 5)
	assert.Equal(t, expectedCausationID, *result.CausationID, "Causation ID should be deterministic")

	// Verify it's UUIDv5
	version := (*result.CausationID)[6] >> 4
	assert.Equal(t, uint8(5), version, "Should be UUIDv5")
}

// TestIntegration_TransactionalExecution_AtomicCommit tests atomic transaction commit.
func TestIntegration_TransactionalExecution_AtomicCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	stepResultRepo := NewGormStepResultRepository(db)
	instanceRepo := NewGormSagaInstanceRepository(db)
	txRepo := NewGormTransactionalRepository(db)

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = instanceRepo.Create(context.Background(), instance)
	require.NoError(t, err)

	executor := NewTransactionalStepExecutor(txRepo).WithStepResultRepo(stepResultRepo)
	handler := func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"status": "success"}, nil
	}

	ctx := context.Background()
	_, err = executor.ExecuteStepInTx(ctx, instance, 0, "step_0", handler, nil)
	require.NoError(t, err)

	// Verify both step result AND step index were atomically updated
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	result, err := stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	require.NoError(t, err)
	require.NotNil(t, result, "Step result should be persisted")
	assert.Equal(t, StepStatusCompleted, result.Status)

	updatedInstance, err := instanceRepo.FindByID(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, 1, updatedInstance.CurrentStepIndex, "Step index should be incremented")
}

// TestIntegration_ConcurrentReplay tests concurrent replay attempts return same result.
func TestIntegration_ConcurrentReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	stepResultRepo := NewGormStepResultRepository(db)
	instanceRepo := NewGormSagaInstanceRepository(db)

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = instanceRepo.Create(context.Background(), instance)
	require.NoError(t, err)

	executor := NewStepExecutor(stepResultRepo, instanceRepo)

	executionCount := int32(0)
	handler := func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		atomic.AddInt32(&executionCount, 1)
		//nolint:forbidigo // simulates step handler work latency to test concurrent idempotency enforcement
		time.Sleep(10 * time.Millisecond)
		return map[string]interface{}{"result": "done"}, nil
	}

	ctx := context.Background()
	numGoroutines := 10
	var wg sync.WaitGroup
	results := make([]interface{}, numGoroutines)
	errs := make([]error, numGoroutines)

	// First, do one successful execution
	_, err = executor.ExecuteStep(ctx, instance, 0, "step_0", handler, nil)
	require.NoError(t, err)

	// Reset execution count
	atomic.StoreInt32(&executionCount, 0)

	// Now simulate concurrent replays
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = executor.ExecuteStep(ctx, instance, 0, "step_0", handler, nil)
		}(i)
	}

	wg.Wait()

	// All replays should succeed without errors
	for i, err := range errs {
		assert.NoError(t, err, "Replay %d should not error", i)
	}

	// Handler should NOT be called during replays (was called once initially)
	assert.Equal(t, int32(0), atomic.LoadInt32(&executionCount),
		"Handler should not be called during concurrent replays")
}

// TestIntegration_StepFailurePersistence tests that failed steps are persisted and replayed correctly.
func TestIntegration_StepFailurePersistence(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	stepResultRepo := NewGormStepResultRepository(db)
	instanceRepo := NewGormSagaInstanceRepository(db)

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = instanceRepo.Create(context.Background(), instance)
	require.NoError(t, err)

	executor := NewStepExecutor(stepResultRepo, instanceRepo)

	executionCount := int32(0)
	handler := func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		atomic.AddInt32(&executionCount, 1)
		return nil, errInsufficientFunds
	}

	ctx := context.Background()

	// First execution fails
	_, err = executor.ExecuteStep(ctx, instance, 0, "transfer_funds", handler, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient funds")

	// Verify failure was persisted
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	result, err := stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, StepStatusFailed, result.Status)
	require.NotNil(t, result.Error)
	assert.Contains(t, *result.Error, "insufficient funds")

	// Verify step index was NOT incremented (failure case)
	updatedInstance, err := instanceRepo.FindByID(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, 0, updatedInstance.CurrentStepIndex, "Step index should not increment on failure")

	// Replay should return cached failure
	_, err = executor.ExecuteStep(ctx, instance, 0, "transfer_funds", handler, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step previously failed")

	// Handler should only be called ONCE (the original failure)
	assert.Equal(t, int32(1), atomic.LoadInt32(&executionCount))
}

// TestIntegration_DeepCopyImmutability tests that persisted data is independent of original.
func TestIntegration_DeepCopyImmutability(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	stepResultRepo := NewGormStepResultRepository(db)
	instanceRepo := NewGormSagaInstanceRepository(db)

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = instanceRepo.Create(context.Background(), instance)
	require.NoError(t, err)

	executor := NewStepExecutor(stepResultRepo, instanceRepo)

	// Return mutable data
	mutableResult := map[string]interface{}{
		"balance": float64(1000),
		"nested": map[string]interface{}{
			"key": "original",
		},
	}
	handler := func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		return mutableResult, nil
	}

	ctx := context.Background()

	// Execute step
	_, err = executor.ExecuteStep(ctx, instance, 0, "step_0", handler, nil)
	require.NoError(t, err)

	// Modify the original data AFTER execution
	mutableResult["balance"] = float64(9999)
	nested := mutableResult["nested"].(map[string]interface{})
	nested["key"] = "modified"

	// Retrieve from database
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	result, err := stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify persisted data is unchanged
	assert.Equal(t, float64(1000), result.Result["balance"], "Persisted balance should be unchanged")
	nestedPersisted := result.Result["nested"].(map[string]interface{})
	assert.Equal(t, "original", nestedPersisted["key"], "Persisted nested data should be unchanged")
}

// TestIntegration_MultiStepSaga tests executing multiple steps in sequence.
func TestIntegration_MultiStepSaga(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	stepResultRepo := NewGormStepResultRepository(db)
	instanceRepo := NewGormSagaInstanceRepository(db)

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = instanceRepo.Create(context.Background(), instance)
	require.NoError(t, err)

	executor := NewStepExecutor(stepResultRepo, instanceRepo)
	ctx := context.Background()

	// Step 0: Create account
	_, err = executor.ExecuteStep(ctx, instance, 0, "create_account", func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"account_id": "ACC-001"}, nil
	}, nil)
	require.NoError(t, err)

	// Step 1: Initialize balance
	_, err = executor.ExecuteStep(ctx, instance, 1, "init_balance", func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"balance": float64(0)}, nil
	}, nil)
	require.NoError(t, err)

	// Step 2: First deposit
	_, err = executor.ExecuteStep(ctx, instance, 2, "deposit", func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"balance": float64(500)}, nil
	}, nil)
	require.NoError(t, err)

	// Verify all steps were persisted
	for i := 0; i < 3; i++ {
		key := FormatIdempotencyKey(instanceID, i)
		result, err := stepResultRepo.FindByIdempotencyKey(ctx, key)
		require.NoError(t, err, "Step %d result should exist", i)
		require.NotNil(t, result, "Step %d result should not be nil", i)
		assert.Equal(t, StepStatusCompleted, result.Status, "Step %d should be completed", i)
	}

	// Verify step index was incremented correctly
	updatedInstance, err := instanceRepo.FindByID(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, 3, updatedInstance.CurrentStepIndex, "Should be at step 3 after 3 successful steps")
}

// TestIntegration_SagaInterruption tests resuming a saga after interruption.
func TestIntegration_SagaInterruption(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	stepResultRepo := NewGormStepResultRepository(db)
	instanceRepo := NewGormSagaInstanceRepository(db)

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = instanceRepo.Create(context.Background(), instance)
	require.NoError(t, err)

	executor := NewStepExecutor(stepResultRepo, instanceRepo)
	ctx := context.Background()

	step0Count := int32(0)
	step1Count := int32(0)
	step2Count := int32(0)

	// Execute steps 0 and 1
	_, err = executor.ExecuteStep(ctx, instance, 0, "step_0", func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		atomic.AddInt32(&step0Count, 1)
		return map[string]interface{}{"step": 0}, nil
	}, nil)
	require.NoError(t, err)

	_, err = executor.ExecuteStep(ctx, instance, 1, "step_1", func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		atomic.AddInt32(&step1Count, 1)
		return map[string]interface{}{"step": 1}, nil
	}, nil)
	require.NoError(t, err)

	// Simulate saga crash/restart - replay from beginning
	// Steps 0 and 1 should be cached, step 2 should execute fresh

	// Replay step 0
	_, err = executor.ExecuteStep(ctx, instance, 0, "step_0", func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		atomic.AddInt32(&step0Count, 1) // Should NOT be called
		return map[string]interface{}{"step": 0}, nil
	}, nil)
	require.NoError(t, err)

	// Replay step 1
	_, err = executor.ExecuteStep(ctx, instance, 1, "step_1", func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		atomic.AddInt32(&step1Count, 1) // Should NOT be called
		return map[string]interface{}{"step": 1}, nil
	}, nil)
	require.NoError(t, err)

	// Execute new step 2
	_, err = executor.ExecuteStep(ctx, instance, 2, "step_2", func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		atomic.AddInt32(&step2Count, 1)
		return map[string]interface{}{"step": 2}, nil
	}, nil)
	require.NoError(t, err)

	// Verify execution counts
	assert.Equal(t, int32(1), atomic.LoadInt32(&step0Count), "Step 0 should execute once")
	assert.Equal(t, int32(1), atomic.LoadInt32(&step1Count), "Step 1 should execute once")
	assert.Equal(t, int32(1), atomic.LoadInt32(&step2Count), "Step 2 should execute once")
}

// TestIntegration_IdempotencyKeyUniqueness tests that duplicate idempotency keys are rejected.
func TestIntegration_IdempotencyKeyUniqueness(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Try to insert duplicate idempotency key directly
	instanceID := uuid.New()

	// Create parent saga instance first
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	idempotencyKey := FormatIdempotencyKey(instanceID, 0)

	result1 := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0,
		IdempotencyKey: idempotencyKey,
		Status:         StepStatusCompleted,
	}
	err = db.Create(result1).Error
	require.NoError(t, err)

	// Try to insert duplicate
	result2 := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0, // Same step
		IdempotencyKey: idempotencyKey,
		Status:         StepStatusCompleted,
	}
	err = db.Create(result2).Error
	assert.Error(t, err, "Should reject duplicate idempotency key")
}

// TestIntegration_WaitForCondition demonstrates using await package instead of time.Sleep.
func TestIntegration_WaitForCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	stepResultRepo := NewGormStepResultRepository(db)
	instanceRepo := NewGormSagaInstanceRepository(db)

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = instanceRepo.Create(context.Background(), instance)
	require.NoError(t, err)

	executor := NewStepExecutor(stepResultRepo, instanceRepo)
	ctx := context.Background()

	// Start async execution
	go func() {
		_, _ = executor.ExecuteStep(ctx, instance, 0, "async_step", func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
			return map[string]interface{}{"async": true}, nil
		}, nil)
	}()

	// Use await instead of time.Sleep to wait for step completion
	idempotencyKey := FormatIdempotencyKey(instanceID, 0)
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			result, findErr := stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
			return findErr == nil && result != nil && result.Status == StepStatusCompleted
		})
	require.NoError(t, err, "Step should complete within timeout")

	// Verify result
	result, err := stepResultRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, StepStatusCompleted, result.Status)
}
