package valuation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/valuation/internal/builtins"
	"github.com/meridianhub/meridian/shared/platform/sandbox"
	"github.com/shopspring/decimal"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// SandboxConfig is the unified sandbox configuration for valuation scripts.
var SandboxConfig = sandbox.ValuationConfig()

// Starlark security constraints.
// These constants are retained for backward compatibility; canonical values live in sandbox.ValuationConfig().
const (
	// DefaultStarlarkTimeout is the maximum execution time for a valuation script.
	DefaultStarlarkTimeout = 5 * time.Second

	// MaxStarlarkScriptSize is the maximum allowed script size in bytes.
	MaxStarlarkScriptSize = 64 * 1024 // 64KB
)

var (
	// ErrStarlarkScriptTooLarge is returned when a script exceeds MaxStarlarkScriptSize.
	ErrStarlarkScriptTooLarge = errors.New("script exceeds maximum size")

	// ErrStarlarkSyntax is returned when a script has syntax errors.
	ErrStarlarkSyntax = errors.New("script syntax error")

	// ErrStarlarkExecution is returned when script execution fails.
	ErrStarlarkExecution = errors.New("script execution error")

	// ErrStarlarkMissingResult is returned when the script doesn't set a 'result' variable.
	ErrStarlarkMissingResult = errors.New("script must set 'result' variable")

	// ErrStarlarkInvalidResult is returned when 'result' is not a dict.
	ErrStarlarkInvalidResult = errors.New("result must be a dict with 'valued_amount' and 'instrument'")
)

// defaultStarlarkRuntime implements StarlarkRuntime.
type defaultStarlarkRuntime struct {
	timeout  time.Duration
	builtins starlark.StringDict
}

// StarlarkRuntimeConfig holds configuration for creating a StarlarkRuntime.
type StarlarkRuntimeConfig struct {
	// Timeout is the maximum execution time for a script.
	// If zero, DefaultStarlarkTimeout (5s) is used.
	Timeout time.Duration

	// PolicyRuntime provides CEL evaluation for run_policy builtin.
	// When set, valuation scripts can delegate mathematical calculations to CEL
	// via run_policy(expression="...", variables={...}).
	PolicyRuntime PolicyRuntime
}

// NewStarlarkRuntime creates a new StarlarkRuntime with security constraints.
func NewStarlarkRuntime(cfg StarlarkRuntimeConfig) StarlarkRuntime {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultStarlarkTimeout
	}

	// Build builtins registry with optional CEL policy evaluator
	registry := builtins.NewRegistry()
	if cfg.PolicyRuntime != nil {
		registry.EvalPolicy = func(ctx context.Context, expression string, variables map[string]interface{}) (interface{}, error) {
			compiled, err := cfg.PolicyRuntime.CompilePolicy(expression)
			if err != nil {
				return nil, err
			}
			result, _, err := cfg.PolicyRuntime.EvaluatePolicy(ctx, compiled, variables)
			return result, err
		}
	}

	return &defaultStarlarkRuntime{
		timeout:  timeout,
		builtins: registry.CreateBuiltins(),
	}
}

// Execute runs a Starlark valuation script with the given request.
// The script must set a 'result' variable containing a dict with:
//   - valued_amount: numeric value
//   - instrument: string instrument code (e.g., "GBP", "USD")
func (r *defaultStarlarkRuntime) Execute(ctx context.Context, script string, req *Request) (*Response, error) {
	// Validate script size using unified sandbox config.
	if err := sandbox.ValidateScript(script, SandboxConfig); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStarlarkScriptTooLarge, err)
	}

	// Apply timeout
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// Build context for script
	scriptCtx := r.buildScriptContext(req)

	// Create Starlark thread
	thread := &starlark.Thread{
		Name: "valuation",
		Print: func(_ *starlark.Thread, _ string) {
			// Silently discard print statements (security: no output channels)
		},
	}

	// Store context for timeout checking
	thread.SetLocal("ctx", ctx)

	// Start goroutine to enforce timeout by calling thread.Cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel("execution timeout")
		case <-done:
			// Execution completed
		}
	}()

	// Apply sandbox security constraints (step limits) from unified config.
	sandbox.HardenThread(thread, SandboxConfig)

	// Build predeclared variables: builtins + script context
	predeclared := make(starlark.StringDict, len(r.builtins)+1)
	for name, val := range r.builtins {
		predeclared[name] = val
	}
	predeclared["ctx"] = r.toStarlarkValue(scriptCtx)

	// Parse and execute script using the non-deprecated API
	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, thread, "valuation.star", script, predeclared)
	if err != nil {
		// Check if context cancelled (timeout)
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("%w: execution exceeded %v", ErrStarlarkTimeout, r.timeout)
			}
			return nil, fmt.Errorf("%w: %w", ErrStarlarkExecution, ctx.Err())
		default:
			// Syntax or execution error - both are syntax errors in Starlark terminology
			return nil, fmt.Errorf("%w: %w", ErrStarlarkSyntax, err)
		}
	}

	// Extract 'result' variable
	resultVal, ok := globals["result"]
	if !ok {
		return nil, fmt.Errorf("%w: script did not set 'result' global variable", ErrStarlarkMissingResult)
	}

	// Convert result to Response
	resp, err := r.parseResult(resultVal, req)
	if err != nil {
		return nil, err
	}

	// Extract path entries recorded by record_path() and populate Analysis
	if analysis := r.extractPathEntries(thread); analysis != nil {
		resp.Analysis = analysis
	}

	return resp, nil
}

