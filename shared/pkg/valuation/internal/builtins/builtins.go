// Package builtins provides read-only Starlark builtin functions for valuation scripts.
//
// CRITICAL: This package uses Go module isolation to enforce read-only constraints.
// FORBIDDEN IMPORTS:
//   - ANY gRPC client with mutation RPCs (position-keeping, financial-accounting, etc.)
//   - ANY package that can write to databases or external systems
//
// ALLOWED IMPORTS:
//   - Standard library
//   - Read-only types (github.com/shopspring/decimal, etc.)
//   - go.starlark.net (Starlark runtime)
package builtins

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"go.starlark.net/starlark"
)

// PolicyEvaluator compiles and evaluates a CEL expression in one call.
// This callback decouples the builtins package from the parent valuation package,
// avoiding circular imports while enabling Starlark scripts to delegate
// mathematical calculations to CEL.
type PolicyEvaluator func(ctx context.Context, expression string, variables map[string]interface{}) (interface{}, error)

// Registry holds configuration for creating Starlark builtins.
type Registry struct {
	// EvalPolicy evaluates a CEL expression. When set, the run_policy builtin
	// is available in Starlark scripts, enforcing the architectural constraint
	// that all mathematical calculations are performed by CEL, not inline Starlark.
	EvalPolicy PolicyEvaluator
}

// NewRegistry creates a new builtin registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// CreateBuiltins returns a StringDict of builtin functions available in Starlark.
// These functions are read-only and cannot perform mutations.
func (r *Registry) CreateBuiltins() starlark.StringDict {
	dict := starlark.StringDict{
		"Decimal":     starlark.NewBuiltin("Decimal", r.decimalBuiltin),
		"quantity":    starlark.NewBuiltin("quantity", r.quantityBuiltin),
		"record_path": starlark.NewBuiltin("record_path", r.recordPathBuiltin),
	}

	// run_policy delegates mathematical calculations to CEL, enforcing the
	// architectural constraint that Starlark orchestrates and CEL calculates.
	if r.EvalPolicy != nil {
		dict["run_policy"] = starlark.NewBuiltin("run_policy", r.runPolicyBuiltin)
	}

	return dict
}

// decimalBuiltin creates a Decimal value from a string.
// Usage: Decimal("123.45") -> decimal value
func (r *Registry) decimalBuiltin(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackPositionalArgs(fn.Name(), args, kwargs, 1, &s); err != nil {
		return nil, err
	}

	d, err := decimal.NewFromString(s)
	if err != nil {
		return nil, fmt.Errorf("decimal: invalid decimal string: %w", err)
	}

	// For now, return as a Starlark string representation of the decimal
	// In a full implementation, this would be a custom Starlark type wrapping decimal.Decimal
	return starlark.String(d.String()), nil
}

// quantityBuiltin creates a quantity dict.
// Usage: quantity(amount="100.50", instrument="GBP", attributes={...})
func (r *Registry) quantityBuiltin(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		amount     string
		instrument string
		attributes *starlark.Dict
		hasAttrs   bool
	)

	// Parse kwargs
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"amount", &amount,
		"instrument", &instrument,
		"attributes?", &attributes,
	); err != nil {
		return nil, err
	}

	hasAttrs = attributes != nil

	// Validate amount is a valid decimal
	_, err := decimal.NewFromString(amount)
	if err != nil {
		return nil, fmt.Errorf("quantity: invalid amount: %w", err)
	}

	// Build result dict
	result := &starlark.Dict{}
	if err := result.SetKey(starlark.String("amount"), starlark.String(amount)); err != nil {
		return nil, fmt.Errorf("quantity: failed to set amount: %w", err)
	}
	if err := result.SetKey(starlark.String("instrument"), starlark.String(instrument)); err != nil {
		return nil, fmt.Errorf("quantity: failed to set instrument: %w", err)
	}

	if hasAttrs {
		if err := result.SetKey(starlark.String("attributes"), attributes); err != nil {
			return nil, fmt.Errorf("quantity: failed to set attributes: %w", err)
		}
	} else {
		// Empty attributes dict
		if err := result.SetKey(starlark.String("attributes"), &starlark.Dict{}); err != nil {
			return nil, fmt.Errorf("quantity: failed to set empty attributes: %w", err)
		}
	}

	return result, nil
}

