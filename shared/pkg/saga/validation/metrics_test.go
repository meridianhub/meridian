package validation

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestRecordValidation_Success(t *testing.T) {
	ExposeValidationMetricsForTesting.ValidationTotal.Reset()
	ExposeValidationMetricsForTesting.ErrorsTotal.Reset()

	result := &ValidationResult{
		Success: true,
		Errors:  []ValidationError{},
		Metrics: ComplexityMetrics{
			HandlerCallCount: 4,
			OperationCount:   10,
		},
	}

	RecordValidation("test-saga", result)

	successCount := testutil.ToFloat64(
		ExposeValidationMetricsForTesting.ValidationTotal.WithLabelValues("test-saga", "success"),
	)
	assert.Equal(t, float64(1), successCount)

	failedCount := testutil.ToFloat64(
		ExposeValidationMetricsForTesting.ValidationTotal.WithLabelValues("test-saga", "failed"),
	)
	assert.Equal(t, float64(0), failedCount)
}

func TestRecordValidation_Failed(t *testing.T) {
	ExposeValidationMetricsForTesting.ValidationTotal.Reset()
	ExposeValidationMetricsForTesting.ErrorsTotal.Reset()

	result := &ValidationResult{
		Success: false,
		Errors: []ValidationError{
			{Line: 1, Column: 5, Message: "syntax error", Category: CategorySyntax},
			{Line: 3, Column: 1, Message: "unknown handler", Category: CategoryUndefinedHandler},
		},
		Metrics: ComplexityMetrics{
			HandlerCallCount: 2,
		},
	}

	RecordValidation("broken-saga", result)

	failedCount := testutil.ToFloat64(
		ExposeValidationMetricsForTesting.ValidationTotal.WithLabelValues("broken-saga", "failed"),
	)
	assert.Equal(t, float64(1), failedCount)

	syntaxErrors := testutil.ToFloat64(
		ExposeValidationMetricsForTesting.ErrorsTotal.WithLabelValues("broken-saga", "SYNTAX"),
	)
	assert.Equal(t, float64(1), syntaxErrors)

	undefinedErrors := testutil.ToFloat64(
		ExposeValidationMetricsForTesting.ErrorsTotal.WithLabelValues("broken-saga", "UNDEFINED_HANDLER"),
	)
	assert.Equal(t, float64(1), undefinedErrors)
}

func TestRecordValidation_NilResult(_ *testing.T) {
	// Should not panic
	RecordValidation("nil-saga", nil)
}

func TestRecordValidation_ComplexityScoreCapped(t *testing.T) {
	ExposeValidationMetricsForTesting.ValidationTotal.Reset()

	result := &ValidationResult{
		Success: true,
		Errors:  []ValidationError{},
		Metrics: ComplexityMetrics{
			HandlerCallCount: 100, // score = 100/2 = 50, capped at 10
		},
	}

	// Should not panic; complexity score should be capped at 10
	RecordValidation("complex-saga", result)

	successCount := testutil.ToFloat64(
		ExposeValidationMetricsForTesting.ValidationTotal.WithLabelValues("complex-saga", "success"),
	)
	assert.Equal(t, float64(1), successCount)
}

func TestRecordValidation_MultipleCategories(t *testing.T) {
	ExposeValidationMetricsForTesting.ErrorsTotal.Reset()

	result := &ValidationResult{
		Success: false,
		Errors: []ValidationError{
			{Category: CategorySyntax, Message: "parse error"},
			{Category: CategorySyntax, Message: "another parse error"},
			{Category: CategoryRuntime, Message: "runtime failure"},
			{Category: CategoryTypeMismatch, Message: "wrong type"},
		},
		Metrics: ComplexityMetrics{HandlerCallCount: 3},
	}

	RecordValidation("multi-error-saga", result)

	syntaxErrors := testutil.ToFloat64(
		ExposeValidationMetricsForTesting.ErrorsTotal.WithLabelValues("multi-error-saga", "SYNTAX"),
	)
	assert.Equal(t, float64(2), syntaxErrors)

	runtimeErrors := testutil.ToFloat64(
		ExposeValidationMetricsForTesting.ErrorsTotal.WithLabelValues("multi-error-saga", "RUNTIME"),
	)
	assert.Equal(t, float64(1), runtimeErrors)

	typeMismatchErrors := testutil.ToFloat64(
		ExposeValidationMetricsForTesting.ErrorsTotal.WithLabelValues("multi-error-saga", "TYPE_MISMATCH"),
	)
	assert.Equal(t, float64(1), typeMismatchErrors)
}
