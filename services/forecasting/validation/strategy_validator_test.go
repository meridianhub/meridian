package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/forecasting/templates"
)

func TestValidateStrategy_ValidScript(t *testing.T) {
	script := `
def compute_forecast(ctx):
    obs = ctx["observations"]
    return [{"timestamp": "2026-02-10T01:00:00Z", "value": 42}]
`
	result := ValidateStrategy(script)

	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
	assert.NotEmpty(t, result.AvailableContextFields)
	assert.NotEmpty(t, result.AvailableFunctions)
}

func TestValidateStrategy_MissingComputeForecast(t *testing.T) {
	script := `
def some_other_function(x):
    return x * 2
`
	result := ValidateStrategy(script)

	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Message, "missing required function: compute_forecast")
	assert.NotEmpty(t, result.Errors[0].Suggestion)
	assert.Contains(t, result.Errors[0].Suggestion, "def compute_forecast(ctx)")
}

func TestValidateStrategy_SyntaxError(t *testing.T) {
	script := `
def compute_forecast(ctx)
    return []
`
	result := ValidateStrategy(script)

	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)

	// Should have line/column information
	syntaxErr := result.Errors[0]
	assert.Greater(t, syntaxErr.Line, 0, "expected line number > 0")
	assert.NotEmpty(t, syntaxErr.Message)
	assert.NotEmpty(t, syntaxErr.Suggestion)
}

func TestValidateStrategy_ForbiddenWhileLoop(t *testing.T) {
	// Note: Starlark itself forbids while loops at the syntax level.
	// The script won't parse if it contains a while loop.
	script := `
while True:
    pass
`
	result := ValidateStrategy(script)

	// Should fail with a syntax error (Starlark rejects while loops)
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
}

func TestValidateStrategy_ForbiddenImport(t *testing.T) {
	script := `
import os

def compute_forecast(ctx):
    return []
`
	result := ValidateStrategy(script)

	assert.False(t, result.Valid)
	foundImportError := false
	for _, e := range result.Errors {
		if e.Message != "" {
			foundImportError = true
			break
		}
	}
	assert.True(t, foundImportError, "expected at least one error about import")
}

func TestValidateStrategy_ForbiddenLoad(t *testing.T) {
	script := `
load("module.star", "func")

def compute_forecast(ctx):
    return []
`
	result := ValidateStrategy(script)

	assert.False(t, result.Valid)
	foundLoadError := false
	for _, e := range result.Errors {
		if e.Message != "" {
			foundLoadError = true
			break
		}
	}
	assert.True(t, foundLoadError, "expected at least one error about load")
}

func TestValidateStrategy_EmptyScript(t *testing.T) {
	result := ValidateStrategy("")

	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0].Message, "empty")
}

func TestValidateStrategy_WhitespaceOnlyScript(t *testing.T) {
	result := ValidateStrategy("   \n\t\n  ")

	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0].Message, "empty")
}

func TestValidateStrategy_MissingCtxParam(t *testing.T) {
	script := `
def compute_forecast():
    return []
`
	result := ValidateStrategy(script)

	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0].Message, "ctx")
}

func TestValidateStrategy_WrongParamName(t *testing.T) {
	script := `
def compute_forecast(data):
    return []
`
	result := ValidateStrategy(script)

	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0].Message, "ctx")
}

func TestValidateStrategy_AIFeedbackStructure(t *testing.T) {
	// Verify that the result always includes available fields and functions
	// for AI self-correction, regardless of validity
	script := `x = 42`

	result := ValidateStrategy(script)

	assert.False(t, result.Valid)
	assert.Contains(t, result.AvailableContextFields, "observations")
	assert.Contains(t, result.AvailableContextFields, "reference_data")
	assert.Contains(t, result.AvailableContextFields, "horizon_seconds")
	assert.Contains(t, result.AvailableContextFields, "granularity_seconds")
	assert.Contains(t, result.AvailableContextFields, "now")

	assert.Contains(t, result.AvailableFunctions, "avg")
	assert.Contains(t, result.AvailableFunctions, "sum")
	assert.Contains(t, result.AvailableFunctions, "filter_by_hour")
	assert.Contains(t, result.AvailableFunctions, "group_by_hour")
	assert.Contains(t, result.AvailableFunctions, "duration")
	assert.Contains(t, result.AvailableFunctions, "add_seconds")
	assert.Contains(t, result.AvailableFunctions, "Decimal")
}

// --- ValidateWithExecution tests ---

func TestValidateWithExecution_ValidScript(t *testing.T) {
	script := `
def compute_forecast(ctx):
    return []
`
	result := ValidateWithExecution(script)

	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
}

func TestValidateWithExecution_RuntimeError(t *testing.T) {
	script := `
x = 1 / 0

def compute_forecast(ctx):
    return []
`
	result := ValidateWithExecution(script)

	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0].Message, "execution error")
}

func TestValidateWithExecution_NotAFunction(t *testing.T) {
	// compute_forecast assigned as variable, not defined as function.
	// Static analysis catches this as "missing required function" because
	// there is no def statement for compute_forecast.
	script := `
compute_forecast = 42
`
	result := ValidateWithExecution(script)

	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0].Message, "compute_forecast")
}

// --- Built-in template validation tests ---

func TestValidateStrategy_AllBuiltinTemplatesPass(t *testing.T) {
	for _, name := range templates.All() {
		t.Run(name, func(t *testing.T) {
			script, err := templates.Load(name)
			require.NoError(t, err)

			result := ValidateStrategy(script)
			assert.True(t, result.Valid, "template %s should be valid, errors: %v", name, result.Errors)
		})
	}
}

func TestValidateWithExecution_AllBuiltinTemplatesPass(t *testing.T) {
	for _, name := range templates.All() {
		t.Run(name, func(t *testing.T) {
			script, err := templates.Load(name)
			require.NoError(t, err)

			result := ValidateWithExecution(script)
			assert.True(t, result.Valid, "template %s should pass execution validation, errors: %v", name, result.Errors)
		})
	}
}
