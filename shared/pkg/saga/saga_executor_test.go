package saga

// Tests for saga executor require dynamic errors to simulate different failure scenarios.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDetermineFailureAction verifies the stateless decision logic.
func TestDetermineFailureAction(t *testing.T) {
	t.Run("FATAL error returns COMPENSATE", func(t *testing.T) {
		action := DetermineFailureAction(ErrorCategoryFatal, 0, 5)
		assert.Equal(t, FailureActionCompensate, action)
	})

	t.Run("FATAL error with high replay count still returns COMPENSATE", func(t *testing.T) {
		// FATAL errors should compensate regardless of replay count
		action := DetermineFailureAction(ErrorCategoryFatal, 10, 5)
		assert.Equal(t, FailureActionCompensate, action)
	})

	t.Run("TRANSIENT error with replays remaining returns RETRY", func(t *testing.T) {
		action := DetermineFailureAction(ErrorCategoryTransient, 2, 5)
		assert.Equal(t, FailureActionRetry, action)
	})

	t.Run("TRANSIENT error at max replays returns MANUAL_INTERVENTION", func(t *testing.T) {
		action := DetermineFailureAction(ErrorCategoryTransient, 5, 5)
		assert.Equal(t, FailureActionManualIntervention, action)
	})

	t.Run("TRANSIENT error exceeding max replays returns MANUAL_INTERVENTION", func(t *testing.T) {
		action := DetermineFailureAction(ErrorCategoryTransient, 10, 5)
		assert.Equal(t, FailureActionManualIntervention, action)
	})

	t.Run("unknown category returns COMPENSATE (fail-safe)", func(t *testing.T) {
		action := DetermineFailureAction(ErrorCategory("UNKNOWN"), 0, 5)
		assert.Equal(t, FailureActionCompensate, action)
	})

	t.Run("empty category returns COMPENSATE (fail-safe)", func(t *testing.T) {
		action := DetermineFailureAction(ErrorCategory(""), 0, 5)
		assert.Equal(t, FailureActionCompensate, action)
	})
}

// TestShouldRetry verifies the retry decision helper.
func TestShouldRetry(t *testing.T) {
	assert.True(t, ShouldRetry(ErrorCategoryTransient))
	assert.False(t, ShouldRetry(ErrorCategoryFatal))
	assert.False(t, ShouldRetry(ErrorCategory("")))
}

// TestShouldCompensate verifies the compensation decision helper.
func TestShouldCompensate(t *testing.T) {
	assert.True(t, ShouldCompensate(ErrorCategoryFatal))
	assert.False(t, ShouldCompensate(ErrorCategoryTransient))
	assert.False(t, ShouldCompensate(ErrorCategory("")))
}

// MockSagaInstanceRepository is a mock implementation for testing.
type MockSagaInstanceRepositoryForExecutor struct {
	instances       map[uuid.UUID]*SagaInstance
	updateStatusErr error
	lastStatus      SagaStatus
	lastStepIndex   int
	lastError       error
	lastCategory    ErrorCategory
}

func NewMockSagaInstanceRepositoryForExecutor() *MockSagaInstanceRepositoryForExecutor {
	return &MockSagaInstanceRepositoryForExecutor{
		instances: make(map[uuid.UUID]*SagaInstance),
	}
}

func (r *MockSagaInstanceRepositoryForExecutor) FindByID(_ context.Context, id uuid.UUID) (*SagaInstance, error) {
	instance, exists := r.instances[id]
	if !exists {
		return nil, nil
	}
	return instance, nil
}

func (r *MockSagaInstanceRepositoryForExecutor) Update(_ context.Context, instance *SagaInstance) error {
	r.instances[instance.ID] = instance
	return nil
}

func (r *MockSagaInstanceRepositoryForExecutor) UpdateStatus(_ context.Context, id uuid.UUID, status SagaStatus) error {
	if r.updateStatusErr != nil {
		return r.updateStatusErr
	}
	r.lastStatus = status
	if instance, exists := r.instances[id]; exists {
		instance.Status = status
	}
	return nil
}

func (r *MockSagaInstanceRepositoryForExecutor) UpdateStatusWithError(_ context.Context, id uuid.UUID, status SagaStatus, stepIndex int, err error, category ErrorCategory) error {
	if r.updateStatusErr != nil {
		return r.updateStatusErr
	}
	r.lastStatus = status
	r.lastStepIndex = stepIndex
	r.lastError = err
	r.lastCategory = category
	if instance, exists := r.instances[id]; exists {
		instance.Status = status
		instance.FailedStepIndex = &stepIndex
		if err != nil {
			errMsg := err.Error()
			instance.ErrorMessage = &errMsg
		}
		catStr := string(category)
		instance.ErrorCategory = &catStr
	}
	return nil
}

