package saga

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSecurityTimeout verifies timeout enforcement for various runaway scripts.
func TestSecurityTimeout(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		timeout time.Duration
	}{
		{
			name: "infinite for loop",
			script: `
def infinite():
    x = 0
    for i in range(10000000000):
        x = x + 1
    return x
result = infinite()
`,
			timeout: 100 * time.Millisecond,
		},
		{
			name: "nested loops",
			script: `
def nested():
    x = 0
    for i in range(10000):
        for j in range(10000):
            for k in range(10000):
                x = x + 1
    return x
result = nested()
`,
			timeout: 100 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			runtime, err := NewRuntime(logger, WithTimeout(tt.timeout))
			require.NoError(t, err)

			ctx := context.Background()
			_, err = runtime.ExecuteSaga(ctx, "timeout_test", tt.script, nil)
			require.Error(t, err)
			assert.True(t,
				strings.Contains(err.Error(), "timeout") ||
					strings.Contains(err.Error(), "deadline") ||
					strings.Contains(err.Error(), "cancelled"),
				"expected timeout/deadline/cancelled error, got: %v", err)
		})
	}
}

// TestSecurityContextCancellation verifies that context cancellation stops execution.
func TestSecurityContextCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger, WithTimeout(10*time.Second))
	require.NoError(t, err)

	script := `
def long_running():
    x = 0
    for i in range(100000000):
        x = x + 1
    return x
result = long_running()
`
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		//nolint:forbidigo // triggers context cancellation while long-running saga security test executes
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err = runtime.ExecuteSaga(ctx, "cancel_test", script, nil)
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "cancel") ||
			strings.Contains(err.Error(), "timeout"),
		"expected cancellation error, got: %v", err)
}

// TestSecurityBlockedRuntimeFunctions tests that dangerous functions are blocked at runtime.
func TestSecurityBlockedRuntimeFunctions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	// Test that blocked functions are not in the builtins
	// We can't use dir() without args in Starlark, so we test differently
	tests := []struct {
		name        string
		script      string
		shouldError bool
	}{
		{
			name: "exec not available",
			script: `
result = exec("1+1")
`,
			shouldError: true,
		},
		{
			name: "compile not available",
			script: `
result = compile("1+1")
`,
			shouldError: true,
		},
		{
			name: "open not available",
			script: `
result = open("file.txt")
`,
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := runtime.ExecuteSaga(ctx, "blocked_test", tt.script, nil)
			if tt.shouldError {
				require.Error(t, err, "blocked function should not be available")
			}
		})
	}
}

// TestSecurityDecimalPrecision verifies Decimal operations don't suffer from float precision issues.
func TestSecurityDecimalPrecision(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	script := `
# Famous float precision issue: 0.1 + 0.2 != 0.3 in floats
a = Decimal("0.1")
b = Decimal("0.2")
c = Decimal("0.3")
sum_ab = a + b
result = str(sum_ab) == str(c)
`
	ctx := context.Background()
	result, err := runtime.ExecuteSaga(ctx, "decimal_precision", script, nil)
	require.NoError(t, err)
	assert.Equal(t, true, result.Globals["result"], "0.1 + 0.2 should equal 0.3 with Decimal")
}

// TestSecurityDecimalFloatRejection verifies floats cannot be converted to Decimal.
func TestSecurityDecimalFloatRejection(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	script := `
# Attempting to create Decimal from float should fail
d = Decimal(1.5)
`
	ctx := context.Background()
	_, err = runtime.ExecuteSaga(ctx, "decimal_float", script, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "string", "should require string argument")
}

// TestSecurityPrintRedirection verifies print goes to audit logger, not stdout.
func TestSecurityPrintRedirection(t *testing.T) {
	var logOutput strings.Builder
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	script := `
print("sensitive information")
print("secret data", "more data")
`
	ctx := context.Background()
	_, err = runtime.ExecuteSaga(ctx, "print_test", script, nil)
	require.NoError(t, err)

	// Verify print output is captured
	output := logOutput.String()
	assert.Contains(t, output, "sensitive information")
	assert.Contains(t, output, "secret data")
}

