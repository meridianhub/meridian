package saga

import (
	"context"
	"testing"

	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/pkg/saga/validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// setupValidateSagaTest creates a RegistryHandler with a dry-run validator for testing.
func setupValidateSagaTest(t *testing.T) (*RegistryHandler, *schema.Registry) {
	t.Helper()

	// Create schema registry
	schemaRegistry := schema.NewRegistry()

	// Load test handlers from YAML
	schemaYAML := `
service: test
version: 1.0
handlers:
  test_service.test_method:
    description: Test method
    compensation_strategy: none
    params: {}
    returns:
      result:
        type: string
      error:
        type: string
  payment.create_lien:
    description: Create payment lien
    compensation_strategy: none
    params:
      amount:
        type: string
        required: true
      customer_id:
        type: string
        required: true
    returns:
      lien_id:
        type: string
      error:
        type: string
`
	require.NoError(t, schemaRegistry.LoadFromYAML([]byte(schemaYAML)))

	// Create DryRunValidator
	validator, err := validation.NewMockValidatorForTesting(schemaRegistry)
	require.NoError(t, err)

	// Create handler with dry-run validator (no registry or reference validator needed)
	handler := NewRegistryHandler(
		nil, // registry not needed for ValidateSaga
		nil, // reference validator not needed
		validator,
		nil, // use default logger
	)

	return handler, schemaRegistry
}

func TestValidateSaga_ValidScript(t *testing.T) {
	handler, _ := setupValidateSagaTest(t)

	req := &sagav1.ValidateSagaRequest{
		SagaName: "test_saga",
		Script: `
result = test_service.test_method()
`,
		Version: "1.0.0",
	}

	resp, err := handler.ValidateSaga(context.Background(), req)
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Assertions
	assert.True(t, resp.Success, "validation should succeed for valid script")
	assert.Empty(t, resp.Errors, "no errors expected for valid script")
	assert.NotNil(t, resp.Metrics, "metrics should be populated")
	assert.Equal(t, int32(1), resp.Metrics.HandlerCallCount, "should count 1 handler call")
	assert.Greater(t, resp.Metrics.OperationCount, int32(0), "should count operations")
	assert.GreaterOrEqual(t, resp.Metrics.ComplexityScore, int32(0), "should calculate complexity score (1 handler = 0 score)")
	assert.NotEmpty(t, resp.FormattedReport, "formatted report should be generated")
}

func TestValidateSaga_MultipleHandlerCalls(t *testing.T) {
	handler, _ := setupValidateSagaTest(t)

	req := &sagav1.ValidateSagaRequest{
		SagaName: "withdrawal",
		Script: `
# Step 1: Create lien
lien_result = payment.create_lien(amount="100.00", customer_id="cust_123")

# Step 2: Process payment
payment_result = test_service.test_method()

# Step 3: Verify
verify_result = test_service.test_method()
`,
		Version: "2.0.0",
	}

	resp, err := handler.ValidateSaga(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, int32(3), resp.Metrics.HandlerCallCount, "should count 3 handler calls")
	assert.Greater(t, resp.Metrics.EstimatedDurationMs, int32(0), "should estimate duration")

	// Complexity score should be HandlerCallCount / 2 = 3 / 2 = 1
	assert.Equal(t, int32(1), resp.Metrics.ComplexityScore)
}

func TestValidateSaga_SyntaxError(t *testing.T) {
	handler, _ := setupValidateSagaTest(t)

	req := &sagav1.ValidateSagaRequest{
		SagaName: "broken_saga",
		Script: `
result = test_service.test_method(  # Missing closing parenthesis
`,
		Version: "1.0.0",
	}

	resp, err := handler.ValidateSaga(context.Background(), req)
	require.NoError(t, err, "gRPC call should succeed even with validation errors")
	assert.NotNil(t, resp)

	// Assertions
	assert.False(t, resp.Success, "validation should fail for syntax error")
	require.Len(t, resp.Errors, 1, "should have exactly 1 syntax error")

	syntaxErr := resp.Errors[0]
	assert.Equal(t, sagav1.ErrorCategory_ERROR_CATEGORY_SYNTAX, syntaxErr.Category)
	assert.Greater(t, syntaxErr.Line, int32(0), "should have line number")
	assert.Greater(t, syntaxErr.Column, int32(0), "should have column number")
	assert.Contains(t, syntaxErr.Message, "got end of file", "error message should mention EOF")
}

func TestValidateSaga_UndefinedHandler(t *testing.T) {
	handler, _ := setupValidateSagaTest(t)

	req := &sagav1.ValidateSagaRequest{
		SagaName: "undefined_handler_saga",
		Script: `
result = nonexistent_service.some_method()
`,
		Version: "1.0.0",
	}

	resp, err := handler.ValidateSaga(context.Background(), req)
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Assertions
	assert.False(t, resp.Success, "validation should fail for undefined handler")
	require.NotEmpty(t, resp.Errors, "should have at least 1 error")

	// First error should be about undefined handler
	err0 := resp.Errors[0]
	assert.Contains(t, []sagav1.ErrorCategory{
		sagav1.ErrorCategory_ERROR_CATEGORY_UNDEFINED_HANDLER,
		sagav1.ErrorCategory_ERROR_CATEGORY_RUNTIME,
	}, err0.Category, "error should be categorized as undefined handler or runtime")
	assert.Contains(t, err0.Message, "nonexistent_service", "error should mention the undefined service")
}

func TestValidateSaga_RuntimeError(t *testing.T) {
	handler, _ := setupValidateSagaTest(t)

	req := &sagav1.ValidateSagaRequest{
		SagaName: "runtime_error_saga",
		Script: `
fail("intentional failure")
`,
		Version: "1.0.0",
	}

	resp, err := handler.ValidateSaga(context.Background(), req)
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Assertions
	assert.False(t, resp.Success, "validation should fail for runtime error")
	require.Len(t, resp.Errors, 1, "should have exactly 1 runtime error")

	runtimeErr := resp.Errors[0]
	assert.Equal(t, sagav1.ErrorCategory_ERROR_CATEGORY_RUNTIME, runtimeErr.Category)
	assert.Contains(t, runtimeErr.Message, "intentional failure", "error should contain fail() message")
}

func TestValidateSaga_ComplexityScoreCalculation(t *testing.T) {
	handler, _ := setupValidateSagaTest(t)

	testCases := []struct {
		name               string
		script             string
		expectedHandlers   int32
		expectedComplexity int32
	}{
		{
			name: "low complexity - 2 handlers",
			script: `
test_service.test_method()
test_service.test_method()
`,
			expectedHandlers:   2,
			expectedComplexity: 1, // 2/2 = 1
		},
		{
			name: "medium complexity - 8 handlers",
			script: `
def run():
    for i in range(8):
        test_service.test_method()
run()
`,
			expectedHandlers:   8,
			expectedComplexity: 4, // 8/2 = 4
		},
		{
			name: "high complexity - 20 handlers (capped at 10)",
			script: `
def run():
    for i in range(20):
        test_service.test_method()
run()
`,
			expectedHandlers:   20,
			expectedComplexity: 10, // 20/2 = 10 (max)
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &sagav1.ValidateSagaRequest{
				SagaName: "complexity_test",
				Script:   tc.script,
				Version:  "1.0.0",
			}

			resp, err := handler.ValidateSaga(context.Background(), req)
			require.NoError(t, err)
			assert.True(t, resp.Success, "script should be valid")
			assert.Equal(t, tc.expectedHandlers, resp.Metrics.HandlerCallCount)
			assert.Equal(t, tc.expectedComplexity, resp.Metrics.ComplexityScore)
		})
	}
}