func (r *MockSagaInstanceRepositoryForExecutor) ResetReplayCount(_ context.Context, id uuid.UUID) error {
	if instance, exists := r.instances[id]; exists {
		instance.ReplayCount = 0
	}
	return nil
}

func (r *MockSagaInstanceRepositoryForExecutor) UpdateNextRetryAt(_ context.Context, id uuid.UUID, nextRetryAt time.Time) error {
	if instance, exists := r.instances[id]; exists {
		t := nextRetryAt
		instance.NextRetryAt = &t
	}
	return nil
}

func (r *MockSagaInstanceRepositoryForExecutor) ResetReplayCountAndBackoff(_ context.Context, id uuid.UUID) error {
	if instance, exists := r.instances[id]; exists {
		instance.ReplayCount = 0
		instance.NextRetryAt = nil
	}
	return nil
}

func (r *MockSagaInstanceRepositoryForExecutor) Add(instance *SagaInstance) {
	r.instances[instance.ID] = instance
}

// TestSagaExecutor_HandleStepFailure_FatalError tests FATAL error handling.
func TestSagaExecutor_HandleStepFailure_FatalError(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      0,
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	// Use a FATAL error (insufficient funds)
	result, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		2,
		"create_lien",
		ErrInsufficientFunds,
		5,
	)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify classification and action
	assert.Equal(t, ErrorCategoryFatal, result.ErrorCategory)
	assert.Equal(t, FailureActionCompensate, result.Action)
	assert.Equal(t, 2, result.StepIndex)
	assert.Equal(t, "create_lien", result.StepName)

	// Verify saga was transitioned to COMPENSATING
	assert.Equal(t, SagaStatusCompensating, instanceRepo.lastStatus)
	assert.Equal(t, 2, instanceRepo.lastStepIndex)
	assert.Equal(t, ErrorCategoryFatal, instanceRepo.lastCategory)

	// Verify saga instance was updated
	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	assert.Equal(t, SagaStatusCompensating, updated.Status)
}

// TestSagaExecutor_HandleStepFailure_TransientError tests TRANSIENT error handling.
func TestSagaExecutor_HandleStepFailure_TransientError(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      2, // Has some replays but not at max
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	// Use a TRANSIENT error (network timeout)
	transientErr := errors.New("connection timeout")
	result, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		1,
		"post_entries",
		transientErr,
		5, // max replays
	)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify classification and action
	assert.Equal(t, ErrorCategoryTransient, result.ErrorCategory)
	assert.Equal(t, FailureActionRetry, result.Action)
	assert.Equal(t, 2, result.ReplayCount) // Current count before any increment

	// Saga should NOT be transitioned to COMPENSATING
	// It stays in RUNNING and waits for claiming service to retry
	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	assert.Equal(t, SagaStatusRunning, updated.Status)
}

// TestSagaExecutor_HandleStepFailure_TransientExceedsMaxReplays tests zombie detection.
func TestSagaExecutor_HandleStepFailure_TransientExceedsMaxReplays(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      5, // At max replays
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	// Use a TRANSIENT error when already at max replays
	transientErr := errors.New("database deadlock")
	result, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		1,
		"post_entries",
		transientErr,
		5, // max replays
	)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Even though it's TRANSIENT, max replays exceeded -> MANUAL_INTERVENTION
	assert.Equal(t, ErrorCategoryTransient, result.ErrorCategory)
	assert.Equal(t, FailureActionManualIntervention, result.Action)

	// Verify saga was transitioned to FAILED_MANUAL_INTERVENTION
	assert.Equal(t, SagaStatusFailedManualIntervention, instanceRepo.lastStatus)

	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	assert.Equal(t, SagaStatusFailedManualIntervention, updated.Status)
}

// TestSagaExecutor_HandleStepFailure_FatalDoesNotCheckReplayCount tests that FATAL errors
// bypass replay count checks and go directly to COMPENSATING.
func TestSagaExecutor_HandleStepFailure_FatalDoesNotCheckReplayCount(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      0, // Low replay count, but FATAL should still compensate
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	// FATAL error should go to COMPENSATING regardless of replay count
	result, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		0,
		"validate_input",
		ErrValidationFailed,
		5,
	)

	require.NoError(t, err)
	require.NotNil(t, result)

	// FATAL error -> COMPENSATE (not RETRY, not MANUAL_INTERVENTION)
	assert.Equal(t, ErrorCategoryFatal, result.ErrorCategory)
	assert.Equal(t, FailureActionCompensate, result.Action)
	assert.Equal(t, SagaStatusCompensating, instanceRepo.lastStatus)
}

