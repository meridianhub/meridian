package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga/validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeScript writes a Starlark script to a temp file and returns the path.
func writeScript(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// testHandlersYAML returns a minimal handler schema YAML for CLI validation tests.
var testHandlersYAML = `
service: test
version: "1.0"
handlers:
  position_keeping.initiate_log:
    description: "Initiate a position log entry"
    compensation_strategy: auto
    compensate: position_keeping.cancel_log
    params:
      position_id:
        type: string
        required: true
      amount:
        type: Decimal
        required: true
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
    returns:
      log_id:
        type: string
      status:
        type: string
  position_keeping.cancel_log:
    description: "Cancel a position log entry"
    compensation_strategy: none
    params:
      log_id:
        type: string
        required: true
    returns:
      status:
        type: string
`

// writeTestHandlersYAML writes the test schema to a temp file and returns the path.
func writeTestHandlersYAML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "handlers.yaml")
	require.NoError(t, os.WriteFile(path, []byte(testHandlersYAML), 0o644))
	return path
}

func TestRunValidateCommand_ValidScript(t *testing.T) {
	handlersPath := writeTestHandlersYAML(t)

	script := writeScript(t, "valid.star", `
result = position_keeping.initiate_log(
    position_id="POS-001",
    direction="CREDIT",
    amount=Decimal("100.00"),
)
`)

	result, err := runValidateLogic(script, handlersPath)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Empty(t, result.Errors)
	assert.Greater(t, result.Metrics.HandlerCallCount, 0)
}

func TestRunValidateCommand_InvalidScript(t *testing.T) {
	handlersPath := writeTestHandlersYAML(t)

	script := writeScript(t, "invalid.star", `
result = position_keeping.initiate_log(account_id="ACC-001"
`)

	result, err := runValidateLogic(script, handlersPath)
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.NotEmpty(t, result.Errors)
	assert.Equal(t, validation.CategorySyntax, result.Errors[0].Category)
	assert.Greater(t, result.Errors[0].Line, 0, "Expected line number in error")
}

func TestRunValidateCommand_MissingFile(t *testing.T) {
	_, err := runValidateLogic("/nonexistent/path/missing.star", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read script")
}

func TestRunValidateCommand_JSONOutput(t *testing.T) {
	handlersPath := writeTestHandlersYAML(t)

	script := writeScript(t, "valid.star", `
result = position_keeping.initiate_log(
    position_id="POS-001",
    direction="CREDIT",
    amount=Decimal("100.00"),
)
`)

	result, err := runValidateLogic(script, handlersPath)
	require.NoError(t, err)

	// Format as JSON and verify it parses
	formatter := &validation.JSONFormatter{
		AvailableHandlers: []string{},
	}
	jsonStr := formatter.Format(result)

	var report validation.JSONReport
	require.NoError(t, json.Unmarshal([]byte(jsonStr), &report))
	assert.True(t, report.Success)
}

func TestRunValidateCommand_ComplexityMetrics(t *testing.T) {
	handlersPath := writeTestHandlersYAML(t)

	script := writeScript(t, "multi_handler.star", `
r1 = position_keeping.initiate_log(
    position_id="POS-001",
    direction="CREDIT",
    amount=Decimal("100.00"),
)
r2 = position_keeping.initiate_log(
    position_id="POS-002",
    direction="DEBIT",
    amount=Decimal("50.00"),
)
`)

	result, err := runValidateLogic(script, handlersPath)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, 2, result.Metrics.HandlerCallCount)
	assert.Greater(t, result.Metrics.OperationCount, 0)
}

func TestRunValidateCommand_RelativePath(t *testing.T) {
	handlersPath := writeTestHandlersYAML(t)

	// Create script in the current working directory to test relative path resolution
	wd, err := os.Getwd()
	require.NoError(t, err)

	scriptPath := filepath.Join(wd, "test_relative.star")
	require.NoError(t, os.WriteFile(scriptPath, []byte(`
result = position_keeping.initiate_log(
    position_id="POS-001",
    direction="CREDIT",
    amount=Decimal("100.00"),
)
`), 0o644))
	t.Cleanup(func() { os.Remove(scriptPath) })

	// Use relative path (just the filename since we're in wd)
	result, err := runValidateLogic("test_relative.star", handlersPath)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestRunValidateCommand_AbsolutePath(t *testing.T) {
	handlersPath := writeTestHandlersYAML(t)

	script := writeScript(t, "absolute.star", `
result = position_keeping.initiate_log(
    position_id="POS-001",
    direction="CREDIT",
    amount=Decimal("100.00"),
)
`)

	// Ensure the path is absolute
	absPath, err := filepath.Abs(script)
	require.NoError(t, err)

	result, err := runValidateLogic(absPath, handlersPath)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestRunValidateCommand_ErrorWithLineNumber(t *testing.T) {
	handlersPath := writeTestHandlersYAML(t)

	// Script with syntax error on line 3
	script := writeScript(t, "line_error.star", `x = 1
y = 2
z = (
`)

	result, err := runValidateLogic(script, handlersPath)
	require.NoError(t, err)
	assert.False(t, result.Success)
	require.NotEmpty(t, result.Errors)
	assert.Greater(t, result.Errors[0].Line, 0, "Expected line number > 0")
}

func TestRunValidateCommand_UndefinedHandler(t *testing.T) {
	handlersPath := writeTestHandlersYAML(t)

	script := writeScript(t, "undefined.star", `
result = nonexistent_service.some_method(param="value")
`)

	result, err := runValidateLogic(script, handlersPath)
	require.NoError(t, err)
	assert.False(t, result.Success)
	require.NotEmpty(t, result.Errors)
}

func TestFormatOutput_HumanReadable(t *testing.T) {
	result := &validation.ValidationResult{
		Success: true,
		Errors:  []validation.ValidationError{},
		Metrics: validation.ComplexityMetrics{
			HandlerCallCount: 3,
			OperationCount:   5,
		},
	}

	output := formatOutput(result, false, []string{"position_keeping.initiate_log"})
	assert.Contains(t, output, "Validation Passed")
	assert.Contains(t, output, "3 handlers called")
}

func TestFormatOutput_JSON(t *testing.T) {
	result := &validation.ValidationResult{
		Success: true,
		Errors:  []validation.ValidationError{},
		Metrics: validation.ComplexityMetrics{
			HandlerCallCount: 2,
		},
	}

	output := formatOutput(result, true, []string{})
	var report validation.JSONReport
	require.NoError(t, json.Unmarshal([]byte(output), &report))
	assert.True(t, report.Success)
	assert.Equal(t, 2, report.Metrics.HandlerCallCount)
}

func TestFormatOutput_FailedHumanReadable(t *testing.T) {
	result := &validation.ValidationResult{
		Success: false,
		Errors: []validation.ValidationError{
			{
				Line:     42,
				Column:   5,
				Message:  "handler 'payment_order.create_lien' not found in registry",
				Category: validation.CategoryUndefinedHandler,
			},
		},
	}

	output := formatOutput(result, false, []string{"position_keeping.create_lien"})
	assert.Contains(t, output, "Validation Failed")
	assert.Contains(t, output, "Line 42")
}