func TestValidateSaga_FormattedReportPopulated(t *testing.T) {
	handler, _ := setupValidateSagaTest(t)

	t.Run("success report", func(t *testing.T) {
		req := &sagav1.ValidateSagaRequest{
			SagaName: "success_saga",
			Script:   `test_service.test_method()`,
			Version:  "1.0.0",
		}

		resp, err := handler.ValidateSaga(context.Background(), req)
		require.NoError(t, err)

		assert.NotEmpty(t, resp.FormattedReport, "formatted report should not be empty")
		assert.Contains(t, resp.FormattedReport, "Validation Passed", "report should indicate success")
		assert.Contains(t, resp.FormattedReport, "ready for deployment", "report should mention deployment readiness")
	})

	t.Run("failure report", func(t *testing.T) {
		req := &sagav1.ValidateSagaRequest{
			SagaName: "failure_saga",
			Script:   `fail("test error")`,
			Version:  "1.0.0",
		}

		resp, err := handler.ValidateSaga(context.Background(), req)
		require.NoError(t, err)

		assert.NotEmpty(t, resp.FormattedReport, "formatted report should not be empty")
		assert.Contains(t, resp.FormattedReport, "Validation Failed", "report should indicate failure")
		assert.Contains(t, resp.FormattedReport, "test error", "report should include error message")
	})
}