// extractPathEntries extracts path entries from thread-local storage.
func (r *defaultStarlarkRuntime) extractPathEntries(thread *starlark.Thread) *Analysis {
	entries, ok := thread.Local("valuation.path_entries").([]builtins.PathEntry)
	if !ok || len(entries) == 0 {
		return nil
	}

	analysis := &Analysis{}
	for _, entry := range entries {
		var data map[string]interface{}
		if entry.Data != nil {
			if m, ok := r.toGoValue(entry.Data).(map[string]interface{}); ok {
				data = m
			}
		}
		analysis.AddPathEntry(entry.Description, data)
	}

	return analysis
}

// buildScriptContext creates the context object available in Starlark as 'ctx'.
func (r *defaultStarlarkRuntime) buildScriptContext(req *Request) map[string]interface{} {
	ctx := make(map[string]interface{})

	// Add request parameters
	for k, v := range req.Parameters {
		ctx[k] = v
	}

	// Add request metadata
	ctx["request_id"] = req.RequestID.String()
	ctx["account_id"] = req.AccountID.String()
	ctx["party_id"] = req.PartyID.String()
	ctx["knowledge_at"] = req.KnowledgeAt.Format(time.RFC3339)

	// Add input quantity
	ctx["input_quantity"] = map[string]interface{}{
		"amount":     req.Quantity.Amount.InexactFloat64(),
		"instrument": req.Quantity.InstrumentCode,
		"attributes": req.Quantity.Attributes,
	}

	return ctx
}

// toStarlarkValue converts a Go value to a Starlark value.
func (r *defaultStarlarkRuntime) toStarlarkValue(v interface{}) starlark.Value {
	switch val := v.(type) {
	case map[string]interface{}:
		dict := &starlark.Dict{}
		for k, v := range val {
			if err := dict.SetKey(starlark.String(k), r.toStarlarkValue(v)); err != nil {
				// SetKey can only fail if key is unhashable, which won't happen with strings
				// Log error but continue (defensive programming)
				continue
			}
		}
		return dict
	case []interface{}:
		list := make([]starlark.Value, len(val))
		for i, elem := range val {
			list[i] = r.toStarlarkValue(elem)
		}
		return starlark.NewList(list)
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
	case nil:
		return starlark.None
	default:
		// Fallback to string representation
		return starlark.String(fmt.Sprintf("%v", val))
	}
}

// parseResult converts the Starlark 'result' dict to a Response.
func (r *defaultStarlarkRuntime) parseResult(resultVal starlark.Value, _ *Request) (*Response, error) {
	// result must be a dict
	resultDict, ok := resultVal.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("%w: got %s", ErrStarlarkInvalidResult, resultVal.Type())
	}

	// Convert to Go map
	resultMap := make(map[string]interface{})
	for _, item := range resultDict.Items() {
		key, ok := item[0].(starlark.String)
		if !ok {
			continue
		}
		resultMap[string(key)] = r.toGoValue(item[1])
	}

	// Extract valued_amount
	valuedAmountRaw, ok := resultMap["valued_amount"]
	if !ok {
		return nil, fmt.Errorf("%w: missing 'valued_amount' field", ErrStarlarkInvalidResult)
	}

	var valuedAmount decimal.Decimal
	switch v := valuedAmountRaw.(type) {
	case float64:
		valuedAmount = decimal.NewFromFloat(v)
	case int64:
		valuedAmount = decimal.NewFromInt(v)
	case string:
		var err error
		valuedAmount, err = decimal.NewFromString(v)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid valued_amount: %w", ErrStarlarkInvalidResult, err)
		}
	default:
		return nil, fmt.Errorf("%w: valued_amount must be numeric, got %T", ErrStarlarkInvalidResult, v)
	}

	// Extract instrument
	instrument, ok := resultMap["instrument"].(string)
	if !ok {
		return nil, fmt.Errorf("%w: missing or invalid 'instrument' field", ErrStarlarkInvalidResult)
	}

	// Build response
	return &Response{
		ValuedAmount: Quantity{
			Amount:         valuedAmount,
			InstrumentCode: instrument,
			Attributes:     map[string]string{}, // No attributes from script result for now
		},
		Analysis:   &Analysis{}, // Analysis populated by builtins during execution
		CacheHit:   false,
		ComputedAt: time.Now(),
	}, nil
}

// toGoValue converts a Starlark value to a Go value.
func (r *defaultStarlarkRuntime) toGoValue(v starlark.Value) interface{} {
	switch val := v.(type) {
	case starlark.String:
		return string(val)
	case starlark.Int:
		i, _ := val.Int64()
		return i
	case starlark.Float:
		return float64(val)
	case starlark.Bool:
		return bool(val)
	case *starlark.Dict:
		m := make(map[string]interface{})
		for _, item := range val.Items() {
			key, ok := item[0].(starlark.String)
			if ok {
				m[string(key)] = r.toGoValue(item[1])
			}
		}
		return m
	case *starlark.List:
		list := make([]interface{}, val.Len())
		for i := 0; i < val.Len(); i++ {
			list[i] = r.toGoValue(val.Index(i))
		}
		return list
	case starlark.NoneType:
		return nil
	default:
		return val.String()
	}
}
