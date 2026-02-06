package validation

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHumanReadableFormatter_Success(t *testing.T) {
	formatter := &HumanReadableFormatter{}

	result := &ValidationResult{
		Success: true,
		Errors:  []ValidationError{},
		Metrics: ComplexityMetrics{
			HandlerCallCount:  3,
			EstimatedDuration: 30 * time.Millisecond,
		},
	}

	output := formatter.Format(result)

	assert.Contains(t, output, "✅", "should contain success checkmark")
	assert.Contains(t, output, "3 handlers called", "should show handler count")
	assert.Contains(t, output, "Complexity: 1/10 (Low)", "should show complexity score")
	assert.Contains(t, output, "<30ms", "should show estimated duration")
	assert.Contains(t, output, "Script ready for deployment", "should show success message")
}

func TestHumanReadableFormatter_SyntaxError(t *testing.T) {
	formatter := &HumanReadableFormatter{}

	result := &ValidationResult{
		Success: false,
		Errors: []ValidationError{
			{
				Line:     42,
				Column:   10,
				Message:  "unexpected token: )",
				Category: CategorySyntax,
			},
		},
		Metrics: ComplexityMetrics{},
	}

	output := formatter.Format(result)

	assert.Contains(t, output, "❌", "should contain failure X")
	assert.Contains(t, output, "Line 42", "should show line number")
	assert.Contains(t, output, "unexpected token: )", "should show error message")
	assert.Contains(t, output, "Script rejected", "should show rejection message")
}

func TestHumanReadableFormatter_UndefinedHandler(t *testing.T) {
	formatter := &HumanReadableFormatter{
		AvailableHandlers: []string{
			"position_keeping.create_lien",
			"position_keeping.release_lien",
			"financial_accounting.post_entry",
		},
	}

	result := &ValidationResult{
		Success: false,
		Errors: []ValidationError{
			{
				Line:     42,
				Column:   10,
				Message:  "handler 'payment_order.create_lien' not found in registry",
				Category: CategoryUndefinedHandler,
			},
		},
		Metrics: ComplexityMetrics{},
	}

	output := formatter.Format(result)

	assert.Contains(t, output, "❌", "should contain failure X")
	assert.Contains(t, output, "Line 42", "should show line number")
	assert.Contains(t, output, "payment_order.create_lien", "should show handler name")
	// Should suggest position_keeping.create_lien (Levenshtein distance)
	assert.Contains(t, output, "position_keeping.create_lien", "should suggest similar handler")
}

func TestHumanReadableFormatter_TypeMismatch(t *testing.T) {
	formatter := &HumanReadableFormatter{}

	result := &ValidationResult{
		Success: false,
		Errors: []ValidationError{
			{
				Line:     58,
				Column:   5,
				Message:  "type mismatch for parameter 'amount': expected Decimal, got String",
				Category: CategoryTypeMismatch,
			},
		},
		Metrics: ComplexityMetrics{},
	}

	output := formatter.Format(result)

	assert.Contains(t, output, "❌", "should contain failure X")
	assert.Contains(t, output, "Line 58", "should show line number")
	assert.Contains(t, output, "type mismatch", "should show error message")
	assert.Contains(t, output, "expected Decimal, got String", "should show type info")
}

func TestHumanReadableFormatter_MultipleErrors(t *testing.T) {
	formatter := &HumanReadableFormatter{}

	result := &ValidationResult{
		Success: false,
		Errors: []ValidationError{
			{
				Line:     10,
				Column:   5,
				Message:  "syntax error: unexpected token",
				Category: CategorySyntax,
			},
			{
				Line:     20,
				Column:   15,
				Message:  "undefined handler 'foo.bar'",
				Category: CategoryUndefinedHandler,
			},
			{
				Line:     30,
				Column:   8,
				Message:  "type mismatch for parameter 'x'",
				Category: CategoryTypeMismatch,
			},
		},
		Metrics: ComplexityMetrics{},
	}

	output := formatter.Format(result)

	assert.Contains(t, output, "❌", "should contain failure X")
	assert.Contains(t, output, "Line 10", "should show first error")
	assert.Contains(t, output, "Line 20", "should show second error")
	assert.Contains(t, output, "Line 30", "should show third error")
	assert.Contains(t, output, "3 errors", "should count errors")
}