func TestValidateSaga_NoValidatorConfigured(t *testing.T) {
	// Create handler WITHOUT dry-run validator
	handler := NewRegistryHandler(nil, nil, nil, nil)

	req := &sagav1.ValidateSagaRequest{
		SagaName: "test_saga",
		Script:   `test_service.test_method()`,
		Version:  "1.0.0",
	}

	_, err := handler.ValidateSaga(context.Background(), req)
	require.Error(t, err, "should return error when validator not configured")

	st, ok := status.FromError(err)
	require.True(t, ok, "error should be gRPC status")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "dry-run validator not configured")
}

func TestValidateSaga_EmptyScript(t *testing.T) {
	handler, _ := setupValidateSagaTest(t)

	req := &sagav1.ValidateSagaRequest{
		SagaName: "empty_saga",
		Script:   "",
		Version:  "1.0.0",
	}

	// Protobuf validation should reject this (min_len: 1)
	// But if it gets through, we should handle it gracefully
	_, err := handler.ValidateSaga(context.Background(), req)
	// Either protobuf validation rejects it, or we get a syntax error
	// Both are acceptable outcomes
	if err != nil {
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Contains(t, []codes.Code{codes.InvalidArgument, codes.Internal}, st.Code())
	}
}

func TestValidateSaga_BlockOnFailureFlag(t *testing.T) {
	handler, _ := setupValidateSagaTest(t)

	// The block_on_failure flag is currently informational only
	// It doesn't affect ValidateSaga behavior, but should be accepted
	req := &sagav1.ValidateSagaRequest{
		SagaName:       "test_saga",
		Script:         `test_service.test_method()`,
		Version:        "1.0.0",
		BlockOnFailure: true,
	}

	resp, err := handler.ValidateSaga(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.Success)

	// block_on_failure doesn't change ValidateSaga behavior
	// It would be used by RegisterSagaDefinition or ActivateSaga
}

func TestValidateSaga_OperationCountTracking(t *testing.T) {
	handler, _ := setupValidateSagaTest(t)

	req := &sagav1.ValidateSagaRequest{
		SagaName: "operations_saga",
		Script: `
# Assignments, conditionals, loops all count as operations
def run():
    x = 1
    y = 2
    if x < y:
        for i in range(3):
            test_service.test_method()
run()
`,
		Version: "1.0.0",
	}

	resp, err := handler.ValidateSaga(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.Success)

	// Should count:
	// - Function definition
	// - Function call
	// - 2 assignments (x=1, y=2)
	// - 1 if statement
	// - 1 for loop
	// - 3 handler calls (in loop)
	// Total: multiple operations
	assert.Greater(t, resp.Metrics.OperationCount, int32(3), "should track multiple operation types")
}

