package starlark

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	starlarklib "go.starlark.net/starlark"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// --- SortForecastPoints tests ---

func TestSortForecastPoints_AlreadySorted(t *testing.T) {
	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t1.Add(2 * time.Hour)
	points := []ForecastPoint{
		{Timestamp: t1, Value: decimal.NewFromInt(1)},
		{Timestamp: t2, Value: decimal.NewFromInt(2)},
		{Timestamp: t3, Value: decimal.NewFromInt(3)},
	}
	SortForecastPoints(points)
	assert.True(t, points[0].Timestamp.Equal(t1))
	assert.True(t, points[1].Timestamp.Equal(t2))
	assert.True(t, points[2].Timestamp.Equal(t3))
}

func TestSortForecastPoints_Unsorted(t *testing.T) {
	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t1.Add(2 * time.Hour)
	points := []ForecastPoint{
		{Timestamp: t3, Value: decimal.NewFromInt(3)},
		{Timestamp: t1, Value: decimal.NewFromInt(1)},
		{Timestamp: t2, Value: decimal.NewFromInt(2)},
	}
	SortForecastPoints(points)
	assert.True(t, points[0].Timestamp.Equal(t1))
	assert.True(t, points[1].Timestamp.Equal(t2))
	assert.True(t, points[2].Timestamp.Equal(t3))
}

func TestSortForecastPoints_Empty(_ *testing.T) {
	var points []ForecastPoint
	SortForecastPoints(points) // should not panic
}

// --- goToStarlark tests ---

func TestGoToStarlark_Nil(t *testing.T) {
	val := goToStarlark(nil)
	assert.Equal(t, starlarklib.None, val)
}

func TestGoToStarlark_String(t *testing.T) {
	val := goToStarlark("hello")
	assert.Equal(t, starlarklib.String("hello"), val)
}

func TestGoToStarlark_Int(t *testing.T) {
	val := goToStarlark(42)
	assert.Equal(t, starlarklib.MakeInt(42), val)
}

func TestGoToStarlark_Int64(t *testing.T) {
	val := goToStarlark(int64(100))
	assert.Equal(t, starlarklib.MakeInt64(100), val)
}

func TestGoToStarlark_Float64(t *testing.T) {
	val := goToStarlark(3.14)
	assert.Equal(t, starlarklib.Float(3.14), val)
}

func TestGoToStarlark_Bool(t *testing.T) {
	val := goToStarlark(true)
	assert.Equal(t, starlarklib.Bool(true), val)
}

func TestGoToStarlark_SliceOfInterface(t *testing.T) {
	val := goToStarlark([]interface{}{"a", 1})
	list, ok := val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 2, list.Len())
}

func TestGoToStarlark_MapStringString(t *testing.T) {
	val := goToStarlark(map[string]string{"key": "value"})
	dict, ok := val.(*starlarklib.Dict)
	require.True(t, ok)
	assert.Equal(t, 1, dict.Len())
}

func TestGoToStarlark_MapStringInterface(t *testing.T) {
	val := goToStarlark(map[string]interface{}{"nested": "value"})
	dict, ok := val.(*starlarklib.Dict)
	require.True(t, ok)
	assert.Equal(t, 1, dict.Len())
}

func TestGoToStarlark_UnknownType(t *testing.T) {
	type customType struct{ X int }
	val := goToStarlark(customType{X: 5})
	// Default branch: fmt.Sprintf("%v", v) -> String
	_, ok := val.(starlarklib.String)
	assert.True(t, ok)
}

// --- toDecimal tests ---

func TestToDecimal_DecimalValue(t *testing.T) {
	dv, err := saga.NewDecimalValue("42.5")
	require.NoError(t, err)
	result, err := toDecimal(dv)
	require.NoError(t, err)
	assert.Equal(t, "42.5", result.String())
}

func TestToDecimal_StarlarkInt(t *testing.T) {
	result, err := toDecimal(starlarklib.MakeInt(100))
	require.NoError(t, err)
	assert.Equal(t, decimal.NewFromInt(100), result)
}

func TestToDecimal_StarlarkFloat(t *testing.T) {
	result, err := toDecimal(starlarklib.Float(2.5))
	require.NoError(t, err)
	assert.Equal(t, "2.5", result.String())
}

func TestToDecimal_StarlarkString(t *testing.T) {
	result, err := toDecimal(starlarklib.String("123.45"))
	require.NoError(t, err)
	assert.Equal(t, "123.45", result.String())
}

func TestToDecimal_StarlarkString_Invalid(t *testing.T) {
	_, err := toDecimal(starlarklib.String("not-a-number"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConversion)
}

func TestToDecimal_UnknownType(t *testing.T) {
	_, err := toDecimal(starlarklib.Bool(true))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConversion)
}

// --- toFloat64 tests ---

func TestToFloat64_StarlarkInt(t *testing.T) {
	result, err := toFloat64(starlarklib.MakeInt(50))
	require.NoError(t, err)
	assert.Equal(t, float64(50), result)
}

func TestToFloat64_StarlarkFloat(t *testing.T) {
	result, err := toFloat64(starlarklib.Float(3.14))
	require.NoError(t, err)
	assert.InDelta(t, 3.14, result, 0.001)
}

func TestToFloat64_DecimalValue(t *testing.T) {
	dv, err := saga.NewDecimalValue("7.5")
	require.NoError(t, err)
	result, err := toFloat64(dv)
	require.NoError(t, err)
	assert.InDelta(t, 7.5, result, 0.001)
}

func TestToFloat64_UnknownType(t *testing.T) {
	_, err := toFloat64(starlarklib.String("3.14"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConversion)
}

// --- extractPointTimestamp tests ---

func TestExtractPointTimestamp_RFC3339String(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("timestamp"), starlarklib.String("2025-01-15T10:00:00Z"))

	ts, err := extractPointTimestamp(dict)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC), ts)
}

func TestExtractPointTimestamp_UnixInt(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("timestamp"), starlarklib.MakeInt(1736899200))

	ts, err := extractPointTimestamp(dict)
	require.NoError(t, err)
	assert.Equal(t, time.Unix(1736899200, 0).UTC(), ts)
}

func TestExtractPointTimestamp_Missing(t *testing.T) {
	dict := starlarklib.NewDict(0)
	_, err := extractPointTimestamp(dict)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidReturnType)
}

func TestExtractPointTimestamp_InvalidRFC3339(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("timestamp"), starlarklib.String("not-a-timestamp"))

	_, err := extractPointTimestamp(dict)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse timestamp")
}

func TestExtractPointTimestamp_WrongType(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("timestamp"), starlarklib.Bool(true))

	_, err := extractPointTimestamp(dict)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidReturnType)
}
