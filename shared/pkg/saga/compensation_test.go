package saga

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test LIFO compensation execution order
func TestExecuteCompensation_LIFOOrder(t *testing.T) {
	ctx := context.Background()
	registry := NewHandlerRegistry()

	// Track compensation execution order
	var executionOrder []string

	// Register compensation handlers that record their execution
	err := registry.Register("handler.compensate_a", func(_ *StarlarkContext, _ map[string]any) (any, error) {
		executionOrder = append(executionOrder, "A")
		return map[string]any{"status": "compensated"}, nil
	})
	require.NoError(t, err)

	err = registry.Register("handler.compensate_b", func(_ *StarlarkContext, _ map[string]any) (any, error) {
		executionOrder = append(executionOrder, "B")
		return map[string]any{"status": "compensated"}, nil
	})
	require.NoError(t, err)

	err = registry.Register("handler.compensate_c", func(_ *StarlarkContext, _ map[string]any) (any, error) {
		executionOrder = append(executionOrder, "C")
		return map[string]any{"status": "compensated"}, nil
	})
	require.NoError(t, err)

	// Create runner
	runtime, err := NewRuntime(nil)
	require.NoError(t, err)

	runner, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
		Runtime:  runtime,
		Registry: registry,
	})
	require.NoError(t, err)

	// Create StarlarkContext
	starlarkCtx := &StarlarkContext{
		Context: ctx,
	}

	// Simulate three completed steps (executed in order A → B → C)
	completedSteps := []StepResult{
		{
			StepName:          "handler.step_a",
			Success:           true,
			Output:            map[string]interface{}{"id": "a-id"},
			CompensateHandler: "handler.compensate_a",
			CompensateParams:  map[string]any{"id": "a-id"},
		},
		{
			StepName:          "handler.step_b",
			Success:           true,
			Output:            map[string]interface{}{"id": "b-id"},
			CompensateHandler: "handler.compensate_b",
			CompensateParams:  map[string]any{"id": "b-id"},
		},
		{
			StepName:          "handler.step_c",
			Success:           true,
			Output:            map[string]interface{}{"id": "c-id"},
			CompensateHandler: "handler.compensate_c",
			CompensateParams:  map[string]any{"id": "c-id"},
		},
	}

	// Execute compensation
	err = runner.executeCompensation(starlarkCtx, completedSteps)
	require.NoError(t, err)

	// Verify LIFO order: C → B → A (reverse of execution order)
	assert.Equal(t, []string{"C", "B", "A"}, executionOrder)
}

// Test compensation skips steps without compensation handlers
func TestExecuteCompensation_SkipsStepsWithoutHandlers(t *testing.T) {
	ctx := context.Background()
	registry := NewHandlerRegistry()

	var executed []string

	err := registry.Register("handler.compensate_a", func(_ *StarlarkContext, _ map[string]any) (any, error) {
		executed = append(executed, "A")
		return map[string]any{}, nil
	})
	require.NoError(t, err)

	err = registry.Register("handler.compensate_c", func(_ *StarlarkContext, _ map[string]any) (any, error) {
		executed = append(executed, "C")
		return map[string]any{}, nil
	})
	require.NoError(t, err)

	runtime, err := NewRuntime(nil)
	require.NoError(t, err)

	runner, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
		Runtime:  runtime,
		Registry: registry,
	})
	require.NoError(t, err)

	starlarkCtx := &StarlarkContext{Context: ctx}

	// Step B has no compensation handler
	completedSteps := []StepResult{
		{StepName: "handler.step_a", Success: true, CompensateHandler: "handler.compensate_a", CompensateParams: map[string]any{}},
		{StepName: "handler.step_b", Success: true, CompensateHandler: "", CompensateParams: map[string]any{}}, // No compensation
		{StepName: "handler.step_c", Success: true, CompensateHandler: "handler.compensate_c", CompensateParams: map[string]any{}},
	}

	err = runner.executeCompensation(starlarkCtx, completedSteps)
	require.NoError(t, err)

	// Should execute C and A, but not B
	assert.Equal(t, []string{"C", "A"}, executed)
}

// Test compensation continues on error (best-effort)
func TestExecuteCompensation_ContinuesOnError(t *testing.T) {
	ctx := context.Background()
	registry := NewHandlerRegistry()

	var executed []string

	testError := errors.New("test compensation error")

	err := registry.Register("handler.compensate_a", func(_ *StarlarkContext, _ map[string]any) (any, error) {
		executed = append(executed, "A")
		return map[string]any{}, nil
	})
	require.NoError(t, err)

	err = registry.Register("handler.compensate_b", func(_ *StarlarkContext, _ map[string]any) (any, error) {
		executed = append(executed, "B")
		return nil, testError // Fail B
	})
	require.NoError(t, err)

	err = registry.Register("handler.compensate_c", func(_ *StarlarkContext, _ map[string]any) (any, error) {
		executed = append(executed, "C")
		return map[string]any{}, nil
	})
	require.NoError(t, err)

	runtime, err := NewRuntime(nil)
	require.NoError(t, err)

	runner, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
		Runtime:  runtime,
		Registry: registry,
	})
	require.NoError(t, err)

	starlarkCtx := &StarlarkContext{Context: ctx}

	completedSteps := []StepResult{
		{StepName: "handler.step_a", Success: true, CompensateHandler: "handler.compensate_a", CompensateParams: map[string]any{}},
		{StepName: "handler.step_b", Success: true, CompensateHandler: "handler.compensate_b", CompensateParams: map[string]any{}},
		{StepName: "handler.step_c", Success: true, CompensateHandler: "handler.compensate_c", CompensateParams: map[string]any{}},
	}

	err = runner.executeCompensation(starlarkCtx, completedSteps)

	// Should return error but continue executing all compensations
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "compensation failed")

	// All three compensations should have been attempted
	assert.Equal(t, []string{"C", "B", "A"}, executed)
}

// Test compensation with empty step list
func TestExecuteCompensation_EmptySteps(t *testing.T) {
	ctx := context.Background()
	registry := NewHandlerRegistry()
	runtime, err := NewRuntime(nil)
	require.NoError(t, err)

	runner, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
		Runtime:  runtime,
		Registry: registry,
	})
	require.NoError(t, err)

	starlarkCtx := &StarlarkContext{Context: ctx}

	err = runner.executeCompensation(starlarkCtx, []StepResult{})
	assert.NoError(t, err)
}
