// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.starlark.net/starlark"

	"github.com/meridianhub/meridian/shared/platform/sandbox"
)

// sandboxCfg is the unified sandbox configuration for saga scripts.
var sandboxCfg = sandbox.DefaultConfig()

// Security constraints for Starlark runtime per PRD Section 6.1.
// These constants are retained for backward compatibility; canonical values live in sandbox.DefaultConfig().
const (
	// DefaultTimeout is the maximum execution time for a saga script.
	DefaultTimeout = 5 * time.Second

	// MaxScriptSize is the maximum allowed script size in bytes.
	MaxScriptSize = 64 * 1024 // 64KB

	// MaxLoopNestingDepth is the maximum allowed loop nesting level.
	MaxLoopNestingDepth = 3

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
	logger             *slog.Logger
	timeout            time.Duration
	partyScopeResolver PartyScopeResolver
}

// RuntimeOption configures a Runtime.
type RuntimeOption func(*Runtime)

// WithTimeout sets the execution timeout for the runtime.
func WithTimeout(timeout time.Duration) RuntimeOption {
	return func(r *Runtime) {
		r.timeout = timeout
	}
}

// WithPartyScopeResolver sets the party scope resolver for the runtime.
// When set, the runtime will resolve party scope before executing saga scripts.
func WithPartyScopeResolver(resolver PartyScopeResolver) RuntimeOption {
	return func(r *Runtime) {
		r.partyScopeResolver = resolver
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

	// PartyScope is the resolved party scope for this execution (if party isolation is enabled).
	// This is set when a PartyScopeResolver is configured and executing_party_id is provided.
	PartyScope *PartyScope
}

// ExecutionInput holds the input parameters for saga execution.
type ExecutionInput struct {
	// Data is the input data passed to the Starlark script as input_data.
	Data map[string]interface{}

	// ExecutingPartyID is the ID of the party executing the saga (for party isolation).
	// If set and a PartyScopeResolver is configured, party scope will be resolved.
	ExecutingPartyID *uuid.UUID

	// Predeclared contains additional Starlark values to inject into the global scope.
	// These are merged after restricted builtins and input_data, allowing callers
	// to inject service modules, backward-compat shims, or other globals.
	Predeclared starlark.StringDict

	// ThreadSetup is called after the thread is created but before script execution.
	// It allows callers to set thread-local storage (e.g., StarlarkContext for handlers).
	ThreadSetup func(thread *starlark.Thread)
}

// ExecuteSaga executes a Starlark script with the given input.
// It enforces timeout constraints and restricts dangerous operations.
// For party isolation support, use ExecuteSagaWithInput instead.
func (r *Runtime) ExecuteSaga(ctx context.Context, name string, script string, input map[string]interface{}) (*ExecutionResult, error) {
	return r.ExecuteSagaWithInput(ctx, name, script, ExecutionInput{Data: input})
}

// ExecuteSagaWithInput executes a Starlark script with the given execution input.
// This variant supports party scope resolution when ExecutingPartyID is provided.
//
// Party Scope Resolution (FR-35):
//   - If ExecutingPartyID is set and PartyScopeResolver is configured, party scope is resolved
//   - The resolved scope is made available as ctx.party_scope in Starlark
//   - Party scope resolution happens BEFORE the first user step (synthetic step -1)
//   - Resolution failures cause the saga to fail before any user steps execute
func (r *Runtime) ExecuteSagaWithInput(ctx context.Context, name string, script string, execInput ExecutionInput) (*ExecutionResult, error) {
	// Validate script size before any execution.
	if err := sandbox.ValidateScript(script, sandboxCfg); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrScriptTooLarge, err)
	}

	// Apply timeout to context
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// Resolve party scope BEFORE executing user script (synthetic step -1)
	var partyScope *PartyScope
	if execInput.ExecutingPartyID != nil && r.partyScopeResolver != nil {
		r.logger.Debug("resolving party scope",
			"saga", name,
			"executing_party_id", execInput.ExecutingPartyID,
		)

		var err error
		partyScope, err = r.partyScopeResolver.Resolve(ctx, *execInput.ExecutingPartyID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve party scope (step -1): %w", err)
		}

		r.logger.Info("party scope resolved",
			"saga", name,
			"party_id", partyScope.PartyID,
			"party_type", partyScope.PartyType,
			"visible_parties_count", len(partyScope.VisibleParties),
			"tenant_id", partyScope.TenantID,
		)

		// Perform visibility pre-flight validation (step -0.5)
		// Extract party references from input and validate against scope
		manifest := NewVisibilityManifestFromInput(execInput.Data)
		if len(manifest.ReferencedParties) > 0 {
			validator := NewVisibilityValidator()
			if err := validator.ValidateOrError(partyScope, manifest); err != nil {
				return nil, fmt.Errorf("visibility pre-flight check failed (step -0.5): %w", err)
			}
			r.logger.Debug("visibility pre-flight check passed",
				"saga", name,
				"referenced_parties_count", len(manifest.ReferencedParties),
			)
		}
	}

	// Build predeclared variables including input and party scope
	predeclared := r.buildPredeclaredWithScope(execInput.Data, partyScope)

	// Merge additional predeclared globals (e.g., service modules, backward-compat shims)
	for k, v := range execInput.Predeclared {
		predeclared[k] = v
	}

	// Create thread with context-based cancellation
	thread := &starlark.Thread{
		Name: name,
		Print: func(_ *starlark.Thread, msg string) {
			r.logger.Info("starlark print", "saga", name, "message", msg)
		},
	}

	// Store context in thread-local for cancellation checking
	thread.SetLocal("ctx", ctx)

	// Allow callers to set up thread-local storage (e.g., StarlarkContext for handlers)
	if execInput.ThreadSetup != nil {
		execInput.ThreadSetup(thread)
	}

	// Enforce CPU step limit to prevent tenant scripts from exhausting compute resources.
	sandbox.HardenThread(thread, sandboxCfg)

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
		Globals:    make(map[string]interface{}),
		PartyScope: partyScope,
	}

	for n, val := range globals {
		result.Globals[n] = starlarkToGo(val)
	}

	return result, nil
}

