package starlark

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/shopspring/decimal"
	starlarklib "go.starlark.net/starlark"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// Builtin errors.
var (
	ErrArgCount         = errors.New("wrong number of arguments")
	ErrArgType          = errors.New("wrong argument type")
	ErrEmptyList        = errors.New("empty list")
	ErrOutOfRange       = errors.New("value out of range")
	ErrConversion       = errors.New("conversion error")
	ErrInvalidTimestamp = errors.New("invalid timestamp")
)

// newForecastBuiltins creates the restricted Starlark environment for forecasting
// scripts, extending the saga builtins with forecasting-specific functions.
//
// Included builtins:
//   - Safe stdlib: True, False, None, len, str, int, float, bool, list, dict,
//     tuple, range, enumerate, zip, sorted, reversed, min, max, abs, any, all,
//     hasattr, getattr, dir, type, repr, hash
//   - Forecasting: avg, sum, percentile, filter_by_hour, group_by_hour, duration
//   - Decimal: Decimal() for arbitrary-precision arithmetic
//
func newForecastBuiltins(logger *slog.Logger) starlarklib.StringDict {
	if logger == nil {
		logger = slog.Default()
	}

	builtins := make(starlarklib.StringDict)

	// Copy safe builtins from Starlark Universe
	safeFunctions := []string{
		"True", "False", "None",
		"len", "str", "int", "float", "bool",
		"list", "dict", "tuple", "range",
		"enumerate", "zip", "sorted", "reversed",
		"min", "max", "abs", "any", "all",
		"hasattr", "getattr", "dir", "type", "repr", "hash",
	}
	for _, name := range safeFunctions {
		if val, ok := starlarklib.Universe[name]; ok {
			builtins[name] = val
		}
	}

	// Override print to route to logger
	builtins["print"] = starlarklib.NewBuiltin("print", func(thread *starlarklib.Thread, _ *starlarklib.Builtin, args starlarklib.Tuple, _ []starlarklib.Tuple) (starlarklib.Value, error) {
		var msg string
		for i, arg := range args {
			if i > 0 {
				msg += " "
			}
			msg += arg.String()
		}
		logger.Info("forecast script print", "message", msg, "thread", thread.Name)
		return starlarklib.None, nil
	})

	// Decimal builtin
	builtins["Decimal"] = saga.DecimalBuiltin()

	// --- Statistical builtins ---
	builtins["avg"] = starlarklib.NewBuiltin("avg", avgBuiltin)
	builtins["sum"] = starlarklib.NewBuiltin("sum", sumBuiltin)
	builtins["percentile"] = starlarklib.NewBuiltin("percentile", percentileBuiltin)

	// --- Observation helper builtins ---
	builtins["filter_by_hour"] = starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)
	builtins["group_by_hour"] = starlarklib.NewBuiltin("group_by_hour", groupByHourBuiltin)

	// --- Time utility builtins ---
	builtins["duration"] = starlarklib.NewBuiltin("duration", durationBuiltin)
	builtins["add_seconds"] = starlarklib.NewBuiltin("add_seconds", addSecondsBuiltin)

	return builtins
}

// avgBuiltin computes the arithmetic mean of a list of numeric values.
func avgBuiltin(_ *starlarklib.Thread, _ *starlarklib.Builtin, args starlarklib.Tuple, _ []starlarklib.Tuple) (starlarklib.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("avg: %w: expected 1, got %d", ErrArgCount, len(args))
	}
	vals, err := toDecimalSlice(args[0])
	if err != nil {
		return nil, fmt.Errorf("avg: %w", err)
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("avg: %w", ErrEmptyList)
	}
	sum := decimal.Zero
	for _, v := range vals {
		sum = sum.Add(v)
	}
	mean := sum.Div(decimal.NewFromInt(int64(len(vals))))
	dv, err := saga.NewDecimalValue(mean.String())
	if err != nil {
		return nil, fmt.Errorf("avg: %w", err)
	}
	return dv, nil
}

// sumBuiltin computes the sum of a list of numeric values.
func sumBuiltin(_ *starlarklib.Thread, _ *starlarklib.Builtin, args starlarklib.Tuple, _ []starlarklib.Tuple) (starlarklib.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("sum: %w: expected 1, got %d", ErrArgCount, len(args))
	}
	vals, err := toDecimalSlice(args[0])
	if err != nil {
		return nil, fmt.Errorf("sum: %w", err)
	}
	total := decimal.Zero
	for _, v := range vals {
		total = total.Add(v)
	}
	dv, err := saga.NewDecimalValue(total.String())
	if err != nil {
		return nil, fmt.Errorf("sum: %w", err)
	}
	return dv, nil
}

