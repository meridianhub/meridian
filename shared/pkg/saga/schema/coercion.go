// Package schema provides Starlark service module generation from handler schemas.
package schema

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/shopspring/decimal"
)

// Coercion errors.
var (
	// ErrOverflow is returned when a numeric value exceeds the target type's range.
	ErrOverflow = errors.New("value out of range")

	// ErrTypeCoercion is returned when a value cannot be coerced to the target type.
	ErrTypeCoercion = errors.New("cannot coerce value")
)

// CoerceValue converts a Go value to the expected type based on the schema field type.
// Returns nil unchanged for nil input (representing Starlark None / optional absent params).
func CoerceValue(val any, schemaType FieldType) (any, error) {
	if val == nil {
		//nolint:nilnil // nil,nil is the correct representation for absent optional parameters
		return nil, nil
	}

	switch schemaType {
	case TypeInt32:
		return coerceInt32(val)
	case TypeInt64:
		return coerceInt64(val)
	case TypeUint32:
		return coerceUint32(val)
	case TypeDecimal:
		return coerceDecimalValue(val)
	case TypeBool:
		return coerceBoolValue(val)
	case TypeString, TypeEnum, TypeUUID:
		return coerceStringValue(val)
	case TypeArray, TypeMap:
		// Pass through without coercion; these complex types are
		// already converted by starlarkToGoValue.
		return val, nil
	default:
		return nil, fmt.Errorf("%w: unknown schema type %q", ErrTypeCoercion, schemaType)
	}
}

// CoerceParams coerces all parameters in the map according to the handler schema.
// This replaces the previous Decimal-only convertDecimalParams with full type coercion.
func CoerceParams(params map[string]any, handlerDef *HandlerDef) error {
	for paramName, fieldDef := range handlerDef.Params {
		val, ok := params[paramName]
		if !ok {
			continue
		}

		coerced, err := CoerceValue(val, fieldDef.Type)
		if err != nil {
			return fmt.Errorf("parameter %s (expected %s): %w", paramName, fieldDef.Type, err)
		}
		params[paramName] = coerced
	}
	return nil
}

// coerceInt32 converts a value to int32 with overflow protection.
func coerceInt32(val any) (int32, error) {
	switch v := val.(type) {
	case int64:
		if v < math.MinInt32 || v > math.MaxInt32 {
			return 0, fmt.Errorf("%w for int32: %d is outside [%d, %d]",
				ErrOverflow, v, int64(math.MinInt32), int64(math.MaxInt32))
		}
		return int32(v), nil
	case int:
		return coerceInt32(int64(v))
	default:
		return 0, fmt.Errorf("%w: cannot convert %T to int32", ErrTypeCoercion, val)
	}
}

// coerceInt64 converts a value to int64 with overflow protection.
func coerceInt64(val any) (int64, error) {
	switch v := val.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case string:
		// Starlark represents very large ints as strings (from starlarkIntToGo).
		i, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w for int64: %q does not fit in int64", ErrOverflow, v)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("%w: cannot convert %T to int64", ErrTypeCoercion, val)
	}
}

// coerceUint32 converts a value to uint32 with overflow protection.
func coerceUint32(val any) (uint32, error) {
	switch v := val.(type) {
	case int64:
		if v < 0 || v > math.MaxUint32 {
			return 0, fmt.Errorf("%w for uint32: %d is outside [0, %d]",
				ErrOverflow, v, uint64(math.MaxUint32))
		}
		return uint32(v), nil
	case int:
		return coerceUint32(int64(v))
	default:
		return 0, fmt.Errorf("%w: cannot convert %T to uint32", ErrTypeCoercion, val)
	}
}

// coerceDecimalValue converts a value to decimal.Decimal.
// This wraps the existing toDecimal function for the CoerceValue dispatcher.
func coerceDecimalValue(val any) (decimal.Decimal, error) {
	return toDecimal(val)
}

// coerceBoolValue converts a value to bool.
func coerceBoolValue(val any) (bool, error) {
	b, ok := val.(bool)
	if !ok {
		return false, fmt.Errorf("%w: cannot convert %T to bool", ErrTypeCoercion, val)
	}
	return b, nil
}

// coerceStringValue validates that a value is a string.
func coerceStringValue(val any) (string, error) {
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("%w: cannot convert %T to string", ErrTypeCoercion, val)
	}
	return s, nil
}
