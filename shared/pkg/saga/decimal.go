package saga

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Decimal type errors.
var (
	// ErrDecimalParse is returned when a string cannot be parsed as a decimal.
	ErrDecimalParse = errors.New("invalid decimal value")

	// ErrDecimalTypeMismatch is returned when an operation involves non-Decimal types.
	ErrDecimalTypeMismatch = errors.New("decimal type mismatch")

	// ErrDivisionByZero is returned when dividing by zero.
	ErrDivisionByZero = errors.New("division by zero")

	// ErrUnsupportedDecimalOperation is returned for unsupported operations.
	ErrUnsupportedDecimalOperation = errors.New("unsupported decimal operation")

	// ErrDecimalArgCount is returned when Decimal() receives wrong number of arguments.
	ErrDecimalArgCount = errors.New("decimal: expected 1 argument")

	// ErrDecimalArgType is returned when Decimal() receives a non-string argument.
	ErrDecimalArgType = errors.New("decimal: expected string argument")
)

// DecimalValue is a Starlark value wrapping shopspring/decimal for arbitrary precision.
// It implements starlark.Value and starlark.HasBinary for operator overloading.
type DecimalValue struct {
	decimal decimal.Decimal
	frozen  bool
}

// Ensure DecimalValue implements required interfaces.
var (
	_ starlark.Value          = (*DecimalValue)(nil)
	_ starlark.HasBinary      = (*DecimalValue)(nil)
	_ starlark.Comparable     = (*DecimalValue)(nil)
	_ starlark.TotallyOrdered = (*DecimalValue)(nil)
)

// NewDecimalValue creates a new DecimalValue from a string.
// Only string input is accepted to prevent floating-point precision loss.
func NewDecimalValue(s string) (*DecimalValue, error) {
	if s == "" {
		return nil, fmt.Errorf("%w: empty string", ErrDecimalParse)
	}

	d, err := decimal.NewFromString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecimalParse, err)
	}

	return &DecimalValue{decimal: d}, nil
}

// String returns the string representation of the decimal.
func (d *DecimalValue) String() string {
	return d.decimal.String()
}

// Type returns the type name "Decimal".
func (d *DecimalValue) Type() string {
	return "Decimal"
}

// Freeze marks the value as frozen (immutable).
func (d *DecimalValue) Freeze() {
	d.frozen = true
}

// Truth returns True for non-zero values, False for zero.
func (d *DecimalValue) Truth() starlark.Bool {
	return starlark.Bool(!d.decimal.IsZero())
}

// Hash returns a hash value for the decimal.
func (d *DecimalValue) Hash() (uint32, error) {
	// Use string representation for hashing
	s := d.decimal.String()
	var h uint32
	for i := 0; i < len(s); i++ {
		h = h*31 + uint32(s[i])
	}
	return h, nil
}

// CompareSameType compares two DecimalValue instances.
// Returns -1 if d < other, 0 if equal, 1 if d > other.
//
//nolint:exhaustive,nolintlint // We only handle comparison operators; other tokens are invalid
func (d *DecimalValue) CompareSameType(op syntax.Token, y starlark.Value, _ int) (bool, error) {
	other, ok := y.(*DecimalValue)
	if !ok {
		return false, fmt.Errorf("%w: cannot compare Decimal with %s", ErrDecimalTypeMismatch, y.Type())
	}

	cmp := d.decimal.Cmp(other.decimal)

	switch op {
	case syntax.EQL:
		return cmp == 0, nil
	case syntax.NEQ:
		return cmp != 0, nil
	case syntax.LT:
		return cmp < 0, nil
	case syntax.LE:
		return cmp <= 0, nil
	case syntax.GT:
		return cmp > 0, nil
	case syntax.GE:
		return cmp >= 0, nil
	default:
		return false, fmt.Errorf("%w: %v", ErrUnsupportedDecimalOperation, op)
	}
}

// Cmp compares d to y. Returns -1, 0, or +1.
func (d *DecimalValue) Cmp(y starlark.Value, _ int) (int, error) {
	other, ok := y.(*DecimalValue)
	if !ok {
		return 0, fmt.Errorf("%w: cannot compare Decimal with %s", ErrDecimalTypeMismatch, y.Type())
	}
	return d.decimal.Cmp(other.decimal), nil
}

// Binary implements binary operators for Decimal values.
// Supports +, -, *, / operations.
// The side parameter indicates whether d is on the left (Left) or right (Right) of the operator.
//
//nolint:exhaustive,nolintlint // We only handle arithmetic operators; other tokens are invalid
func (d *DecimalValue) Binary(op syntax.Token, y starlark.Value, side starlark.Side) (starlark.Value, error) {
	// Only operate on Decimal types
	other, ok := y.(*DecimalValue)
	if !ok {
		return nil, fmt.Errorf("%w: cannot perform %v between Decimal and %s", ErrDecimalTypeMismatch, op, y.Type())
	}

	var result decimal.Decimal

	switch op {
	case syntax.PLUS:
		result = d.decimal.Add(other.decimal)
	case syntax.MINUS:
		if side == starlark.Left {
			result = d.decimal.Sub(other.decimal)
		} else {
			result = other.decimal.Sub(d.decimal)
		}
	case syntax.STAR:
		result = d.decimal.Mul(other.decimal)
	case syntax.SLASH:
		if side == starlark.Left {
			if other.decimal.IsZero() {
				return nil, ErrDivisionByZero
			}
			result = d.decimal.Div(other.decimal)
		} else {
			if d.decimal.IsZero() {
				return nil, ErrDivisionByZero
			}
			result = other.decimal.Div(d.decimal)
		}
	default:
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedDecimalOperation, op)
	}

	return &DecimalValue{decimal: result}, nil
}

// DecimalBuiltin returns the Starlark builtin function for creating Decimals.
// Usage: Decimal("123.45")
// Only accepts string arguments to prevent floating-point precision loss.
func DecimalBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("Decimal", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("%w, got %d", ErrDecimalArgCount, len(args))
		}

		// Only accept string input - reject float and int to enforce string-only construction
		s, ok := args[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("%w, got %s (use Decimal(\"123.45\") not Decimal(123.45))", ErrDecimalArgType, args[0].Type())
		}

		return NewDecimalValue(string(s))
	})
}

// GetDecimal returns the underlying shopspring/decimal value.
func (d *DecimalValue) GetDecimal() decimal.Decimal {
	return d.decimal
}