func TestHumanReadableFormatter_NoColorInCI(t *testing.T) {
	// Set CI environment variable
	originalCI := os.Getenv("CI")
	os.Setenv("CI", "true")
	defer os.Setenv("CI", originalCI)

	formatter := &HumanReadableFormatter{}

	result := &ValidationResult{
		Success: true,
		Errors:  []ValidationError{},
		Metrics: ComplexityMetrics{
			HandlerCallCount: 1,
		},
	}

	output := formatter.Format(result)

	// Should not contain ANSI color codes
	assert.NotContains(t, output, "\033[", "should not contain ANSI escape codes in CI")
}

func TestHumanReadableFormatter_ComplexityLabels(t *testing.T) {
	testCases := []struct {
		name          string
		handlerCount  int
		expectedScore int
		expectedLabel string
	}{
		{"zero handlers", 0, 0, "Low"},
		{"one handler", 1, 0, "Low"},
		{"two handlers", 2, 1, "Low"},
		{"six handlers", 6, 3, "Low"},
		{"eight handlers", 8, 4, "Medium"},
		{"twelve handlers", 12, 6, "Medium"},
		{"fourteen handlers", 14, 7, "High"},
		{"twenty handlers", 20, 10, "High"},
		{"thirty handlers", 30, 10, "High"}, // capped at 10
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			formatter := &HumanReadableFormatter{}
			result := &ValidationResult{
				Success: true,
				Errors:  []ValidationError{},
				Metrics: ComplexityMetrics{
					HandlerCallCount: tc.handlerCount,
				},
			}

			output := formatter.Format(result)

			expectedComplexity := tc.expectedScore
			assert.Contains(t, output, "Complexity: ", "should show complexity")
			assert.Contains(t, output, tc.expectedLabel, "should show correct label")

			// Verify score calculation
			if tc.handlerCount > 0 {
				expectedScoreStr := fmt.Sprintf("Complexity: %d/10", expectedComplexity)
				assert.Contains(t, output, expectedScoreStr, "should show correct score")
			}
		})
	}
}

func TestLevenshteinDistance(t *testing.T) {
	testCases := []struct {
		a        string
		b        string
		expected int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "adc", 1},
		{"create_lien", "create_lein", 2}, // transposition counts as 2 edits
		{"payment_order.create_lien", "position_keeping.create_lien", 14},
		{"foo", "bar", 3},
		{"kitten", "sitting", 3},
	}

	for _, tc := range testCases {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			distance := levenshteinDistance(tc.a, tc.b)
			assert.Equal(t, tc.expected, distance, "distance should match")
		})
	}
}

func TestJSONFormatter_Success(t *testing.T) {
	formatter := &JSONFormatter{}

	result := &ValidationResult{
		Success: true,
		Errors:  []ValidationError{},
		Metrics: ComplexityMetrics{
			HandlerCallCount:  3,
			OperationCount:    10,
			EstimatedDuration: 30 * time.Millisecond,
		},
	}

	output := formatter.Format(result)

	var report JSONReport
	err := json.Unmarshal([]byte(output), &report)
	require.NoError(t, err, "should unmarshal JSON")

	assert.True(t, report.Success, "success should be true")
	assert.Empty(t, report.Errors, "errors should be empty")
	assert.Equal(t, 3, report.Metrics.HandlerCallCount, "handler count should match")
	assert.Equal(t, 1, report.Metrics.ComplexityScore, "complexity score should match")
	assert.Equal(t, 30, report.Metrics.EstimatedDurationMs, "duration should match")
}

func TestJSONFormatter_SyntaxError(t *testing.T) {
	formatter := &JSONFormatter{}

	result := &ValidationResult{
		Success: false,
		Errors: []ValidationError{
			{
				Line:     42,
				Column:   10,
				Message:  "unexpected token: )",
				Category: CategorySyntax,
			},
		},
		Metrics: ComplexityMetrics{},
	}

	output := formatter.Format(result)

	var report JSONReport
	err := json.Unmarshal([]byte(output), &report)
	require.NoError(t, err, "should unmarshal JSON")

	assert.False(t, report.Success, "success should be false")
	require.Len(t, report.Errors, 1, "should have one error")

	jsonErr := report.Errors[0]
	assert.Equal(t, 42, jsonErr.Line, "line should match")
	assert.Equal(t, 10, jsonErr.Column, "column should match")
	assert.Equal(t, "unexpected token: )", jsonErr.Message, "message should match")
	assert.Equal(t, "SYNTAX", jsonErr.Category, "category should match")
	assert.Empty(t, jsonErr.Suggestion, "suggestion should be empty for syntax errors")
}

