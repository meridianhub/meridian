package schema

import (
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// convertKwargsToParams converts Starlark kwargs to a Go map.
func convertKwargsToParams(kwargs []starlark.Tuple) (map[string]any, error) {
	params := make(map[string]any, len(kwargs))
	for _, kwarg := range kwargs {
		keyVal, ok := kwarg[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("%w: got %s", ErrDictKeyNotString, kwarg[0].Type())
		}
		key := string(keyVal)
		value, err := starlarkToGoValue(kwarg[1])
		if err != nil {
			return nil, fmt.Errorf("failed to convert parameter %s: %w", key, err)
		}
		params[key] = value
	}
	return params, nil
}

// starlarkToGoValue converts a Starlark value to a Go value.
func starlarkToGoValue(v starlark.Value) (any, error) {
	switch val := v.(type) {
	case starlark.String:
		return string(val), nil
	case starlark.Int:
		return starlarkIntToGo(val), nil
	case starlark.Float:
		return float64(val), nil
	case starlark.Bool:
		return bool(val), nil
	case starlark.NoneType:
		//nolint:nilnil // nil,nil is the correct representation of Starlark None
		return nil, nil
	case *starlark.List:
		return starlarkListToGo(val)
	case *starlark.Dict:
		return starlarkDictToGo(val)
	case *starlarkstruct.Struct:
		return starlarkStructToGo(val)
	default:
		// For custom types, try to convert to string
		return val.String(), nil
	}
}

// starlarkIntToGo converts a Starlark Int to Go int64 or string (for very large ints).
func starlarkIntToGo(val starlark.Int) any {
	if i, ok := val.Int64(); ok {
		return i
	}
	// Fall back to string for very large ints
	return val.String()
}

// starlarkListToGo converts a Starlark List to Go []any.
func starlarkListToGo(val *starlark.List) ([]any, error) {
	result := make([]any, val.Len())
	for i := 0; i < val.Len(); i++ {
		elem, err := starlarkToGoValue(val.Index(i))
		if err != nil {
			return nil, err
		}
		result[i] = elem
	}
	return result, nil
}

// starlarkDictToGo converts a Starlark Dict to Go map[string]any.
func starlarkDictToGo(val *starlark.Dict) (map[string]any, error) {
	result := make(map[string]any)
	for _, item := range val.Items() {
		key, ok := item[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("%w: got %s", ErrDictKeyNotString, item[0].Type())
		}
		value, err := starlarkToGoValue(item[1])
		if err != nil {
			return nil, err
		}
		result[string(key)] = value
	}
	return result, nil
}

// starlarkStructToGo converts a Starlark Struct to Go map[string]any.
func starlarkStructToGo(val *starlarkstruct.Struct) (map[string]any, error) {
	result := make(map[string]any)
	for _, attrName := range val.AttrNames() {
		attrVal, _ := val.Attr(attrName)
		converted, err := starlarkToGoValue(attrVal)
		if err != nil {
			return nil, err
		}
		result[attrName] = converted
	}
	return result, nil
}

// toDecimal converts various Go types to decimal.Decimal.
func toDecimal(v any) (decimal.Decimal, error) {
	switch val := v.(type) {
	case decimal.Decimal:
		return val, nil
	case string:
		return decimal.NewFromString(val)
	case int:
		return decimal.NewFromInt(int64(val)), nil
	case int64:
		return decimal.NewFromInt(val), nil
	case float64:
		return decimal.NewFromFloat(val), nil
	default:
		return decimal.Zero, fmt.Errorf("%w: unsupported type %T", ErrDecimalConversion, v)
	}
}

// goToStarlarkResult converts a Go handler result to a Starlark struct.
// Handler results are expected to be map[string]any which gets converted
// to a branded starlarkstruct.Struct for type-safe field access.
func goToStarlarkResult(handlerName string, result any) (starlark.Value, error) {
	if result == nil {
		return starlark.None, nil
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		// If not a map, convert directly to Starlark value
		return goToStarlarkValue(result)
	}

	// Build struct from map
	members := make(starlark.StringDict, len(resultMap))

	// Sort keys for deterministic output
	keys := make([]string, 0, len(resultMap))
	for k := range resultMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		val, err := goToStarlarkValue(resultMap[key])
		if err != nil {
			return nil, fmt.Errorf("failed to convert result field %s: %w", key, err)
		}
		members[key] = val
	}

	// Create a branded struct with handler name as the type
	// This allows scripts to check type(result) == "position_keeping.initiate_log.Result"
	typeName := handlerName + ".Result"
	return starlarkstruct.FromStringDict(starlark.String(typeName), members), nil
}

// goToStarlarkValue converts a Go value to a Starlark value.
func goToStarlarkValue(v any) (starlark.Value, error) {
	if v == nil {
		return starlark.None, nil
	}

	switch val := v.(type) {
	case string:
		return starlark.String(val), nil
	case int:
		return starlark.MakeInt(val), nil
	case int64:
		return starlark.MakeInt64(val), nil
	case int32:
		return starlark.MakeInt(int(val)), nil
	case uint32:
		return starlark.MakeUint(uint(val)), nil
	case float64:
		return starlark.Float(val), nil
	case bool:
		return starlark.Bool(val), nil
	case decimal.Decimal:
		// Convert Decimal to string for lossless representation in Starlark
		// Starlark scripts should use Decimal() builtin to work with these values
		return starlark.String(val.String()), nil
	case []any:
		return goSliceToStarlark(val)
	case []string:
		return goStringSliceToStarlark(val), nil
	case map[string]any:
		return goMapToStarlark(val)
	default:
		// Try to convert to string as fallback
		return starlark.String(fmt.Sprintf("%v", v)), nil
	}
}

// goSliceToStarlark converts a Go []any to a Starlark List.
func goSliceToStarlark(val []any) (*starlark.List, error) {
	list := make([]starlark.Value, len(val))
	for i, elem := range val {
		converted, err := goToStarlarkValue(elem)
		if err != nil {
			return nil, err
		}
		list[i] = converted
	}
	return starlark.NewList(list), nil
}

// goStringSliceToStarlark converts a Go []string to a Starlark List.
func goStringSliceToStarlark(val []string) *starlark.List {
	list := make([]starlark.Value, len(val))
	for i, elem := range val {
		list[i] = starlark.String(elem)
	}
	return starlark.NewList(list)
}

// goMapToStarlark converts a Go map[string]any to a Starlark Dict.
func goMapToStarlark(val map[string]any) (*starlark.Dict, error) {
	dict := starlark.NewDict(len(val))
	for k, v := range val {
		converted, err := goToStarlarkValue(v)
		if err != nil {
			return nil, err
		}
		if err := dict.SetKey(starlark.String(k), converted); err != nil {
			return nil, err
		}
	}
	return dict, nil
}