// percentileBuiltin computes the p-th percentile of a list of numeric values.
// Usage: percentile(values, p) where p is 0-100.
func percentileBuiltin(_ *starlarklib.Thread, _ *starlarklib.Builtin, args starlarklib.Tuple, _ []starlarklib.Tuple) (starlarklib.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("percentile: %w: expected 2, got %d", ErrArgCount, len(args))
	}
	vals, err := toDecimalSlice(args[0])
	if err != nil {
		return nil, fmt.Errorf("percentile: %w", err)
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("percentile: %w", ErrEmptyList)
	}

	p, err := toFloat64(args[1])
	if err != nil {
		return nil, fmt.Errorf("percentile: p must be numeric: %w", err)
	}
	if p < 0 || p > 100 {
		return nil, fmt.Errorf("percentile: %w: p must be 0-100, got %v", ErrOutOfRange, p)
	}

	// Sort values
	sorted := make([]decimal.Decimal, len(vals))
	copy(sorted, vals)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].LessThan(sorted[j])
	})

	// Linear interpolation
	rank := (p / 100.0) * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper || upper >= len(sorted) {
		dv, err := saga.NewDecimalValue(sorted[lower].String())
		if err != nil {
			return nil, fmt.Errorf("percentile: %w", err)
		}
		return dv, nil
	}

	// Interpolate
	frac := decimal.NewFromFloat(rank - float64(lower))
	diff := sorted[upper].Sub(sorted[lower])
	result := sorted[lower].Add(diff.Mul(frac))

	dv, err := saga.NewDecimalValue(result.String())
	if err != nil {
		return nil, fmt.Errorf("percentile: %w", err)
	}
	return dv, nil
}

// filterByHourBuiltin filters observations to those with matching hour-of-day.
// Usage: filter_by_hour(observations, hour)
// observations is a list of dicts with "timestamp" keys (RFC3339 strings).
func filterByHourBuiltin(_ *starlarklib.Thread, _ *starlarklib.Builtin, args starlarklib.Tuple, _ []starlarklib.Tuple) (starlarklib.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("filter_by_hour: %w: expected 2, got %d", ErrArgCount, len(args))
	}

	obsList, ok := args[0].(*starlarklib.List)
	if !ok {
		return nil, fmt.Errorf("filter_by_hour: %w: first argument must be list, got %s", ErrArgType, args[0].Type())
	}

	hourVal, ok := args[1].(starlarklib.Int)
	if !ok {
		return nil, fmt.Errorf("filter_by_hour: %w: second argument must be int, got %s", ErrArgType, args[1].Type())
	}
	hour, ok := hourVal.Int64()
	if !ok || hour < 0 || hour > 23 {
		return nil, fmt.Errorf("filter_by_hour: %w: hour must be 0-23", ErrOutOfRange)
	}

	result := make([]starlarklib.Value, 0)
	for i := 0; i < obsList.Len(); i++ {
		item := obsList.Index(i)
		ts, ok := extractTimestamp(item)
		if !ok {
			continue
		}
		if int64(ts.Hour()) == hour {
			result = append(result, item)
		}
	}

	return starlarklib.NewList(result), nil
}

// groupByHourBuiltin groups observations by hour-of-day.
// Usage: group_by_hour(observations)
// Returns a dict mapping hour (int) -> list of observation dicts.
func groupByHourBuiltin(_ *starlarklib.Thread, _ *starlarklib.Builtin, args starlarklib.Tuple, _ []starlarklib.Tuple) (starlarklib.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("group_by_hour: %w: expected 1, got %d", ErrArgCount, len(args))
	}

	obsList, ok := args[0].(*starlarklib.List)
	if !ok {
		return nil, fmt.Errorf("group_by_hour: %w: argument must be list, got %s", ErrArgType, args[0].Type())
	}

	groups := make(map[int][]starlarklib.Value)
	for i := 0; i < obsList.Len(); i++ {
		item := obsList.Index(i)
		ts, ok := extractTimestamp(item)
		if !ok {
			continue
		}
		groups[ts.Hour()] = append(groups[ts.Hour()], item)
	}

	result := starlarklib.NewDict(len(groups))
	for hour, items := range groups {
		_ = result.SetKey(starlarklib.MakeInt(hour), starlarklib.NewList(items))
	}
	return result, nil
}