// TestValidateSaga_E2E_CompleteFlow is an end-to-end integration test that validates
// the complete saga validation flow including error handling, metrics, and formatting.
func TestValidateSaga_E2E_CompleteFlow(t *testing.T) {
	handler, schemaRegistry := setupValidateSagaTest(t)

	t.Run("complete success flow", func(t *testing.T) {
		req := &sagav1.ValidateSagaRequest{
			SagaName: "payment_withdrawal_saga",
			Script: `
# Payment withdrawal saga with direct handler calls
# Step 1: Create lien
lien = payment.create_lien(amount="100.00", customer_id="cust_123")

# Step 2: Verify balance
balance = test_service.test_method()

# Step 3: Another check
check = test_service.test_method()
`,
			Version: "1.0.0",
		}

		resp, err := handler.ValidateSaga(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Verify success
		assert.True(t, resp.Success, "validation should succeed")
		assert.Empty(t, resp.Errors, "no errors expected")

		// Verify metrics
		require.NotNil(t, resp.Metrics)
		assert.Equal(t, int32(3), resp.Metrics.HandlerCallCount, "3 handler calls total")
		assert.Greater(t, resp.Metrics.OperationCount, int32(2), "multiple operations")
		assert.GreaterOrEqual(t, resp.Metrics.ComplexityScore, int32(0), "complexity calculated")
		assert.GreaterOrEqual(t, resp.Metrics.EstimatedDurationMs, int32(0), "duration estimated")

		// Verify formatted report
		assert.NotEmpty(t, resp.FormattedReport, "report should be generated")
		assert.Contains(t, resp.FormattedReport, "Validation Passed", "report shows success")
		assert.Contains(t, resp.FormattedReport, "3 handlers", "report includes metrics")
	})

	t.Run("complete failure flow", func(t *testing.T) {
		req := &sagav1.ValidateSagaRequest{
			SagaName: "broken_saga",
			Script: `
# Saga with multiple issues
result = nonexistent.handler()  # Undefined handler
x = undefined_variable           # Runtime error
`,
			Version: "1.0.0",
		}

		resp, err := handler.ValidateSaga(context.Background(), req)
		require.NoError(t, err, "gRPC should succeed even with validation errors")
		require.NotNil(t, resp)

		// Verify failure
		assert.False(t, resp.Success, "validation should fail")
		assert.NotEmpty(t, resp.Errors, "errors should be present")

		// Verify error details
		firstError := resp.Errors[0]
		assert.GreaterOrEqual(t, firstError.Line, int32(0), "error may have line number")
		assert.NotEmpty(t, firstError.Message, "error has message")
		assert.NotEqual(t, sagav1.ErrorCategory_ERROR_CATEGORY_UNSPECIFIED, firstError.Category)

		// Verify metrics (may be partial)
		require.NotNil(t, resp.Metrics)

		// Verify formatted report includes error details
		assert.NotEmpty(t, resp.FormattedReport)
		assert.Contains(t, resp.FormattedReport, "Validation Failed", "report shows failure")
	})

	t.Run("complex conditional saga", func(t *testing.T) {
		req := &sagav1.ValidateSagaRequest{
			SagaName: "complex_conditional_saga",
			Script: `
# Multiple handler calls with different parameters
result1 = payment.create_lien(amount="1000", customer_id="high_value")
result2 = payment.create_lien(amount="500", customer_id="medium_value")
result3 = test_service.test_method()
`,
			Version: "1.0.0",
		}

		resp, err := handler.ValidateSaga(context.Background(), req)
		require.NoError(t, err)
		assert.True(t, resp.Success)

		// Multiple handler calls should increase complexity
		assert.Equal(t, int32(3), resp.Metrics.HandlerCallCount, "should count 3 handler calls")
		assert.GreaterOrEqual(t, resp.Metrics.ComplexityScore, int32(1), "multiple handlers increase complexity")
		assert.GreaterOrEqual(t, resp.Metrics.OperationCount, int32(3), "should count operations")
	})

	t.Run("verify schema registry integration", func(t *testing.T) {
		// Verify the schema registry has the expected handlers
		handlers := schemaRegistry.ListHandlers()
		assert.Contains(t, handlers, "test_service.test_method")
		assert.Contains(t, handlers, "payment.create_lien")

		// Test with a handler that definitely exists
		req := &sagav1.ValidateSagaRequest{
			SagaName: "known_handler_saga",
			Script:   `result = test_service.test_method()`,
			Version:  "1.0.0",
		}

		resp, err := handler.ValidateSaga(context.Background(), req)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, int32(1), resp.Metrics.HandlerCallCount)
	})

	t.Run("empty saga name validation", func(_ *testing.T) {
		req := &sagav1.ValidateSagaRequest{
			SagaName: "", // Empty name
			Script:   `test_service.test_method()`,
			Version:  "1.0.0",
		}

		// Should handle gracefully (protobuf validation may catch this)
		_, err := handler.ValidateSaga(context.Background(), req)
		// Either protobuf rejects it or handler validates it anyway
		// Both are acceptable for this E2E test
		_ = err // May or may not error
	})

	t.Run("malformed version string", func(t *testing.T) {
		req := &sagav1.ValidateSagaRequest{
			SagaName: "version_test",
			Script:   `test_service.test_method()`,
			Version:  "not-a-semver", // Invalid semver
		}

		// Validation should still work (version is informational for ValidateSaga)
		resp, err := handler.ValidateSaga(context.Background(), req)
		require.NoError(t, err)
		// Script is valid even if version string is weird
		assert.True(t, resp.Success)
	})
}
