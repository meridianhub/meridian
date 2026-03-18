package saga

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRuntime(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)
	require.NotNil(t, runtime)
}

func TestExecuteSaga_ValidScript(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	script := `
result = "hello"
`
	ctx := context.Background()
	result, err := runtime.ExecuteSaga(ctx, "test_saga", script, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestExecuteSaga_TimeoutEnforcement(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// Use a shorter timeout for the test
	runtime, err := NewRuntime(logger, WithTimeout(100*time.Millisecond))
	require.NoError(t, err)

	// Script with long computation (Starlark for loops with large range)
	script := `
def compute():
    result = 0
    for i in range(100000000):
        result = result + i
    return result

x = compute()
`
	ctx := context.Background()
	_, err = runtime.ExecuteSaga(ctx, "infinite_loop", script, nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "cancelled") || strings.Contains(err.Error(), "deadline"),
		"expected timeout/cancelled/deadline error, got: %v", err)
}

func TestExecuteSaga_ContextCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	// Script with long operation
	script := `
counter = 0
for i in range(10000000):
    counter = counter + 1
`
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		//nolint:forbidigo // triggers context cancellation while long-running saga script executes
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err = runtime.ExecuteSaga(ctx, "cancelled_saga", script, nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "cancel") || strings.Contains(err.Error(), "timeout"),
		"expected cancellation error, got: %v", err)
}

func TestExecuteSaga_InputVariables(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	script := `
result = input_data["key"]
`
	input := map[string]interface{}{
		"key": "value123",
	}
	ctx := context.Background()
	result, err := runtime.ExecuteSaga(ctx, "input_test", script, input)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestExecuteSaga_ConcurrentExecution(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	script := `
result = 42
`
	ctx := context.Background()
	var wg sync.WaitGroup
	var successCount atomic.Int32
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runtime.ExecuteSaga(ctx, "concurrent_saga", script, nil)
			if err == nil {
				successCount.Add(1)
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, int32(numGoroutines), successCount.Load(), "all concurrent executions should succeed")
}

func TestExecuteSaga_EmptyScript(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	ctx := context.Background()
	result, err := runtime.ExecuteSaga(ctx, "empty", "", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestExecuteSaga_SyntaxError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	script := `
def incomplete(
`
	ctx := context.Background()
	_, err = runtime.ExecuteSaga(ctx, "syntax_error", script, nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "syntax") || strings.Contains(err.Error(), "parse"),
		"expected syntax/parse error, got: %v", err)
}

func TestExecuteSaga_PrintRedirection(t *testing.T) {
	var logOutput strings.Builder
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	script := `
print("hello from starlark")
`
	ctx := context.Background()
	_, err = runtime.ExecuteSaga(ctx, "print_test", script, nil)
	require.NoError(t, err)

	// Print should be redirected to audit logger
	assert.Contains(t, logOutput.String(), "hello from starlark",
		"print output should be captured in logs")
}

func TestRuntimeOptions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Test with custom timeout
	runtime, err := NewRuntime(logger, WithTimeout(2*time.Second))
	require.NoError(t, err)
	require.NotNil(t, runtime)
	assert.Equal(t, 2*time.Second, runtime.timeout)
}