// extractTimestamp extracts a time.Time from a Starlark dict's "timestamp" key.
// Returns false if the dict doesn't have a valid RFC3339 timestamp.
func extractTimestamp(val starlarklib.Value) (time.Time, bool) {
	dict, ok := val.(*starlarklib.Dict)
	if !ok {
		return time.Time{}, false
	}
	tsVal, found, err := dict.Get(starlarklib.String("timestamp"))
	if err != nil || !found {
		return time.Time{}, false
	}
	tsStr, ok := tsVal.(starlarklib.String)
	if !ok {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, string(tsStr))
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// durationBuiltin creates a duration value in seconds.
// Usage: duration(hours=0, minutes=0, seconds=0) -> int
func durationBuiltin(_ *starlarklib.Thread, b *starlarklib.Builtin, args starlarklib.Tuple, kwargs []starlarklib.Tuple) (starlarklib.Value, error) {
	var hours, minutes, seconds int
	if err := starlarklib.UnpackArgs(b.Name(), args, kwargs,
		"hours?", &hours,
		"minutes?", &minutes,
		"seconds?", &seconds,
	); err != nil {
		return nil, err
	}

	total := hours*3600 + minutes*60 + seconds
	return starlarklib.MakeInt(total), nil
}

// addSecondsBuiltin adds seconds to an RFC3339 timestamp string.
// Usage: add_seconds("2026-02-10T00:00:00Z", 3600) -> "2026-02-10T01:00:00Z"
func addSecondsBuiltin(_ *starlarklib.Thread, _ *starlarklib.Builtin, args starlarklib.Tuple, _ []starlarklib.Tuple) (starlarklib.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("add_seconds: %w: expected 2, got %d", ErrArgCount, len(args))
	}

	tsStr, ok := args[0].(starlarklib.String)
	if !ok {
		return nil, fmt.Errorf("add_seconds: %w: first argument must be string, got %s", ErrArgType, args[0].Type())
	}

	secsVal, ok := args[1].(starlarklib.Int)
	if !ok {
		return nil, fmt.Errorf("add_seconds: %w: second argument must be int, got %s", ErrArgType, args[1].Type())
	}
	secs, ok := secsVal.Int64()
	if !ok {
		return nil, fmt.Errorf("add_seconds: %w: seconds value too large", ErrOutOfRange)
	}

	ts, err := time.Parse(time.RFC3339, string(tsStr))
	if err != nil {
		return nil, fmt.Errorf("add_seconds: %w: %w", ErrInvalidTimestamp, err)
	}

	result := ts.Add(time.Duration(secs) * time.Second)
	return starlarklib.String(result.Format(time.RFC3339)), nil
}

// toDecimalSlice converts a Starlark list/tuple of numeric values to []decimal.Decimal.
func toDecimalSlice(val starlarklib.Value) ([]decimal.Decimal, error) {
	var items []starlarklib.Value

	switch v := val.(type) {
	case *starlarklib.List:
		items = make([]starlarklib.Value, v.Len())
		for i := 0; i < v.Len(); i++ {
			items[i] = v.Index(i)
		}
	case starlarklib.Tuple:
		items = []starlarklib.Value(v)
	default:
		return nil, fmt.Errorf("%w: expected list or tuple, got %s", ErrArgType, val.Type())
	}

	result := make([]decimal.Decimal, 0, len(items))
	for i, item := range items {
		d, err := toDecimal(item)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		result = append(result, d)
	}
	return result, nil
}

// toDecimal converts a single Starlark value to decimal.Decimal.
func toDecimal(val starlarklib.Value) (decimal.Decimal, error) {
	switch v := val.(type) {
	case *saga.DecimalValue:
		return v.GetDecimal(), nil
	case starlarklib.Int:
		i64, ok := v.Int64()
		if !ok {
			return decimal.Zero, fmt.Errorf("%w: integer too large", ErrConversion)
		}
		return decimal.NewFromInt(i64), nil
	case starlarklib.Float:
		return decimal.NewFromFloat(float64(v)), nil
	case starlarklib.String:
		d, err := decimal.NewFromString(string(v))
		if err != nil {
			return decimal.Zero, fmt.Errorf("%w: cannot parse %q as decimal", ErrConversion, string(v))
		}
		return d, nil
	default:
		return decimal.Zero, fmt.Errorf("%w: cannot convert %s to decimal", ErrConversion, val.Type())
	}
}

// toFloat64 converts a Starlark numeric value to float64.
func toFloat64(val starlarklib.Value) (float64, error) {
	switch v := val.(type) {
	case starlarklib.Int:
		i64, ok := v.Int64()
		if !ok {
			return 0, fmt.Errorf("%w: integer too large", ErrConversion)
		}
		return float64(i64), nil
	case starlarklib.Float:
		return float64(v), nil
	case *saga.DecimalValue:
		f, _ := v.GetDecimal().Float64()
		return f, nil
	default:
		return 0, fmt.Errorf("%w: expected numeric type, got %s", ErrConversion, val.Type())
	}
}
