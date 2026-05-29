// Package starlark conversion and validation helpers for translating between
// Starlark values and forecast domain types.
package starlark

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	starlarklib "go.starlark.net/starlark"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// resolveEntryPoint looks up and validates the compute_forecast function in globals.
func resolveEntryPoint(globals starlarklib.StringDict) (*starlarklib.Function, error) {
	computeFn, ok := globals["compute_forecast"]
	if !ok {
		return nil, ErrEntryPointMissing
	}
	fn, ok := computeFn.(*starlarklib.Function)
	if !ok {
		return nil, ErrEntryPointMissing
	}
	return fn, nil
}

// validateForecastPoints checks that forecast points are within horizon, monotonic,
// and aligned to granularity.
func validateForecastPoints(points []ForecastPoint, now time.Time, horizon, granularity time.Duration) error {
	horizonEnd := now.Add(horizon)

	for i, p := range points {
		// Check timestamp is within [now, now+horizon]
		if p.Timestamp.Before(now) || p.Timestamp.After(horizonEnd) {
			return fmt.Errorf("%w: point %d at %v is outside [%v, %v]",
				ErrTimestampOutOfRange, i, p.Timestamp, now, horizonEnd)
		}

		// Check monotonically increasing
		if i > 0 && !p.Timestamp.After(points[i-1].Timestamp) {
			return fmt.Errorf("%w: point %d at %v is not after point %d at %v",
				ErrNonMonotonic, i, p.Timestamp, i-1, points[i-1].Timestamp)
		}

		// Check granularity alignment
		offset := p.Timestamp.Sub(now)
		if granularity > 0 && offset%granularity != 0 {
			return fmt.Errorf("%w: point %d at %v (offset %v) is not aligned to %v granularity",
				ErrGranularityMismatch, i, p.Timestamp, offset, granularity)
		}
	}

	return nil
}

// starlarkToForecastPoints converts the Starlark return value to []ForecastPoint.
func starlarkToForecastPoints(val starlarklib.Value) ([]ForecastPoint, error) {
	list, ok := val.(*starlarklib.List)
	if !ok {
		return nil, fmt.Errorf("%w: got %s, want list", ErrInvalidReturnType, val.Type())
	}

	points := make([]ForecastPoint, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		item := list.Index(i)
		dict, ok := item.(*starlarklib.Dict)
		if !ok {
			return nil, fmt.Errorf("%w: element %d is %s, want dict", ErrInvalidReturnType, i, item.Type())
		}

		point, err := dictToForecastPoint(dict)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		points = append(points, point)
	}

	return points, nil
}

// dictToForecastPoint converts a Starlark dict to a ForecastPoint.
func dictToForecastPoint(dict *starlarklib.Dict) (ForecastPoint, error) {
	var point ForecastPoint

	ts, err := extractPointTimestamp(dict)
	if err != nil {
		return point, err
	}
	point.Timestamp = ts

	val, err := extractPointValue(dict)
	if err != nil {
		return point, err
	}
	point.Value = val

	meta, err := extractPointMetadata(dict)
	if err != nil {
		return point, err
	}
	point.Metadata = meta

	return point, nil
}

// extractPointTimestamp extracts and parses the timestamp from a forecast point dict.
func extractPointTimestamp(dict *starlarklib.Dict) (time.Time, error) {
	tsVal, found, err := dict.Get(starlarklib.String("timestamp"))
	if err != nil {
		return time.Time{}, fmt.Errorf("get timestamp: %w", err)
	}
	if !found {
		return time.Time{}, fmt.Errorf("%w: missing 'timestamp' key", ErrInvalidReturnType)
	}

	switch v := tsVal.(type) {
	case starlarklib.String:
		ts, err := time.Parse(time.RFC3339, string(v))
		if err != nil {
			return time.Time{}, fmt.Errorf("parse timestamp %q: %w", string(v), err)
		}
		return ts, nil
	case starlarklib.Int:
		unixSec, ok := v.Int64()
		if !ok {
			return time.Time{}, fmt.Errorf("%w: timestamp integer too large", ErrInvalidReturnType)
		}
		return time.Unix(unixSec, 0).UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("%w: timestamp must be string (RFC3339) or int (unix seconds)", ErrInvalidReturnType)
	}
}

// extractPointValue extracts and converts the value from a forecast point dict.
func extractPointValue(dict *starlarklib.Dict) (decimal.Decimal, error) {
	valVal, found, err := dict.Get(starlarklib.String("value"))
	if err != nil {
		return decimal.Zero, fmt.Errorf("get value: %w", err)
	}
	if !found {
		return decimal.Zero, fmt.Errorf("%w: missing 'value' key", ErrInvalidReturnType)
	}

	switch v := valVal.(type) {
	case *saga.DecimalValue:
		return v.GetDecimal(), nil
	case starlarklib.String:
		d, err := decimal.NewFromString(string(v))
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse value %q: %w", string(v), err)
		}
		return d, nil
	case starlarklib.Float:
		return decimal.NewFromFloat(float64(v)), nil
	case starlarklib.Int:
		i64, ok := v.Int64()
		if !ok {
			return decimal.Zero, fmt.Errorf("%w: integer value too large for decimal conversion", ErrInvalidReturnType)
		}
		return decimal.NewFromInt(i64), nil
	default:
		return decimal.Zero, fmt.Errorf("%w: value must be Decimal, string, float, or int; got %s", ErrInvalidReturnType, valVal.Type())
	}
}

