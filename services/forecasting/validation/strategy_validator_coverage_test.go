package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ValidateWithExecution edge cases ---

func TestValidateWithExecution_StaticValidationFails_ReturnsEarly(t *testing.T) {
	// Empty script fails static validation, should not reach execution
	result := ValidateWithExecution("")
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0].Message, "empty")
}

func TestValidateWithExecution_ComputeForecastIsVariable(t *testing.T) {
	// compute_forecast exists as a global variable after execution, but is not a function.
	// Static analysis catches this as "missing required function" because there's no def.
	// The execution path handles the case where it passes static check but isn't callable.
	script := `
compute_forecast = 42
`
	result := ValidateWithExecution(script)
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
}

func TestValidateWithExecution_ForbiddenImportFailsStatically(t *testing.T) {
	script := `
import os
def compute_forecast(ctx):
    return []
`
	result := ValidateWithExecution(script)
	assert.False(t, result.Valid)
}

func TestValidateWithExecution_ScriptWithHelperFunctions(t *testing.T) {
	script := `
def helper(x):
    return x * 2

def compute_forecast(ctx):
    return []
`
	result := ValidateWithExecution(script)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
}

func TestValidateWithExecution_UndefinedVariableAtTopLevel(t *testing.T) {
	// References an undefined variable at top level, should fail during execution
	script := `
x = undefined_var + 1

def compute_forecast(ctx):
    return []
`
	result := ValidateWithExecution(script)
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0].Message, "execution error")
}

// --- addSyntaxError edge cases ---

func TestValidateStrategy_SyntaxErrorWithUnexpected(t *testing.T) {
	// Trigger "unexpected" in error message for the suggestion branch
	script := `
def compute_forecast(ctx):
    return [}]
`
	result := ValidateStrategy(script)
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	// The error should have a suggestion about unmatched parentheses/brackets
	assert.NotEmpty(t, result.Errors[0].Suggestion)
}

func TestValidateWithExecution_DefOverwrittenToNonFunction(t *testing.T) {
	// Define compute_forecast as a function (passes static check) but then
	// reassign it to an integer at top level, making it a non-function at runtime.
	script := `
def compute_forecast(ctx):
    return []

compute_forecast = 42
`
	result := ValidateWithExecution(script)
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	// Should detect that compute_forecast is not a function
	assert.Contains(t, result.Errors[0].Message, "compute_forecast")
}

func TestValidateWithExecution_DefDeletedAtRuntime(t *testing.T) {
	// Define compute_forecast as a function (passes static check) but conditionally
	// make it unavailable at runtime by reassigning in the same scope.
	script := `
def compute_forecast(ctx):
    return []

# Reassign to None - still a global, but not a function
compute_forecast = None
`
	result := ValidateWithExecution(script)
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
}

func TestValidateWithExecution_WithStubBuiltins(t *testing.T) {
	// Verify that stub builtins (avg, sum, etc.) don't cause errors during validation
	script := `
x = avg([1, 2, 3])
y = sum([1, 2, 3])

def compute_forecast(ctx):
    return []
`
	result := ValidateWithExecution(script)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
}

func TestValidateStrategy_SyntaxErrorWithNewline(t *testing.T) {
	// Trigger "got newline" for the colon suggestion branch
	script := `
def compute_forecast(ctx)
    return []
`
	result := ValidateStrategy(script)
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	// Check that the newline-specific suggestion is generated
	foundNewlineSuggestion := false
	for _, e := range result.Errors {
		if e.Suggestion != "" {
			foundNewlineSuggestion = true
		}
	}
	assert.True(t, foundNewlineSuggestion)
}
