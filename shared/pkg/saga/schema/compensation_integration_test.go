package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test end-to-end compensation flow with typed service modules
func TestCompensation_EndToEnd_WithServiceModules(t *testing.T) {
	ctx := context.Background()

	// Track handler execution order
	var executionLog []string

	// Create handler registry
	registry := saga.NewHandlerRegistry()

	// Register forward handlers
	err := registry.Register("test_service.create_resource", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		executionLog = append(executionLog, "create_resource")
		return map[string]any{
			"resource_id": "res-123",
			"status":      "CREATED",
		}, nil
	})
	require.NoError(t, err)

	err = registry.Register("test_service.allocate_quota", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		executionLog = append(executionLog, "allocate_quota")
		return map[string]any{
			"allocation_id": "alloc-456",
			"status":        "ALLOCATED",
		}, nil
	})
	require.NoError(t, err)

	err = registry.Register("test_service.failing_step", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		executionLog = append(executionLog, "failing_step")
		return nil, errors.New("intentional failure")
	})
	require.NoError(t, err)

	// Register compensation handlers
	err = registry.Register("test_service.delete_resource", func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
		executionLog = append(executionLog, "delete_resource:"+params["resource_id"].(string))
		return map[string]any{"status": "DELETED"}, nil
	})
	require.NoError(t, err)

	err = registry.Register("test_service.release_quota", func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
		executionLog = append(executionLog, "release_quota:"+params["allocation_id"].(string))
		return map[string]any{"status": "RELEASED"}, nil
	})
	require.NoError(t, err)

	// Create handler schema with compensation handlers
	schemaRegistry := NewRegistry()
	schemaYAML := `
service: test_service
version: "1.0"
handlers:
  test_service.create_resource:
    description: "Create a resource"
    params:
      name:
        type: string
        required: true
    returns:
      resource_id:
        type: string
      status:
        type: string
    compensate: test_service.delete_resource

  test_service.allocate_quota:
    description: "Allocate quota"
    params:
      amount:
        type: string
        required: true
    returns:
      allocation_id:
        type: string
      status:
        type: string
    compensate: test_service.release_quota

  test_service.failing_step:
    description: "A step that fails"
    compensation_strategy: none
    params: {}
    returns:
      status:
        type: string

  test_service.delete_resource:
    description: "Delete a resource (compensation)"
    compensation_strategy: none
    params:
      resource_id:
        type: string
        required: true
    returns:
      status:
        type: string

  test_service.release_quota:
    description: "Release quota (compensation)"
    compensation_strategy: none
    params:
      allocation_id:
        type: string
        required: true
    returns:
      status:
        type: string
`
	err = schemaRegistry.LoadFromYAML([]byte(schemaYAML))
	require.NoError(t, err)

	// Build service modules
	serviceModules, err := BuildServiceModules(registry, schemaRegistry)
	require.NoError(t, err)

	// Create runtime and runner
	runtime, err := saga.NewRuntime(nil)
	require.NoError(t, err)

	runner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       registry,
		ServiceModules: serviceModules,
	})
	require.NoError(t, err)

	// Define a saga script that uses typed service modules and fails at the third step.
	// Handler calls must be at the top level because the Starlark runtime uses ExecFile,
	// which executes top-level statements (it does not call a "saga" function).
	script := `
# Step 1: Create resource
res1 = test_service.create_resource(name="my-resource")

# Step 2: Allocate quota
res2 = test_service.allocate_quota(amount="100")

# Step 3: This will fail, triggering compensation
res3 = test_service.failing_step()

final_status = "SUCCESS"
`

	// Execute saga
	output, err := runner.ExecuteSaga(ctx, "test_saga", script, saga.RunnerInput{
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		Input:           map[string]interface{}{},
	})
	require.NoError(t, err)

	// Verify saga failed
	assert.False(t, output.Success)
	assert.Contains(t, output.Error, "intentional failure")

	// Verify execution order: forward steps, then compensation in LIFO order
	expectedLog := []string{
		"create_resource",
		"allocate_quota",
		"failing_step",
		"release_quota:alloc-456", // Compensate step 2 first (LIFO)
		"delete_resource:res-123", // Then compensate step 1
	}
	assert.Equal(t, expectedLog, executionLog)

	// Verify step results captured compensation metadata.
	// Service modules track all steps including failures via thread-local saga.StepResults.
	require.Len(t, output.StepResults, 3)
	assert.Equal(t, "test_service.create_resource", output.StepResults[0].StepName)
	assert.True(t, output.StepResults[0].Success)
	assert.Equal(t, "test_service.delete_resource", output.StepResults[0].CompensateHandler)
	assert.Equal(t, "res-123", output.StepResults[0].CompensateParams["resource_id"])

	assert.Equal(t, "test_service.allocate_quota", output.StepResults[1].StepName)
	assert.True(t, output.StepResults[1].Success)
	assert.Equal(t, "test_service.release_quota", output.StepResults[1].CompensateHandler)
	assert.Equal(t, "alloc-456", output.StepResults[1].CompensateParams["allocation_id"])

	assert.Equal(t, "test_service.failing_step", output.StepResults[2].StepName)
	assert.False(t, output.StepResults[2].Success)
}

// Test compensation with successful saga (no compensation should execute)
func TestCompensation_SuccessfulSaga_NoCompensation(t *testing.T) {
	ctx := context.Background()
	var executionLog []string

	registry := saga.NewHandlerRegistry()

	err := registry.Register("test.forward", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		executionLog = append(executionLog, "forward")
		return map[string]any{"id": "123"}, nil
	})
	require.NoError(t, err)

	err = registry.Register("test.compensate", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		executionLog = append(executionLog, "compensate")
		return map[string]any{}, nil
	})
	require.NoError(t, err)

	schemaRegistry := NewRegistry()
	schemaYAML := `
service: test
version: "1.0"
handlers:
  test.forward:
    description: "Forward handler"
    params: {}
    returns:
      id:
        type: string
    compensate: test.compensate
  test.compensate:
    description: "Compensation handler"
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    returns: {}
`
	err = schemaRegistry.LoadFromYAML([]byte(schemaYAML))
	require.NoError(t, err)

	serviceModules, err := BuildServiceModules(registry, schemaRegistry)
	require.NoError(t, err)

	runtime, err := saga.NewRuntime(nil)
	require.NoError(t, err)

	runner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       registry,
		ServiceModules: serviceModules,
	})
	require.NoError(t, err)

	// Handler calls must be at the top level (ExecFile does not call a function).
	// Service module results are structs, so use dot notation (result.id, not result["id"]).
	script := `
result = test.forward()
status = "SUCCESS"
id = result.id
`

	output, err := runner.ExecuteSaga(ctx, "test_saga", script, saga.RunnerInput{
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		Input:           map[string]interface{}{},
	})
	require.NoError(t, err)

	// Verify saga succeeded
	assert.True(t, output.Success)

	// Verify compensation was NOT executed (saga succeeded)
	assert.Equal(t, []string{"forward"}, executionLog)
}