// extractPointMetadata extracts the optional metadata dict from a forecast point dict.
// Returns an empty map (not nil) when metadata is absent or None.
func extractPointMetadata(dict *starlarklib.Dict) (map[string]string, error) {
	metaVal, found, err := dict.Get(starlarklib.String("metadata"))
	if err != nil {
		return nil, fmt.Errorf("get metadata: %w", err)
	}
	if !found || metaVal == starlarklib.None {
		return make(map[string]string), nil
	}

	metaDict, ok := metaVal.(*starlarklib.Dict)
	if !ok {
		return nil, fmt.Errorf("%w: metadata must be dict, got %s", ErrInvalidReturnType, metaVal.Type())
	}

	result := make(map[string]string)
	for _, item := range metaDict.Items() {
		k, ok := item[0].(starlarklib.String)
		if !ok {
			continue
		}
		// Use type assertion to get the raw string value, avoiding Starlark repr quotes.
		if sv, ok := item[1].(starlarklib.String); ok {
			result[string(k)] = string(sv)
		} else {
			result[string(k)] = item[1].String()
		}
	}
	return result, nil
}

// forecastContextToStarlark converts a ForecastContext to a frozen Starlark dict.
func forecastContextToStarlark(fc *ForecastContext) *starlarklib.Dict {
	ctx := starlarklib.NewDict(5)

	// Convert observations: map[string][]Observation -> dict[string, list[dict]]
	obsDict := starlarklib.NewDict(len(fc.Observations))
	for code, observations := range fc.Observations {
		obsList := make([]starlarklib.Value, 0, len(observations))
		for _, obs := range observations {
			d := starlarklib.NewDict(3)
			_ = d.SetKey(starlarklib.String("timestamp"), starlarklib.String(obs.Timestamp.Format(time.RFC3339)))
			_ = d.SetKey(starlarklib.String("value"), starlarklib.String(obs.Value.String()))
			_ = d.SetKey(starlarklib.String("quality"), starlarklib.String(obs.Quality))
			obsList = append(obsList, d)
		}
		_ = obsDict.SetKey(starlarklib.String(code), starlarklib.NewList(obsList))
	}
	_ = ctx.SetKey(starlarklib.String("observations"), obsDict)

	// Convert reference data
	if fc.ReferenceData != nil {
		refDict := starlarklib.NewDict(3)
		_ = refDict.SetKey(starlarklib.String("node_type"), starlarklib.String(fc.ReferenceData.NodeType))
		_ = refDict.SetKey(starlarklib.String("resolution_key"), starlarklib.String(fc.ReferenceData.ResolutionKey))

		attrs := starlarklib.NewDict(len(fc.ReferenceData.Attributes))
		for k, v := range fc.ReferenceData.Attributes {
			_ = attrs.SetKey(starlarklib.String(k), goToStarlark(v))
		}
		_ = refDict.SetKey(starlarklib.String("attributes"), attrs)
		_ = ctx.SetKey(starlarklib.String("reference_data"), refDict)
	} else {
		_ = ctx.SetKey(starlarklib.String("reference_data"), starlarklib.None)
	}

	// Horizon in seconds
	_ = ctx.SetKey(starlarklib.String("horizon_seconds"), starlarklib.MakeInt64(int64(fc.Horizon.Seconds())))

	// Granularity in seconds
	_ = ctx.SetKey(starlarklib.String("granularity_seconds"), starlarklib.MakeInt64(int64(fc.Granularity.Seconds())))

	// Now as RFC3339
	_ = ctx.SetKey(starlarklib.String("now"), starlarklib.String(fc.Now.Format(time.RFC3339)))

	// Freeze to prevent modification by scripts
	ctx.Freeze()

	return ctx
}

// goToStarlark converts a Go value to a Starlark value.
// Mirrors the saga package's unexported function.
func goToStarlark(v interface{}) starlarklib.Value {
	if v == nil {
		return starlarklib.None
	}

	switch val := v.(type) {
	case string:
		return starlarklib.String(val)
	case int:
		return starlarklib.MakeInt(val)
	case int64:
		return starlarklib.MakeInt64(val)
	case float64:
		return starlarklib.Float(val)
	case bool:
		return starlarklib.Bool(val)
	case []interface{}:
		list := make([]starlarklib.Value, len(val))
		for i, elem := range val {
			list[i] = goToStarlark(elem)
		}
		return starlarklib.NewList(list)
	case map[string]string:
		dict := starlarklib.NewDict(len(val))
		for k, v := range val {
			_ = dict.SetKey(starlarklib.String(k), starlarklib.String(v))
		}
		return dict
	case map[string]interface{}:
		dict := starlarklib.NewDict(len(val))
		for k, v := range val {
			_ = dict.SetKey(starlarklib.String(k), goToStarlark(v))
		}
		return dict
	default:
		return starlarklib.String(fmt.Sprintf("%v", v))
	}
}

// wrapStarlarkError wraps Starlark errors with appropriate package errors.
func wrapStarlarkError(err error) error {
	if err == nil {
		return nil
	}

	var evalErr *starlarklib.EvalError
	if errors.As(err, &evalErr) {
		return errors.Join(saga.ErrExecution, err)
	}

	errStr := err.Error()
	if strings.Contains(errStr, "syntax") ||
		strings.Contains(errStr, "parse") ||
		strings.Contains(errStr, "got ") {
		return errors.Join(ErrValidation, err)
	}

	return errors.Join(saga.ErrExecution, err)
}

// SortForecastPoints sorts forecast points by timestamp.
func SortForecastPoints(points []ForecastPoint) {
	sort.Slice(points, func(i, j int) bool {
		return points[i].Timestamp.Before(points[j].Timestamp)
	})
}
