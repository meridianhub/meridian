// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.starlark.net/starlark"
)

// Security constraints for Starlark runtime per PRD Section 6.1.
const (
	// DefaultTimeout is the maximum execution time for a saga script.
	DefaultTimeout = 5 * time.Second

	// MaxScriptSize is the maximum allowed script size in bytes.
	MaxScriptSize = 64 * 1024 // 64KB

	// MaxLoopNestingDepth is the maximum allowed loop nesting level.
	MaxLoopNestingDepth = 3

	// MemoryWarningThreshold is the memory allocation threshold that triggers a warning.
	MemoryWarningThreshold = 10 * 1024 * 1024 // 10MB

	// MaxStepsPerExecution limits the number of steps to prevent infinite loops.
	MaxStepsPerExecution = 1_000_000
)

// Runtime errors.
var (
	// ErrTimeout is returned when script execution exceeds the timeout.
	ErrTimeout = errors.New("script execution timeout")

	// ErrCancelled is returned when the context is cancelled during execution.
	ErrCancelled = errors.New("script execution cancelled")

	// ErrScriptTooLarge is returned when a script exceeds MaxScriptSize.
	ErrScriptTooLarge = errors.New("script exceeds maximum size")

	// ErrSyntax is returned when a script has syntax errors.
	ErrSyntax = errors.New("script syntax error")

	// ErrExecution is returned when script execution fails.
	ErrExecution = errors.New("script execution error")
)

// Runtime provides a secure Starlark execution environment for saga scripts.
// It enforces timeouts, restricts builtins, and logs print statements.
type Runtime struct {
	logger  *slog.Logger
	timeout time.Duration
}

// RuntimeOption configures a Runtime.
type RuntimeOption func(*Runtime)

// WithTimeout sets the execution timeout for the runtime.
func WithTimeout(timeout time.Duration) RuntimeOption {
	return func(r *Runtime) {
		r.timeout = timeout
	}
}

// NewRuntime creates a new Starlark runtime with security constraints.
func NewRuntime(logger *slog.Logger, opts ...RuntimeOption) (*Runtime, error) {
	if logger == nil {
		logger = slog.Default()
	}

	r := &Runtime{
		logger:  logger,
		timeout: DefaultTimeout,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r, nil
}

// ExecutionResult holds the result of a saga script execution.
type ExecutionResult struct {
	// Globals contains the final global variables after execution.
	Globals map[string]interface{}
}

// ExecuteSaga executes a Starlark script with the given input.
// It enforces timeout constraints and restricts dangerous operations.
func (r *Runtime) ExecuteSaga(ctx context.Context, name string, script string, input map[string]interface{}) (*ExecutionResult, error) {
	// Apply timeout to context
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// Build predeclared variables including input
	predeclared := r.buildPredeclared(input)

	// Create thread with context-based cancellation
	thread := &starlark.Thread{
		Name: name,
		Print: func(_ *starlark.Thread, msg string) {
			r.logger.Info("starlark print", "saga", name, "message", msg)
		},
	}

	// Store context in thread-local for cancellation checking
	thread.SetLocal("ctx", ctx)

	// Set up cancellation checking
	done := make(chan struct{})
	var execErr error
	var globals starlark.StringDict

	// Execute in goroutine so we can handle context cancellation
	go func() {
		defer close(done)

		var err error
		//nolint:staticcheck // ExecFileOptions requires FileOptions which we don't need to customize
		globals, err = starlark.ExecFile(thread, name+".star", script, predeclared)
		if err != nil {
			execErr = err
		}
	}()

	// Wait for completion or context cancellation
	select {
	case <-done:
		if execErr != nil {
			return nil, r.wrapError(execErr)
		}
	case <-ctx.Done():
		// Context cancelled or timed out
		thread.Cancel("execution cancelled")
		<-done // Wait for goroutine to finish
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: exceeded %v", ErrTimeout, r.timeout)
		}
		return nil, fmt.Errorf("%w: %w", ErrCancelled, ctx.Err())
	}

	// Convert globals to result
	result := &ExecutionResult{
		Globals: make(map[string]interface{}),
	}

	for name, val := range globals {
		result.Globals[name] = starlarkToGo(val)
	}

	return result, nil
}

// buildPredeclared creates the predeclared environment for script execution.
// It includes restricted builtins and the input data.
func (r *Runtime) buildPredeclared(input map[string]interface{}) starlark.StringDict {
	// Start with restricted builtins
	predeclared := NewRestrictedBuiltins(r.logger)

	// Add input_data if provided
	if input != nil {
		inputDict := starlark.NewDict(len(input))
		for k, v := range input {
			_ = inputDict.SetKey(starlark.String(k), goToStarlark(v))
		}
		predeclared["input_data"] = inputDict
	} else {
		predeclared["input_data"] = starlark.NewDict(0)
	}

	return predeclared
}

// wrapError wraps Starlark errors with appropriate package errors.
func (r *Runtime) wrapError(err error) error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Check for EvalError using errors.As instead of type assertion
	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) {
		return errors.Join(ErrExecution, err)
	}

	// Check if it looks like a syntax error
	if contains(errStr, "syntax") || contains(errStr, "parse") || contains(errStr, "got ") {
		return errors.Join(ErrSyntax, err)
	}

	// Check for cancellation
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrCancelled, err)
	}

	return errors.Join(ErrExecution, err)
}

// contains checks if s contains substr (case-insensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if equalFold(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}

func equalFold(s, t string) bool {
	if len(s) != len(t) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c1 := s[i]
		c2 := t[i]
		if c1 >= 'A' && c1 <= 'Z' {
			c1 += 'a' - 'A'
		}
		if c2 >= 'A' && c2 <= 'Z' {
			c2 += 'a' - 'A'
		}
		if c1 != c2 {
			return false
		}
	}
	return true
}

// goToStarlark converts a Go value to a Starlark value.
func goToStarlark(v interface{}) starlark.Value {
	if v == nil {
		return starlark.None
	}

	switch val := v.(type) {
	case string:
		return starlark.String(val)
	case int:
		return starlark.MakeInt(val)
	case int64:
		return starlark.MakeInt64(val)
	case float64:
		return starlark.Float(val)
	case bool:
		return starlark.Bool(val)
	case []interface{}:
		list := make([]starlark.Value, len(val))
		for i, elem := range val {
			list[i] = goToStarlark(elem)
		}
		return starlark.NewList(list)
	case map[string]interface{}:
		dict := starlark.NewDict(len(val))
		for k, v := range val {
			_ = dict.SetKey(starlark.String(k), goToStarlark(v))
		}
		return dict
	default:
		return starlark.String(fmt.Sprintf("%v", v))
	}
}

// starlarkToGo converts a Starlark value to a Go value.
func starlarkToGo(v starlark.Value) interface{} {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case starlark.String:
		return string(val)
	case starlark.Int:
		if i, ok := val.Int64(); ok {
			return i
		}
		return val.String()
	case starlark.Float:
		return float64(val)
	case starlark.Bool:
		return bool(val)
	case starlark.NoneType:
		return nil
	case *starlark.List:
		result := make([]interface{}, val.Len())
		for i := 0; i < val.Len(); i++ {
			result[i] = starlarkToGo(val.Index(i))
		}
		return result
	case *starlark.Dict:
		result := make(map[string]interface{})
		for _, item := range val.Items() {
			if key, ok := item[0].(starlark.String); ok {
				result[string(key)] = starlarkToGo(item[1])
			}
		}
		return result
	default:
		return val.String()
	}
}
