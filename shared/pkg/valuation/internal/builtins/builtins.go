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
	"fmt"

	"github.com/shopspring/decimal"
	"go.starlark.net/starlark"
)

// Registry holds configuration for creating Starlark builtins.
type Registry struct {
	// Future: Add PolicyResolver, MarketDataClient (read-only) when needed
}

// NewRegistry creates a new builtin registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// CreateBuiltins returns a StringDict of builtin functions available in Starlark.
// These functions are read-only and cannot perform mutations.
func (r *Registry) CreateBuiltins() starlark.StringDict {
	return starlark.StringDict{
		"Decimal":     starlark.NewBuiltin("Decimal", r.decimalBuiltin),
		"quantity":    starlark.NewBuiltin("quantity", r.quantityBuiltin),
		"record_path": starlark.NewBuiltin("record_path", r.recordPathBuiltin),
		// Future: "run_policy", "market_data" when integrated with dependencies
	}
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

// PathEntry represents a calculation step recorded via record_path().
type PathEntry struct {
	Description string
	Data        *starlark.Dict
}