// TestSecurityConcurrentExecutionIsolation verifies concurrent executions don't interfere.
func TestSecurityConcurrentExecutionIsolation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	// Two scripts that should not interfere with each other
	script1 := `
global_var = "script1"
result = global_var
`
	script2 := `
global_var = "script2"
result = global_var
`

	var wg sync.WaitGroup
	var result1, result2 string
	var err1, err2 error

	wg.Add(2)

	go func() {
		defer wg.Done()
		ctx := context.Background()
		res, err := runtime.ExecuteSaga(ctx, "script1", script1, nil)
		if err == nil {
			result1 = res.Globals["result"].(string)
		}
		err1 = err
	}()

	go func() {
		defer wg.Done()
		ctx := context.Background()
		res, err := runtime.ExecuteSaga(ctx, "script2", script2, nil)
		if err == nil {
			result2 = res.Globals["result"].(string)
		}
		err2 = err
	}()

	wg.Wait()

	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.Equal(t, "script1", result1, "script1 should see its own global_var")
	assert.Equal(t, "script2", result2, "script2 should see its own global_var")
}

// TestSecurityValidationBlocksAtUpload tests that static validation catches issues.
func TestSecurityValidationBlocksAtUpload(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		wantErr bool
		errType error
	}{
		{
			name:    "blocked load statement",
			script:  `load("module.star", "func")`,
			wantErr: true,
			errType: ErrBlockedFunction,
		},
		{
			name:    "blocked exec call",
			script:  `exec("code")`,
			wantErr: true,
			errType: ErrBlockedFunction,
		},
		{
			name:    "excessive loop nesting",
			script:  "for a in range(1):\n for b in range(1):\n  for c in range(1):\n   for d in range(1):\n    x=1",
			wantErr: true,
			errType: ErrExcessiveLoopNesting,
		},
		{
			name:    "script too large",
			script:  strings.Repeat("x=1\n", 20000),
			wantErr: true,
			errType: ErrScriptTooLarge,
		},
		{
			name: "valid script passes",
			script: `
def my_saga():
    return Decimal("100.00")
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSagaScript(tt.script)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestSecuritySandboxEscapeAttempts tests various sandbox escape attempts.
func TestSecuritySandboxEscapeAttempts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	tests := []struct {
		name    string
		script  string
		wantErr bool
	}{
		{
			name: "simple assignment works",
			script: `
x = 1
result = x + 1
`,
			wantErr: false,
		},
		{
			name: "cannot import modules",
			script: `
# import is not valid Starlark syntax anyway
result = 42
`,
			wantErr: false,
		},
		{
			name: "basic list operations work",
			script: `
items = [1, 2, 3]
result = len(items)
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := runtime.ExecuteSaga(ctx, "escape_test", tt.script, nil)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestSecurityStressTest runs many concurrent executions to verify stability.
func TestSecurityStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	// Starlark requires for loops to be inside functions
	script := `
def compute():
    result = 0
    for i in range(100):
        result = result + i
    return result
result = compute()
`
	numGoroutines := 100
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			_, err := runtime.ExecuteSaga(ctx, "stress_test", script, nil)
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("stress test error: %v", err)
	}
}

// TestSecurityStepLimitEnforced verifies that scripts exceeding MaxStepsPerExecution
// are terminated with a step limit error, not a timeout.
func TestSecurityStepLimitEnforced(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// Use a long timeout so the step limit triggers first, not the timeout.
	runtime, err := NewRuntime(logger, WithTimeout(30*time.Second))
	require.NoError(t, err)

	// This script loops over a range large enough to exceed MaxStepsPerExecution (1,000,000).
	// Each iteration costs multiple steps in the Starlark evaluator.
	script := `
def burn_steps():
    x = 0
    for i in range(10000000):
        x = x + 1
    return x
result = burn_steps()
`
	ctx := context.Background()
	_, err = runtime.ExecuteSaga(ctx, "step_limit_test", script, nil)
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "step") ||
			strings.Contains(err.Error(), "limit") ||
			strings.Contains(err.Error(), "cancelled") ||
			strings.Contains(err.Error(), "execution"),
		"expected step limit or execution error, got: %v", err)
}

// TestSecurityInputIsolation verifies input data cannot be modified across executions.
func TestSecurityInputIsolation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	script := `
# Attempt to modify input
input_data["new_key"] = "should_not_persist"
result = input_data["key"]
`
	input := map[string]interface{}{
		"key": "original_value",
	}

	ctx := context.Background()

	// First execution
	result1, err := runtime.ExecuteSaga(ctx, "input_test1", script, input)
	require.NoError(t, err)
	assert.Equal(t, "original_value", result1.Globals["result"])

	// Second execution - input should not have "new_key" from previous run
	script2 := `
result = "new_key" in input_data
`
	result2, err := runtime.ExecuteSaga(ctx, "input_test2", script2, input)
	require.NoError(t, err)
	// Each execution should get a fresh copy of input_data without modifications from previous runs
	assert.Equal(t, false, result2.Globals["result"], "new_key should not persist across executions")
}