// buildPredeclaredWithScope creates the predeclared environment with party scope support.
// It includes restricted builtins, input data, and optionally party scope.
func (r *Runtime) buildPredeclaredWithScope(input map[string]interface{}, partyScope *PartyScope) starlark.StringDict {
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

	// Add party_scope if provided (will be added as immutable in Task 6.3)
	// For now, we make it available as a frozen dict
	if partyScope != nil {
		predeclared["party_scope"] = partyScopeToStarlark(partyScope)
	}

	return predeclared
}

// partyScopeToStarlark converts a PartyScope to a frozen Starlark value.
// The returned value is immutable to prevent modification by scripts.
func partyScopeToStarlark(ps *PartyScope) starlark.Value {
	// Build visible_parties list
	visiblePartiesList := make([]starlark.Value, len(ps.VisibleParties))
	for i, partyID := range ps.VisibleParties {
		visiblePartiesList[i] = starlark.String(partyID.String())
	}

	// Create and freeze the visible parties list
	visiblePartiesValue := starlark.NewList(visiblePartiesList)
	visiblePartiesValue.Freeze()

	// Build the party_scope dict
	scopeDict := starlark.NewDict(4)
	_ = scopeDict.SetKey(starlark.String("party_id"), starlark.String(ps.PartyID.String()))
	_ = scopeDict.SetKey(starlark.String("party_type"), starlark.String(ps.PartyType))
	_ = scopeDict.SetKey(starlark.String("visible_parties"), visiblePartiesValue)
	_ = scopeDict.SetKey(starlark.String("tenant_id"), starlark.String(ps.TenantID))

	// Freeze the dict to make it immutable
	scopeDict.Freeze()

	return scopeDict
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
	case map[string]string:
		// Handle map[string]string (e.g., PaymentAttributes)
		dict := starlark.NewDict(len(val))
		for k, v := range val {
			_ = dict.SetKey(starlark.String(k), starlark.String(v))
		}
		return dict
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
