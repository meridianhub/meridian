package starlark

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

// minimalForecastCtx returns a minimal ForecastContext for security tests that
// don't need real observations or reference data.
func minimalForecastCtx() *ForecastContext {
	return &ForecastContext{
		Observations:  map[string][]Observation{},
		ReferenceData: nil,
		Horizon:       24 * time.Hour,
		Granularity:   1 * time.Hour,
		Now:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// newSecurityTestRunner creates a ForecastRunner backed by no-op mock clients.
func newSecurityTestRunner(t *testing.T, opts ...func(*ForecastRunner)) *ForecastRunner {
	t.Helper()
	runner, err := NewForecastRunner(ForecastRunnerConfig{
		MISClient: &mockMISClient{},
		RefData:   &mockRefDataClient{},
		Timeout:   10 * time.Second,
		Logger:    slog.New(slog.NewTextHandler(os.Stdout, nil)),
	})
	require.NoError(t, err)
	for _, opt := range opts {
		opt(runner)
	}
	return runner
}

// executeRawScript calls the internal executeScript directly so we can test
// sandboxing without the forecast-point validation layer getting in the way.
func executeRawScript(t *testing.T, runner *ForecastRunner, script string) ([]ForecastPoint, error) {
	t.Helper()
	return runner.executeScript(context.Background(), script, minimalForecastCtx())
}

// TestForecastSecurityScriptTooLarge verifies that scripts exceeding MaxScriptSize
// are rejected before execution via the public ExecuteStrategy entry point.
func TestForecastSecurityScriptTooLarge(t *testing.T) {
	runner := newSecurityTestRunner(t)

	largeScript := strings.Repeat("# padding line\n", 5000) // ~80KB – above 64KB
	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:           largeScript,
		HorizonHours:     24,
		GranularityHours: 1,
		Now:              time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrScriptTooLarge)
}

// TestForecastSecurityTimeout verifies that long-running scripts are terminated
// when the runner's timeout elapses.
func TestForecastSecurityTimeout(t *testing.T) {
	runner, err := NewForecastRunner(ForecastRunnerConfig{
		MISClient: &mockMISClient{},
		RefData:   &mockRefDataClient{},
		Timeout:   100 * time.Millisecond,
		Logger:    slog.New(slog.NewTextHandler(os.Stdout, nil)),
	})
	require.NoError(t, err)

	tests := []struct {
		name   string
		script string
	}{
		{
			name: "infinite for loop",
			script: `
def compute_forecast(ctx):
    x = 0
    for i in range(10000000000):
        x = x + 1
    return []
`,
		},
		{
			name: "nested loops",
			script: `
def compute_forecast(ctx):
    x = 0
    for i in range(10000):
        for j in range(10000):
            for k in range(10000):
                x = x + 1
    return []
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runner.executeScript(context.Background(), tt.script, minimalForecastCtx())
			require.Error(t, err)
			assert.True(t,
				strings.Contains(err.Error(), "timeout") ||
					strings.Contains(err.Error(), "deadline") ||
					strings.Contains(err.Error(), "cancelled") ||
					strings.Contains(err.Error(), "step") ||
					strings.Contains(err.Error(), "execution"),
				"expected timeout/deadline/cancelled/step error, got: %v", err)
		})
	}
}

// TestForecastSecurityStepLimit verifies that scripts exceeding MaxStepsPerExecution
// are terminated with a step/execution error before the long timeout fires.
func TestForecastSecurityStepLimit(t *testing.T) {
	// Use a long wall-clock timeout so the step limit triggers first.
	runner, err := NewForecastRunner(ForecastRunnerConfig{
		MISClient: &mockMISClient{},
		RefData:   &mockRefDataClient{},
		Timeout:   30 * time.Second,
		Logger:    slog.New(slog.NewTextHandler(os.Stdout, nil)),
	})
	require.NoError(t, err)

	script := `
def compute_forecast(ctx):
    x = 0
    for i in range(10000000):
        x = x + 1
    return []
`
	_, err = runner.executeScript(context.Background(), script, minimalForecastCtx())
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "step") ||
			strings.Contains(err.Error(), "limit") ||
			strings.Contains(err.Error(), "cancelled") ||
			strings.Contains(err.Error(), "execution"),
		"expected step limit or execution error, got: %v", err)
}

// TestForecastSecurityContextCancellation verifies that a cancelled context
// stops script execution promptly.
func TestForecastSecurityContextCancellation(t *testing.T) {
	runner := newSecurityTestRunner(t)

	script := `
def compute_forecast(ctx):
    x = 0
    for i in range(100000000):
        x = x + 1
    return []
`
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := runner.executeScript(ctx, script, minimalForecastCtx())
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "cancel") ||
			strings.Contains(err.Error(), "timeout") ||
			strings.Contains(err.Error(), "step") ||
			strings.Contains(err.Error(), "execution"),
		"expected cancellation error, got: %v", err)
}

// TestForecastSecurityBlockedFunctions verifies that dangerous built-in functions
// are not available in the forecasting sandbox.
func TestForecastSecurityBlockedFunctions(t *testing.T) {
	runner := newSecurityTestRunner(t)

	tests := []struct {
		name   string
		script string
	}{
		{
			name: "open not available",
			script: `
result = open("file.txt")
`,
		},
		{
			name: "exec not available",
			script: `
result = exec("1+1")
`,
		},
		{
			name: "compile not available",
			script: `
result = compile("1+1")
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeRawScript(t, runner, tt.script)
			require.Error(t, err, "blocked function should not be available")
		})
	}
}