func TestJSONFormatter_UndefinedHandler(t *testing.T) {
	formatter := &JSONFormatter{
		AvailableHandlers: []string{
			"position_keeping.create_lien",
			"position_keeping.release_lien",
		},
	}

	result := &ValidationResult{
		Success: false,
		Errors: []ValidationError{
			{
				Line:     42,
				Column:   10,
				Message:  "handler 'payment_order.create_lien' not found",
				Category: CategoryUndefinedHandler,
			},
		},
		Metrics: ComplexityMetrics{},
	}

	output := formatter.Format(result)

	var report JSONReport
	err := json.Unmarshal([]byte(output), &report)
	require.NoError(t, err, "should unmarshal JSON")

	assert.False(t, report.Success, "success should be false")
	require.Len(t, report.Errors, 1, "should have one error")

	jsonErr := report.Errors[0]
	assert.Equal(t, "UNDEFINED_HANDLER", jsonErr.Category, "category should match")
	// Should suggest position_keeping.create_lien
	assert.NotEmpty(t, jsonErr.Suggestion, "suggestion should be populated")
	assert.Contains(t, jsonErr.Suggestion, "position_keeping.create_lien", "should suggest similar handler")
}

func TestJSONFormatter_TypeMismatch(t *testing.T) {
	formatter := &JSONFormatter{}

	result := &ValidationResult{
		Success: false,
		Errors: []ValidationError{
			{
				Line:     58,
				Column:   5,
				Message:  "type mismatch: expected Decimal, got String",
				Category: CategoryTypeMismatch,
			},
		},
		Metrics: ComplexityMetrics{},
	}

	output := formatter.Format(result)

	var report JSONReport
	err := json.Unmarshal([]byte(output), &report)
	require.NoError(t, err, "should unmarshal JSON")

	assert.False(t, report.Success, "success should be false")
	require.Len(t, report.Errors, 1, "should have one error")

	jsonErr := report.Errors[0]
	assert.Equal(t, "TYPE_MISMATCH", jsonErr.Category, "category should match")
	assert.Contains(t, jsonErr.Message, "expected Decimal, got String", "message should contain type info")
}

func TestJSONFormatter_MultipleErrors(t *testing.T) {
	formatter := &JSONFormatter{}

	result := &ValidationResult{
		Success: false,
		Errors: []ValidationError{
			{Line: 10, Column: 5, Message: "error 1", Category: CategorySyntax},
			{Line: 20, Column: 15, Message: "error 2", Category: CategoryUndefinedHandler},
			{Line: 30, Column: 8, Message: "error 3", Category: CategoryTypeMismatch},
		},
		Metrics: ComplexityMetrics{},
	}

	output := formatter.Format(result)

	var report JSONReport
	err := json.Unmarshal([]byte(output), &report)
	require.NoError(t, err, "should unmarshal JSON")

	assert.False(t, report.Success, "success should be false")
	assert.Len(t, report.Errors, 3, "should have three errors")
}

func TestJSONFormatter_ComplexityScore(t *testing.T) {
	testCases := []struct {
		handlerCount  int
		expectedScore int
	}{
		{0, 0},
		{2, 1},
		{5, 2},
		{10, 5},
		{20, 10},
		{30, 10}, // capped at 10
	}

	for _, tc := range testCases {
		formatter := &JSONFormatter{}
		result := &ValidationResult{
			Success: true,
			Errors:  []ValidationError{},
			Metrics: ComplexityMetrics{
				HandlerCallCount: tc.handlerCount,
			},
		}

		output := formatter.Format(result)

		var report JSONReport
		err := json.Unmarshal([]byte(output), &report)
		require.NoError(t, err, "should unmarshal JSON")

		assert.Equal(t, tc.expectedScore, report.Metrics.ComplexityScore, "complexity score should match for %d handlers", tc.handlerCount)
	}
}
