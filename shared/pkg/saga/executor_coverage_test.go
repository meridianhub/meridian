package saga

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSagaExecutor_WithLogger(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()
	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	logger := slog.Default()
	result := executor.WithLogger(logger)
	assert.Same(t, executor, result, "WithLogger should return the same executor for chaining")
	assert.Same(t, logger, executor.logger)
}

func TestSagaExecutor_ProcessStepResult_Success(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()
	stepResultRepo := NewMockStepResultRepository()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      3,
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	// Success case: err is nil
	failureResult, err := executor.ProcessStepResult(
		context.Background(),
		instance,
		0,
		"test_step",
		map[string]interface{}{"result": "ok"},
		nil, // no error = success
		5,
	)

	require.NoError(t, err)
	assert.Nil(t, failureResult, "success should return nil failure result")

	// Verify replay count was reset
	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	assert.Equal(t, 0, updated.ReplayCount)
}

func TestSagaExecutor_ProcessStepResult_Failure(t *testing.T) {
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

	// Failure case: err is non-nil FATAL error
	failureResult, err := executor.ProcessStepResult(
		context.Background(),
		instance,
		1,
		"debit_account",
		nil,
		ErrInsufficientFunds,
		5,
	)

	require.NoError(t, err)
	require.NotNil(t, failureResult)
	assert.Equal(t, ErrorCategoryFatal, failureResult.ErrorCategory)
	assert.Equal(t, FailureActionCompensate, failureResult.Action)
}

func TestSagaExecutor_HandleStepFailure_UpdateStatusError(t *testing.T) {
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
	instanceRepo.updateStatusErr = errors.New("database connection lost")

	executor := NewSagaExecutor(instanceRepo, stepResultRepo, nil, nil)

	// FATAL error but UpdateStatusWithError fails
	result, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		0,
		"step",
		ErrInsufficientFunds,
		5,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to transition saga to COMPENSATING")
	assert.Nil(t, result)
}

func TestUpdateSagaError_NilError(t *testing.T) {
	instance := &SagaInstance{
		ID:     uuid.New(),
		Status: SagaStatusRunning,
	}

	UpdateSagaError(instance, 2, nil, ErrorCategoryFatal)

	assert.NotNil(t, instance.FailedStepIndex)
	assert.Equal(t, 2, *instance.FailedStepIndex)
	assert.Nil(t, instance.ErrorMessage, "nil error should not set error message")
	assert.NotNil(t, instance.ErrorCategory)
	assert.Equal(t, string(ErrorCategoryFatal), *instance.ErrorCategory)
}
