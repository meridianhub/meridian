package saga

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposer_ExecuteWithComposition(t *testing.T) {
	mockRegistry := &MockRegistry{
		Sagas: map[string]*MockSagaDef{
			"simple-saga": {
				Name:           "simple-saga",
				Script:         `result = "ok"`,
				StepsCompleted: 2,
			},
		},
	}

	composer := NewComposer(mockRegistry, nil)
	ctx := context.Background()

	result, err := composer.ExecuteWithComposition(ctx, "simple-saga", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, ResultStatusCompleted, result.Status)
}

func TestComposer_CompensateSaga(t *testing.T) {
	mockRegistry := &MockRegistry{
		Sagas: map[string]*MockSagaDef{},
	}

	composer := NewComposer(mockRegistry, nil)
	ctx := context.Background()

	err := composer.CompensateSaga(ctx, uuid.New())
	require.NoError(t, err)
}

func TestCallStack_PopEmpty(t *testing.T) {
	stack := NewCallStack()
	entry := stack.Pop()
	assert.Equal(t, CallEntry{}, entry, "popping empty stack should return zero value")
}

func TestCallStack_GetSagaNames(t *testing.T) {
	stack := NewCallStack()
	_ = stack.Push(CallEntry{SagaName: "saga-A", ExecutionID: uuid.New()})
	_ = stack.Push(CallEntry{SagaName: "saga-B", ExecutionID: uuid.New()})

	names := stack.GetSagaNames()
	assert.Equal(t, []string{"saga-A", "saga-B"}, names)
}

func TestCompensationCascade_CompensateAll_ChildFails(t *testing.T) {
	compensateErr := errors.New("compensation failed for child")
	cascade := NewCompensationCascade()

	// Record child with failing compensation
	cascade.RecordChildSaga(&Result{
		ExecutionID: uuid.New(),
		Status:      ResultStatusCompleted,
		CompensateFunc: func(_ context.Context) error {
			return compensateErr
		},
	})

	err := cascade.CompensateAll(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compensate child saga")

	// Status should show child not compensated
	status := cascade.GetCompensationStatus()
	require.Len(t, status, 1)
	assert.False(t, status[0].ChildCompensated)
}

func TestCompensationCascade_CompensateAll_NilCompensateFunc(t *testing.T) {
	cascade := NewCompensationCascade()

	// Record child without compensation function
	cascade.RecordChildSaga(&Result{
		ExecutionID:    uuid.New(),
		Status:         ResultStatusCompleted,
		CompensateFunc: nil,
	})

	err := cascade.CompensateAll(context.Background())
	require.NoError(t, err)

	status := cascade.GetCompensationStatus()
	require.Len(t, status, 1)
	assert.True(t, status[0].ChildCompensated)
}

func TestCompensationCascade_CompensateWithParentStep_NoParent(t *testing.T) {
	cascade := NewCompensationCascade()
	// No parent compensation set, no children

	err := cascade.CompensateWithParentStep(context.Background())
	require.NoError(t, err)
}

func TestComposer_InvokeSaga_NilStack(t *testing.T) {
	mockRegistry := &MockRegistry{
		Sagas: map[string]*MockSagaDef{
			"saga": {Name: "saga", Script: `result = "ok"`, StepsCompleted: 1},
		},
	}

	composer := NewComposer(mockRegistry, nil)
	ctx := context.Background()

	// nil stack should work (no depth checking)
	result, err := composer.InvokeSaga(ctx, "saga", nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, ResultStatusCompleted, result.Status)
}

func TestComposer_InvokeSaga_SagaNotFound(t *testing.T) {
	mockRegistry := &MockRegistry{
		Sagas: map[string]*MockSagaDef{},
	}

	composer := NewComposer(mockRegistry, nil)
	ctx := context.Background()

	_, err := composer.InvokeSaga(ctx, "nonexistent", nil, nil, NewCallStack())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load saga")
}

func TestComposer_InvokeSaga_StepLimitExceeded(t *testing.T) {
	mockRegistry := &MockRegistry{
		Sagas: map[string]*MockSagaDef{
			"large-saga": {
				Name:           "large-saga",
				Script:         `result = "ok"`,
				StepsCompleted: 100,
			},
		},
	}

	composer := NewComposer(mockRegistry, nil)
	ctx := context.Background()

	stack := NewCallStack()
	stack.SetMaxTotalSteps(10)

	_, err := composer.InvokeSaga(ctx, "large-saga", nil, nil, stack)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMaxTotalStepsExceeded)
}