// recordPathBuiltin records a calculation step in the audit trail.
// Usage: record_path("Step description", {"key": "value"})
//
// This function stores entries in thread-local storage for later extraction.
// The valuation engine will collect these and populate Analysis.CalculationPath.
func (r *Registry) recordPathBuiltin(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		description string
		data        *starlark.Dict
	)

	if err := starlark.UnpackPositionalArgs(fn.Name(), args, kwargs, 2, &description, &data); err != nil {
		return nil, err
	}

	// Store in thread-local storage
	// The Starlark runtime will extract these entries after execution
	pathEntries, _ := thread.Local("valuation.path_entries").([]PathEntry)
	pathEntries = append(pathEntries, PathEntry{
		Description: description,
		Data:        data,
	})
	thread.SetLocal("valuation.path_entries", pathEntries)

	return starlark.None, nil
}

// runPolicyBuiltin evaluates a CEL expression with the given variables.
// Usage: run_policy(expression="amount * rate", variables={"amount": 100.0, "rate": 0.35})
//
// This is the primary mechanism for mathematical calculations in valuation scripts.
// Starlark scripts MUST use run_policy for arithmetic rather than inline operators,
// ensuring all calculations are CEL-evaluated with cost tracking and audit trails.
func (r *Registry) runPolicyBuiltin(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var expression string
	var variablesDict *starlark.Dict

	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"expression", &expression,
		"variables?", &variablesDict,
	); err != nil {
		return nil, err
	}

	// Build Go map from Starlark dict
	variables := make(map[string]interface{})
	if variablesDict != nil {
		for _, item := range variablesDict.Items() {
			key, ok := item[0].(starlark.String)
			if !ok {
				return nil, fmt.Errorf("%w, got %s", ErrRunPolicyInvalidKey, item[0].Type())
			}
			variables[string(key)] = starlarkToGo(item[1])
		}
	}

	// Get Go context from thread-local storage
	ctx, _ := thread.Local("ctx").(context.Context)
	if ctx == nil {
		ctx = context.Background()
	}

	// Evaluate via CEL PolicyRuntime
	result, err := r.EvalPolicy(ctx, expression, variables)
	if err != nil {
		return nil, fmt.Errorf("run_policy: %w", err)
	}

	return goToStarlark(result), nil
}

// starlarkToGo converts a Starlark value to a Go value for CEL evaluation.
func starlarkToGo(v starlark.Value) interface{} {
	switch val := v.(type) {
	case starlark.String:
		return string(val)
	case starlark.Int:
		if i64, ok := val.Int64(); ok {
			return i64
		}
		return val.String()
	case starlark.Float:
		return float64(val)
	case starlark.Bool:
		return bool(val)
	case starlark.NoneType:
		return nil
	case *starlark.Dict:
		m := make(map[string]interface{})
		for _, item := range val.Items() {
			if key, ok := item[0].(starlark.String); ok {
				m[string(key)] = starlarkToGo(item[1])
			}
		}
		return m
	case *starlark.List:
		list := make([]interface{}, val.Len())
		for i := 0; i < val.Len(); i++ {
			list[i] = starlarkToGo(val.Index(i))
		}
		return list
	default:
		return val.String()
	}
}

// goToStarlark converts a Go value from CEL evaluation to a Starlark value.
func goToStarlark(v interface{}) starlark.Value {
	switch val := v.(type) {
	case float64:
		return starlark.Float(val)
	case int64:
		return starlark.MakeInt64(val)
	case int:
		return starlark.MakeInt(val)
	case string:
		return starlark.String(val)
	case bool:
		return starlark.Bool(val)
	case nil:
		return starlark.None
	default:
		return starlark.String(fmt.Sprintf("%v", val))
	}
}

// ErrRunPolicyInvalidKey is returned when a run_policy variables dict contains a non-string key.
var ErrRunPolicyInvalidKey = errors.New("run_policy: variable keys must be strings")

// PathEntry represents a calculation step recorded via record_path().
type PathEntry struct {
	Description string
	Data        *starlark.Dict
}
