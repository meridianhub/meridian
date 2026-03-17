package valuation_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/valuation"
)

// minimalRequest returns a valid Request for security tests that don't care about
// actual valuation results.
func minimalRequest() *valuation.Request {
	return &valuation.Request{
		RequestID:   uuid.New(),
		MethodID:    uuid.New(),
		Quantity:    valuation.Quantity{Amount: decimal.NewFromInt(1), InstrumentCode: "KWH"},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
		Parameters:  map[string]interface{}{},
	}
}

// newSecurityRuntime returns a StarlarkRuntime with a short timeout for security tests.
func newSecurityRuntime(timeout time.Duration) valuation.StarlarkRuntime {
	return valuation.NewStarlarkRuntime(valuation.StarlarkRuntimeConfig{
		Timeout: timeout,
	})
}

// TestValuationSecurityScriptTooLarge verifies that scripts exceeding MaxStarlarkScriptSize
// are rejected before execution.
func TestValuationSecurityScriptTooLarge(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	largeScript := strings.Repeat("x=1\n", 20000) // well above 64KB
	_, err := runtime.Execute(context.Background(), largeScript, minimalRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, valuation.ErrStarlarkScriptTooLarge)
}

// TestValuationSecurityStepLimit verifies that scripts exceeding the 5M step limit
// are terminated rather than running indefinitely.
func TestValuationSecurityStepLimit(t *testing.T) {
	// Long wall-clock timeout so the step limit triggers first.
	runtime := newSecurityRuntime(30 * time.Second)

	script := `
def burn_steps():
    x = 0
    for i in range(10000000):
        x = x + 1
    return x
result = {"valued_amount": burn_steps(), "instrument": "GBP"}
`
	_, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "step") ||
			strings.Contains(err.Error(), "limit") ||
			strings.Contains(err.Error(), "cancelled") ||
			strings.Contains(err.Error(), "execution") ||
			strings.Contains(err.Error(), "timeout"),
		"expected step limit or execution error, got: %v", err)
}

// TestValuationSecurityTimeout verifies that long-running scripts are terminated
// when the configured timeout elapses.
func TestValuationSecurityTimeout(t *testing.T) {
	runtime := newSecurityRuntime(100 * time.Millisecond)

	tests := []struct {
		name   string
		script string
	}{
		{
			name: "large range loop",
			script: `
def run():
    x = 0
    for i in range(10000000000):
        x = x + 1
    return x
result = {"valued_amount": run(), "instrument": "GBP"}
`,
		},
		{
			name: "nested loops",
			script: `
def run():
    x = 0
    for i in range(10000):
        for j in range(10000):
            x = x + 1
    return x
result = {"valued_amount": run(), "instrument": "GBP"}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runtime.Execute(context.Background(), tt.script, minimalRequest())
			require.Error(t, err)
			assert.True(t,
				strings.Contains(err.Error(), "timeout") ||
					strings.Contains(err.Error(), "deadline") ||
					strings.Contains(err.Error(), "cancelled") ||
					strings.Contains(err.Error(), "step") ||
					strings.Contains(err.Error(), "execution"),
				"expected timeout/step/execution error, got: %v", err)
		})
	}
}

// TestValuationSecurityContextCancellation verifies that a cancelled context stops
// script execution.
func TestValuationSecurityContextCancellation(t *testing.T) {
	runtime := newSecurityRuntime(10 * time.Second)

	script := `
def run():
    x = 0
    for i in range(100000000):
        x = x + 1
    return x
result = {"valued_amount": run(), "instrument": "GBP"}
`
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := runtime.Execute(ctx, script, minimalRequest())
	require.Error(t, err)
}

// TestValuationSecurityBlockedFunctions verifies that dangerous built-in functions
// are not available in the valuation sandbox.
func TestValuationSecurityBlockedFunctions(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	tests := []struct {
		name   string
		script string
	}{
		{
			name:   "open not available",
			script: `result = open("file.txt")`,
		},
		{
			name:   "exec not available",
			script: `result = exec("1+1")`,
		},
		{
			name:   "compile not available",
			script: `result = compile("1+1")`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runtime.Execute(context.Background(), tt.script, minimalRequest())
			require.Error(t, err, "blocked function should not be available in valuation sandbox")
		})
	}
}

// TestValuationSecurityNoFilesystemAccess verifies that filesystem operations
// cannot be invoked from valuation scripts.
func TestValuationSecurityNoFilesystemAccess(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	// Starlark has no 'import' keyword — should fail at parse time.
	script := `
import os
result = {"valued_amount": 0, "instrument": "GBP"}
`
	_, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.Error(t, err)
}

// TestValuationSecurityNoLoadStatements verifies that load() directives are blocked.
func TestValuationSecurityNoLoadStatements(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `load("module.star", "func")`
	_, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.Error(t, err)
}

// TestValuationSecurityConcurrentIsolation verifies that concurrent executions
// do not share global state.
func TestValuationSecurityConcurrentIsolation(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script1 := `
shared_var = "script1"
result = {"valued_amount": 1, "instrument": "GBP"}
`
	script2 := `
shared_var = "script2"
result = {"valued_amount": 2, "instrument": "GBP"}
`

	var wg sync.WaitGroup
	var err1, err2 error
	var resp1, resp2 *valuation.Response

	wg.Add(2)
	go func() {
		defer wg.Done()
		resp1, err1 = runtime.Execute(context.Background(), script1, minimalRequest())
	}()
	go func() {
		defer wg.Done()
		resp2, err2 = runtime.Execute(context.Background(), script2, minimalRequest())
	}()
	wg.Wait()

	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.Equal(t, decimal.NewFromInt(1), resp1.ValuedAmount.Amount)
	assert.Equal(t, decimal.NewFromInt(2), resp2.ValuedAmount.Amount)
}

// TestValuationSecurityStressTest runs many concurrent executions to verify stability.
func TestValuationSecurityStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	runtime := newSecurityRuntime(5 * time.Second)

	script := `
def compute():
    x = 0
    for i in range(100):
        x = x + i
    return x
result = {"valued_amount": compute(), "instrument": "GBP"}
`

	numGoroutines := 100
	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runtime.Execute(context.Background(), script, minimalRequest())
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

// TestValuationSecurityValidScript verifies that a well-formed minimal script
// executes without errors.
func TestValuationSecurityValidScript(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `
result = {"valued_amount": 42, "instrument": "GBP"}
`
	resp, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.NoError(t, err)
	assert.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode)
}

// TestValuationSecuritySyntaxError verifies that scripts with syntax errors
// return a clear error rather than panicking.
func TestValuationSecuritySyntaxError(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `
result = {"valued_amount": 42
` // missing closing brace
	_, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.Error(t, err)
}

// TestValuationSecurityMissingResult verifies that scripts which do not set the
// required 'result' variable return a descriptive error.
func TestValuationSecurityMissingResult(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `
x = 42
# No result variable set
`
	_, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, valuation.ErrStarlarkMissingResult)
}
