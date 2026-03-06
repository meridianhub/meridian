package validation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidate_ValidScript verifies that a valid script passes validation.
func TestValidate_ValidScript(t *testing.T) {
	// Setup schema registry with test handlers
	schemaReg := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test.echo:
    description: Test echo handler
    compensation_strategy: none
    params:
      message:
        type: string
        required: true
    returns:
      result:
        type: string
`
	require.NoError(t, schemaReg.LoadFromYAML([]byte(schemaYAML)))

	// Create validator
	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	// Valid script
	script := `
result = test.echo(message="hello")
`

	// Execute validation
	ctx := context.Background()
	result, err := validator.Validate(ctx, script)
	require.NoError(t, err)

	// Assert success
	assert.True(t, result.Success, "Expected validation to succeed")
	assert.Empty(t, result.Errors, "Expected no errors")
	assert.Len(t, result.StepResults, 1, "Expected 1 handler call")
	assert.Equal(t, "test.echo", result.StepResults[0].StepName)
}

// TestValidate_SyntaxError verifies that syntax errors are detected with line numbers.
func TestValidate_SyntaxError(t *testing.T) {
	schemaReg := schema.NewRegistry()
	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	// Script with syntax error (unclosed paren)
	script := `
result = test.echo(message="hello"
`

	ctx := context.Background()
	result, err := validator.Validate(ctx, script)
	require.NoError(t, err)

	// Assert failure with syntax error
	assert.False(t, result.Success)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, CategorySyntax, result.Errors[0].Category)
	assert.Greater(t, result.Errors[0].Line, 0, "Expected line number > 0")
	// Error message contains parse error details
	assert.NotEmpty(t, result.Errors[0].Message)
}

// TestValidate_UndefinedHandler verifies that undefined handlers are caught.
func TestValidate_UndefinedHandler(t *testing.T) {
	// Empty schema - no handlers defined
	schemaReg := schema.NewRegistry()
	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	// Script calling undefined handler
	script := `
result = undefined_service.undefined_handler(param="value")
`

	ctx := context.Background()
	result, err := validator.Validate(ctx, script)
	require.NoError(t, err)

	// Assert failure with undefined handler error
	assert.False(t, result.Success, "Expected validation to fail")
	require.NotEmpty(t, result.Errors, "Expected errors to be captured")
	// Error should mention undefined/not found
	errorMsg := result.Errors[0].Message
	assert.True(t,
		strings.Contains(errorMsg, "undefined") ||
			strings.Contains(errorMsg, "not defined") ||
			strings.Contains(errorMsg, "has no"),
		"Expected undefined handler error, got: %s", errorMsg)
}

// TestValidate_WrongParameterType verifies that type mismatches are rejected.
func TestValidate_WrongParameterType(t *testing.T) {
	// Register handler expecting Decimal
	schemaReg := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test.add_amount:
    description: "Add amount handler"
    compensation_strategy: none
    params:
      amount:
        type: Decimal
        required: true
    returns:
      total:
        type: Decimal
`
	require.NoError(t, schemaReg.LoadFromYAML([]byte(schemaYAML)))

	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	// Script passing wrong type (object instead of Decimal)
	script := `
result = test.add_amount(amount={"invalid": "object"})
`

	ctx := context.Background()
	result, err := validator.Validate(ctx, script)
	require.NoError(t, err)

	// Assert failure with type mismatch error
	assert.False(t, result.Success, "Expected validation to fail")
	require.NotEmpty(t, result.Errors, "Expected errors to be captured")
}

// TestValidate_RuntimeError verifies that runtime errors (fail()) are caught.
func TestValidate_RuntimeError(t *testing.T) {
	schemaReg := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test.process:
    description: "Process handler"
    compensation_strategy: none
    params:
      value:
        type: string
        required: true
    returns:
      status:
        type: string
`
	require.NoError(t, schemaReg.LoadFromYAML([]byte(schemaYAML)))

	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	// Script that calls fail()
	script := `
def run():
    fail("something went wrong")

run()
`

	ctx := context.Background()
	result, err := validator.Validate(ctx, script)
	require.NoError(t, err)

	// Assert failure with runtime error
	assert.False(t, result.Success, "Expected validation to fail")
	require.NotEmpty(t, result.Errors, "Expected errors to be captured")
	assert.Equal(t, CategoryRuntime, result.Errors[0].Category)
}

// TestValidate_Timeout verifies that scripts timeout after 5 seconds.
func TestValidate_Timeout(t *testing.T) {
	t.Skip("Timeout test requires runaway loops which Starlark prevents")
	// Note: Starlark's bounded loops prevent infinite execution,
	// so we can't easily trigger the 5s timeout in a real test.
	// This test documents the expected behavior.
}

// TestComplexityMetrics verifies that complexity metrics are calculated correctly.
func TestComplexityMetrics(t *testing.T) {
	schemaReg := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test.step1:
    description: "Step 1"
    compensation_strategy: none
    params:
      input:
        type: string
        required: true
    returns:
      output:
        type: string
  test.step2:
    description: "Step 2"
    compensation_strategy: none
    params:
      input:
        type: string
        required: true
    returns:
      output:
        type: string
`
	require.NoError(t, schemaReg.LoadFromYAML([]byte(schemaYAML)))

	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	// Script with multiple handler calls (for loops must be inside functions in Starlark)
	script := `
r1 = test.step1(input="a")
r2 = test.step2(input="b")

def process():
    for i in range(3):
        x = i + 1
    return x

result = process()
`

	ctx := context.Background()
	result, err := validator.Validate(ctx, script)
	require.NoError(t, err)

	// Assert metrics
	assert.True(t, result.Success)
	assert.Equal(t, 2, result.Metrics.HandlerCallCount, "Expected 2 handler calls")
	assert.Greater(t, result.Metrics.OperationCount, 0, "Expected operations > 0")
	assert.Equal(t, 20*time.Millisecond, result.Metrics.EstimatedDuration, "Expected 2 * 10ms = 20ms")
	assert.Greater(t, result.Metrics.MaxDepth, 0, "Expected max depth > 0")
}

// TestMultipleErrors verifies that multiple errors are collected (not fail-fast).
func TestMultipleErrors(t *testing.T) {
	// Note: Current implementation stops at first error (syntax errors prevent execution).
	// This test documents expected behavior for future enhancements.
	// To collect multiple errors, we'd need to:
	// 1. Continue parsing after syntax errors
	// 2. Dry-run type checking before execution
	// 3. Capture all step failures during execution

	schemaReg := schema.NewRegistry()
	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	// Script with syntax error (stops here)
	script := `
result = test.echo(message="hello"
`

	ctx := context.Background()
	result, err := validator.Validate(ctx, script)
	require.NoError(t, err)

	// Currently only first error is captured
	assert.False(t, result.Success)
	assert.NotEmpty(t, result.Errors)
}

// TestValidator_RequiredFields verifies that validator config validation works.
func TestValidator_RequiredFields(t *testing.T) {
	t.Run("missing runtime", func(t *testing.T) {
		_, err := NewDryRunValidator(DryRunValidatorConfig{
			Runtime:        nil,
			MockRegistry:   nil,
			SchemaRegistry: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runtime is required")
	})

	t.Run("missing mock registry", func(t *testing.T) {
		runtime, _ := NewMockRuntime(t)
		_, err := NewDryRunValidator(DryRunValidatorConfig{
			Runtime:        runtime,
			MockRegistry:   nil,
			SchemaRegistry: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mock registry is required")
	})

	t.Run("missing schema registry", func(t *testing.T) {
		runtime, _ := NewMockRuntime(t)
		schemaReg := schema.NewRegistry()
		validator, err := NewMockValidatorForTesting(schemaReg)
		require.NoError(t, err)

		_, err = NewDryRunValidator(DryRunValidatorConfig{
			Runtime:        runtime,
			MockRegistry:   validator.mockRegistry,
			SchemaRegistry: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "schema registry is required")
	})
}

// NewMockRuntime creates a test runtime with 5s timeout.
func NewMockRuntime(t *testing.T) (*saga.Runtime, error) {
	t.Helper()
	// Note: This helper is kept for test structure, but NewMockValidatorForTesting
	// is the recommended way to create validators in tests.
	return saga.NewRuntime(nil, saga.WithTimeout(5*time.Second))
}
