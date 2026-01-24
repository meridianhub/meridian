package saga

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

func TestRestrictedBuiltins_WhitelistedFunctions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	builtins := NewRestrictedBuiltins(logger)

	// Check that required DSL functions are present
	expectedFunctions := []string{
		"Decimal",
		"saga",
		"step",
		"posting",
		"cel_eval",
		"resolve_account",
		"resolve_instrument",
		"invoke_saga",
		"fail",
		"log",
	}

	for _, fn := range expectedFunctions {
		_, ok := builtins[fn]
		assert.True(t, ok, "expected builtin %q to be present", fn)
	}
}

func TestRestrictedBuiltins_SafeStdlib(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	builtins := NewRestrictedBuiltins(logger)

	// Check that safe stdlib functions are available
	safeFunctions := []string{
		"True",
		"False",
		"None",
		"len",
		"str",
		"int",
		"list",
		"dict",
		"range",
		"enumerate",
		"zip",
		"sorted",
		"reversed",
		"min",
		"max",
		"abs",
		"bool",
		"hasattr",
		"getattr",
		"type",
		"repr",
		"hash",
	}

	for _, fn := range safeFunctions {
		_, ok := builtins[fn]
		assert.True(t, ok, "expected safe builtin %q to be present", fn)
	}
}

func TestRestrictedBuiltins_BlockedFunctions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	builtins := NewRestrictedBuiltins(logger)

	// These dangerous functions must be absent
	blockedFunctions := []string{
		"load",       // File loading
		"exec",       // Code execution
		"compile",    // Code compilation
		"open",       // File system access
		"http",       // Network access
		"getenv",     // Environment variables
		"setenv",     // Environment modification
		"__import__", // Module import
	}

	for _, fn := range blockedFunctions {
		_, ok := builtins[fn]
		assert.False(t, ok, "blocked builtin %q should not be present", fn)
	}
}

func TestRestrictedBuiltins_DecimalFunction(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	builtins := NewRestrictedBuiltins(logger)

	thread := &starlark.Thread{Name: "test"}
	decimalFn, ok := builtins["Decimal"].(*starlark.Builtin)
	require.True(t, ok)

	// Should create Decimal from string
	val, err := starlark.Call(thread, decimalFn, starlark.Tuple{starlark.String("123.45")}, nil)
	require.NoError(t, err)
	_, ok = val.(*DecimalValue)
	assert.True(t, ok)
}

func TestRestrictedBuiltins_SagaFunction(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	builtins := NewRestrictedBuiltins(logger)

	thread := &starlark.Thread{Name: "test"}
	sagaFn, ok := builtins["saga"].(*starlark.Builtin)
	require.True(t, ok)

	// Should be callable with name argument
	val, err := starlark.Call(thread, sagaFn, starlark.Tuple{starlark.String("my_saga")}, nil)
	require.NoError(t, err)
	require.NotNil(t, val)
}

func TestRestrictedBuiltins_LogFunction(t *testing.T) {
	var logOutput strings.Builder
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	builtins := NewRestrictedBuiltins(logger)

	thread := &starlark.Thread{Name: "test"}
	logFn, ok := builtins["log"].(*starlark.Builtin)
	require.True(t, ok)

	// Should log to the provided logger
	_, err := starlark.Call(thread, logFn, starlark.Tuple{starlark.String("test message")}, nil)
	require.NoError(t, err)

	assert.Contains(t, logOutput.String(), "test message")
}

func TestRestrictedBuiltins_FailFunction(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	builtins := NewRestrictedBuiltins(logger)

	thread := &starlark.Thread{Name: "test"}
	failFn, ok := builtins["fail"].(*starlark.Builtin)
	require.True(t, ok)

	// Should return an error value
	_, err := starlark.Call(thread, failFn, starlark.Tuple{starlark.String("intentional failure")}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "intentional failure")
}

func TestRestrictedBuiltins_StepFunction(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	builtins := NewRestrictedBuiltins(logger)

	thread := &starlark.Thread{Name: "test"}
	stepFn, ok := builtins["step"].(*starlark.Builtin)
	require.True(t, ok)

	// Should be callable with name argument
	val, err := starlark.Call(thread, stepFn, starlark.Tuple{starlark.String("step1")}, nil)
	require.NoError(t, err)
	require.NotNil(t, val)
}

func TestRestrictedBuiltins_PrintRedirection(t *testing.T) {
	var logOutput strings.Builder
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	// Script that uses print
	script := `
print("hello from script")
`
	ctx := context.Background()
	_, err = runtime.ExecuteSaga(ctx, "print_test", script, nil)
	// Execution should complete (print is allowed but redirected)
	require.NoError(t, err)

	// Print should be captured in logs
	assert.Contains(t, logOutput.String(), "hello from script")
}

func TestRestrictedBuiltins_PostingFunction(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	builtins := NewRestrictedBuiltins(logger)

	thread := &starlark.Thread{Name: "test"}
	postingFn, ok := builtins["posting"].(*starlark.Builtin)
	require.True(t, ok)

	// Should be callable
	val, err := starlark.Call(thread, postingFn, starlark.Tuple{
		starlark.String("debit_account"),
		starlark.String("credit_account"),
		starlark.String("100.00"),
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, val)
}

func TestRestrictedBuiltins_IntegrationWithRuntime(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	// Script that uses allowed builtins
	script := `
result = len([1, 2, 3])
name = str(42)
items = list(range(5))
`
	ctx := context.Background()
	result, err := runtime.ExecuteSaga(ctx, "builtin_test", script, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, int64(3), result.Globals["result"])
	assert.Equal(t, "42", result.Globals["name"])
}