// TestSagaExecutor_HandleStepFailure_WrappedFatalError tests that wrapped FATAL errors are detected.
func TestSagaExecutor_HandleStepFailure_WrappedFatalError(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      0,
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	// Wrapped FATAL error should be detected via errors.Is
	wrappedErr := NewFatalError(errors.New("business logic failed"))
	result, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		1,
		"process_order",
		wrappedErr,
		5,
	)

	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, ErrorCategoryFatal, result.ErrorCategory)
	assert.Equal(t, FailureActionCompensate, result.Action)
}

// TestSagaExecutor_HandleStepFailure_NilError tests that nil error is rejected.
func TestSagaExecutor_HandleStepFailure_NilError(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	result, err := executor.HandleStepFailure(
		context.Background(),
		uuid.New(),
		0,
		"step",
		nil, // nil error
		5,
	)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNilError)
	assert.Nil(t, result)
}

// TestSagaExecutor_HandleStepFailure_SagaNotFound tests handling of missing saga.
func TestSagaExecutor_HandleStepFailure_SagaNotFound(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	// Don't add any saga to the repo
	result, err := executor.HandleStepFailure(
		context.Background(),
		uuid.New(), // Non-existent saga
		0,
		"step",
		ErrBusinessRuleViolation, // Use a known error
		5,
	)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSagaNotFound)
	assert.Nil(t, result)
}

// TestUpdateSagaError verifies the error update helper.
func TestUpdateSagaError(t *testing.T) {
	instance := &SagaInstance{
		ID:     uuid.New(),
		Status: SagaStatusRunning,
	}

	testErr := errors.New("test error message")
	UpdateSagaError(instance, 3, testErr, ErrorCategoryFatal)

	assert.NotNil(t, instance.FailedStepIndex)
	assert.Equal(t, 3, *instance.FailedStepIndex)
	assert.NotNil(t, instance.ErrorMessage)
	assert.Equal(t, "test error message", *instance.ErrorMessage)
	assert.NotNil(t, instance.ErrorCategory)
	assert.Equal(t, string(ErrorCategoryFatal), *instance.ErrorCategory)
}

// TestIntegration_FatalErrorSkipsToCompensation is an integration-style test that
// verifies the complete flow: FATAL error -> immediate compensation, no retry.
func TestIntegration_FatalErrorSkipsToCompensation(t *testing.T) {
	// This test verifies FR-28: FATAL errors should skip to compensation immediately
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 2,
		ReplayCount:      0,
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	// Simulate a step handler returning insufficient funds error
	insufficientFundsErr := ErrInsufficientFunds
	result, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		2,
		"debit_account",
		insufficientFundsErr,
		5,
	)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify: FATAL -> COMPENSATE action (not RETRY)
	assert.Equal(t, FailureActionCompensate, result.Action,
		"FATAL error should result in COMPENSATE action, not RETRY")

	// Verify: Saga transitioned to COMPENSATING (not still RUNNING)
	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	assert.Equal(t, SagaStatusCompensating, updated.Status,
		"Saga should be in COMPENSATING status after FATAL error")

	// Verify: Error category recorded correctly
	assert.NotNil(t, updated.ErrorCategory)
	assert.Equal(t, string(ErrorCategoryFatal), *updated.ErrorCategory)

	// Verify: Failed step index recorded
	assert.NotNil(t, updated.FailedStepIndex)
	assert.Equal(t, 2, *updated.FailedStepIndex)

	// Verify: Error message recorded
	assert.NotNil(t, updated.ErrorMessage)
	assert.Contains(t, *updated.ErrorMessage, "insufficient funds")
}

// TestIntegration_TransientErrorAllowsRetry is an integration-style test that
// verifies the complete flow: TRANSIENT error -> retry allowed, no compensation.
func TestIntegration_TransientErrorAllowsRetry(t *testing.T) {
	// This test verifies FR-28: TRANSIENT errors should allow retry
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 1,
		ReplayCount:      1, // Has been retried once, still under limit
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	// Simulate a step handler returning network timeout error
	timeoutErr := errors.New("connection timeout exceeded")
	result, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		1,
		"call_external_service",
		timeoutErr,
		5, // max 5 replays
	)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify: TRANSIENT -> RETRY action (not COMPENSATE)
	assert.Equal(t, FailureActionRetry, result.Action,
		"TRANSIENT error should result in RETRY action, not COMPENSATE")

	// Verify: Saga stays in RUNNING (not COMPENSATING)
	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	assert.Equal(t, SagaStatusRunning, updated.Status,
		"Saga should remain in RUNNING status for TRANSIENT error (waiting for retry)")
}