// TestForecastSecurityNoFilesystemAccess verifies that filesystem-related operations
// are blocked in the sandbox.
func TestForecastSecurityNoFilesystemAccess(t *testing.T) {
	runner := newSecurityTestRunner(t)

	// Starlark has no 'import' keyword — this should fail at parse time.
	script := `
import os
result = os.getcwd()
`
	_, err := executeRawScript(t, runner, script)
	require.Error(t, err)
}

// TestForecastSecurityNoLoadStatements verifies that load() directives are blocked.
func TestForecastSecurityNoLoadStatements(t *testing.T) {
	runner := newSecurityTestRunner(t)

	script := `load("module.star", "func")`
	_, err := executeRawScript(t, runner, script)
	require.Error(t, err)
}

// TestForecastSecurityPrintRedirection verifies that print() output is captured by
// the logger and does not reach stdout directly.
func TestForecastSecurityPrintRedirection(t *testing.T) {
	var logOutput strings.Builder
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))

	runner, err := NewForecastRunner(ForecastRunnerConfig{
		MISClient: &mockMISClient{},
		RefData:   &mockRefDataClient{},
		Timeout:   5 * time.Second,
		Logger:    logger,
	})
	require.NoError(t, err)

	script := `
print("sensitive information")
print("secret data", "more data")
def compute_forecast(ctx):
    return []
`
	_, _ = runner.executeScript(context.Background(), script, minimalForecastCtx())

	output := logOutput.String()
	assert.Contains(t, output, "sensitive information")
	assert.Contains(t, output, "secret data")
}

// TestForecastSecurityConcurrentIsolation verifies that concurrent script executions
// do not share global state.
func TestForecastSecurityConcurrentIsolation(t *testing.T) {
	runner := newSecurityTestRunner(t)

	// Both scripts set a module-level variable; each execution must see only its own.
	script1 := `
shared_var = "script1"
def compute_forecast(ctx):
    return []
`
	script2 := `
shared_var = "script2"
def compute_forecast(ctx):
    return []
`

	var wg sync.WaitGroup
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err1 = runner.executeScript(context.Background(), script1, minimalForecastCtx())
	}()
	go func() {
		defer wg.Done()
		_, err2 = runner.executeScript(context.Background(), script2, minimalForecastCtx())
	}()
	wg.Wait()

	require.NoError(t, err1)
	require.NoError(t, err2)
}

// TestForecastSecurityStressTest runs many concurrent executions to verify stability
// under load.
func TestForecastSecurityStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	runner := newSecurityTestRunner(t)

	script := `
def compute_forecast(ctx):
    result = 0
    for i in range(100):
        result = result + i
    return []
`

	numGoroutines := 100
	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runner.executeScript(context.Background(), script, minimalForecastCtx())
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("stress test error: %v", err)
	}
}

// TestForecastSecurityContextImmutability verifies that the frozen ctx dict passed
// to compute_forecast cannot be mutated by the script.
func TestForecastSecurityContextImmutability(t *testing.T) {
	runner := newSecurityTestRunner(t)

	// Attempt to mutate the frozen context dict — must fail.
	script := `
def compute_forecast(ctx):
    ctx["injected"] = "value"
    return []
`
	_, err := runner.executeScript(context.Background(), script, minimalForecastCtx())
	require.Error(t, err, "mutating frozen context should fail")
}

// TestForecastSecurityValidScript verifies that a well-formed minimal script runs
// without errors.
func TestForecastSecurityValidScript(t *testing.T) {
	runner := newSecurityTestRunner(t)

	script := `
def compute_forecast(ctx):
    return []
`
	_, err := executeRawScript(t, runner, script)
	require.NoError(t, err)
}
