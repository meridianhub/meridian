package starlark

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	starlarklib "go.starlark.net/starlark"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// --- avgBuiltin error paths ---

func TestAvgBuiltin_WrongArgCount(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("avg", avgBuiltin)

	// No args
	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong number of arguments")

	// Two args
	_, err = starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.NewList(nil), starlarklib.MakeInt(1)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong number of arguments")
}

func TestAvgBuiltin_EmptyList(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("avg", avgBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.NewList(nil)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty list")
}

func TestAvgBuiltin_NonListArg(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("avg", avgBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.String("not a list")}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected list or tuple")
}

func TestAvgBuiltin_InvalidElementInList(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("avg", avgBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{starlarklib.Bool(true)})
	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "avg")
}

func TestAvgBuiltin_WithTuple(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("avg", avgBuiltin)

	tuple := starlarklib.Tuple{starlarklib.MakeInt(10), starlarklib.MakeInt(20)}
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{tuple}, nil)
	require.NoError(t, err)
	dv, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "15", dv.GetDecimal().String())
}

func TestAvgBuiltin_WithFloats(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("avg", avgBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{starlarklib.Float(1.0), starlarklib.Float(2.0), starlarklib.Float(3.0)})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	dv, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "2", dv.GetDecimal().String())
}

// --- sumBuiltin error paths ---

func TestSumBuiltin_WrongArgCount(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("sum", sumBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong number of arguments")
}

func TestSumBuiltin_NonListArg(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("sum", sumBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.MakeInt(42)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected list or tuple")
}

func TestSumBuiltin_EmptyList(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("sum", sumBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.NewList(nil)}, nil)
	require.NoError(t, err)
	dv, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "0", dv.GetDecimal().String())
}

func TestSumBuiltin_WithStringDecimals(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("sum", sumBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.String("10.5"),
		starlarklib.String("20.5"),
	})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	dv, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "31", dv.GetDecimal().String())
}

// --- percentileBuiltin error paths ---

func TestPercentileBuiltin_WrongArgCount(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	// One arg
	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.NewList(nil)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong number of arguments")

	// Three args
	_, err = starlarklib.Call(thread, b, starlarklib.Tuple{
		starlarklib.NewList(nil), starlarklib.MakeInt(50), starlarklib.MakeInt(1),
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong number of arguments")
}

func TestPercentileBuiltin_EmptyList(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.NewList(nil), starlarklib.MakeInt(50)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty list")
}

func TestPercentileBuiltin_POutOfRange_Negative(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{starlarklib.MakeInt(1)})
	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(-1)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "p must be 0-100")
}

func TestPercentileBuiltin_POutOfRange_Over100(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{starlarklib.MakeInt(1)})
	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(101)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "p must be 0-100")
}

func TestPercentileBuiltin_PNotNumeric(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{starlarklib.MakeInt(1)})
	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.String("fifty")}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "p must be numeric")
}

func TestPercentileBuiltin_InvalidListElement(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{starlarklib.Bool(true)})
	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(50)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "percentile")
}

func TestPercentileBuiltin_P0_Returns_Min(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(5), starlarklib.MakeInt(1), starlarklib.MakeInt(9),
	})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(0)}, nil)
	require.NoError(t, err)
	dv, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "1", dv.GetDecimal().String())
}

func TestPercentileBuiltin_P100_Returns_Max(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(5), starlarklib.MakeInt(1), starlarklib.MakeInt(9),
	})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(100)}, nil)
	require.NoError(t, err)
	dv, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "9", dv.GetDecimal().String())
}

func TestPercentileBuiltin_SingleElement(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{starlarklib.MakeInt(42)})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(50)}, nil)
	require.NoError(t, err)
	dv, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "42", dv.GetDecimal().String())
}

func TestPercentileBuiltin_InterpolatedValue(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	// [10, 20, 30, 40] p=25 => rank=0.75, lower=0, upper=1, frac=0.75
	// result = 10 + (20-10)*0.75 = 17.5
	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(10), starlarklib.MakeInt(20),
		starlarklib.MakeInt(30), starlarklib.MakeInt(40),
	})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(25)}, nil)
	require.NoError(t, err)
	dv, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "17.5", dv.GetDecimal().String())
}

func TestPercentileBuiltin_WithFloat(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(10), starlarklib.MakeInt(20),
	})
	// p as float
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.Float(50.0)}, nil)
	require.NoError(t, err)
	dv, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "15", dv.GetDecimal().String())
}

func TestPercentileBuiltin_WithDecimalP(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(10), starlarklib.MakeInt(20),
	})
	dv, err := saga.NewDecimalValue("50")
	require.NoError(t, err)
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, dv}, nil)
	require.NoError(t, err)
	result, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "15", result.GetDecimal().String())
}

// --- filterByHourBuiltin error paths ---

func TestFilterByHourBuiltin_WrongArgCount(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.NewList(nil)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong number of arguments")
}

func TestFilterByHourBuiltin_FirstArgNotList(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.String("not list"), starlarklib.MakeInt(10)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "first argument must be list")
}

func TestFilterByHourBuiltin_SecondArgNotInt(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.NewList(nil), starlarklib.String("10")}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "second argument must be int")
}

func TestFilterByHourBuiltin_HourOutOfRange(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.NewList(nil), starlarklib.MakeInt(24)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hour must be 0-23")

	_, err = starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.NewList(nil), starlarklib.MakeInt(-1)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hour must be 0-23")
}

func TestFilterByHourBuiltin_SkipsNonDictItems(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	// Mix of dicts and non-dicts; non-dicts should be silently skipped
	d := starlarklib.NewDict(1)
	_ = d.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T10:00:00Z"))
	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(42), // not a dict
		d,
	})

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(10)}, nil)
	require.NoError(t, err)
	result, ok := val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 1, result.Len())
}

func TestFilterByHourBuiltin_SkipsDictWithoutTimestamp(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	d := starlarklib.NewDict(1)
	_ = d.SetKey(starlarklib.String("value"), starlarklib.String("100"))
	list := starlarklib.NewList([]starlarklib.Value{d})

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(10)}, nil)
	require.NoError(t, err)
	result, ok := val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 0, result.Len())
}

func TestFilterByHourBuiltin_SkipsDictWithNonStringTimestamp(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	d := starlarklib.NewDict(1)
	_ = d.SetKey(starlarklib.String("timestamp"), starlarklib.MakeInt(12345))
	list := starlarklib.NewList([]starlarklib.Value{d})

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(10)}, nil)
	require.NoError(t, err)
	result, ok := val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 0, result.Len())
}

func TestFilterByHourBuiltin_SkipsDictWithInvalidTimestamp(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	d := starlarklib.NewDict(1)
	_ = d.SetKey(starlarklib.String("timestamp"), starlarklib.String("not-a-valid-time"))
	list := starlarklib.NewList([]starlarklib.Value{d})

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(10)}, nil)
	require.NoError(t, err)
	result, ok := val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 0, result.Len())
}

// --- groupByHourBuiltin error paths ---

func TestGroupByHourBuiltin_WrongArgCount(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("group_by_hour", groupByHourBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong number of arguments")
}

func TestGroupByHourBuiltin_ArgNotList(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("group_by_hour", groupByHourBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.MakeInt(1)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "argument must be list")
}

func TestGroupByHourBuiltin_SkipsInvalidItems(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("group_by_hour", groupByHourBuiltin)

	// Non-dict items and dicts without valid timestamps are skipped
	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(42),
	})

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	dict, ok := val.(*starlarklib.Dict)
	require.True(t, ok)
	assert.Equal(t, 0, dict.Len())
}

// --- addSecondsBuiltin error paths ---

func TestAddSecondsBuiltin_WrongArgCount(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("add_seconds", addSecondsBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.String("2026-02-10T00:00:00Z")}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong number of arguments")
}

func TestAddSecondsBuiltin_FirstArgNotString(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("add_seconds", addSecondsBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.MakeInt(0), starlarklib.MakeInt(3600)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "first argument must be string")
}

func TestAddSecondsBuiltin_SecondArgNotInt(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("add_seconds", addSecondsBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.String("2026-02-10T00:00:00Z"), starlarklib.String("3600")}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "second argument must be int")
}

func TestAddSecondsBuiltin_InvalidTimestamp(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("add_seconds", addSecondsBuiltin)

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.String("not-a-time"), starlarklib.MakeInt(3600)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid timestamp")
}

// --- extractTimestamp edge cases ---

func TestExtractTimestamp_NotADict(t *testing.T) {
	_, ok := extractTimestamp(starlarklib.MakeInt(42))
	assert.False(t, ok)
}

func TestExtractTimestamp_NoTimestampKey(t *testing.T) {
	d := starlarklib.NewDict(1)
	_ = d.SetKey(starlarklib.String("value"), starlarklib.String("100"))
	_, ok := extractTimestamp(d)
	assert.False(t, ok)
}

func TestExtractTimestamp_NonStringTimestamp(t *testing.T) {
	d := starlarklib.NewDict(1)
	_ = d.SetKey(starlarklib.String("timestamp"), starlarklib.MakeInt(12345))
	_, ok := extractTimestamp(d)
	assert.False(t, ok)
}

func TestExtractTimestamp_InvalidTimestampFormat(t *testing.T) {
	d := starlarklib.NewDict(1)
	_ = d.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10 00:00:00"))
	_, ok := extractTimestamp(d)
	assert.False(t, ok)
}

func TestExtractTimestamp_ValidTimestamp(t *testing.T) {
	d := starlarklib.NewDict(1)
	_ = d.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T10:00:00Z"))
	ts, ok := extractTimestamp(d)
	assert.True(t, ok)
	assert.Equal(t, 10, ts.Hour())
}

// --- toDecimalSlice edge cases ---

func TestToDecimalSlice_WithTuple(t *testing.T) {
	tuple := starlarklib.Tuple{starlarklib.MakeInt(1), starlarklib.MakeInt(2), starlarklib.MakeInt(3)}
	vals, err := toDecimalSlice(tuple)
	require.NoError(t, err)
	assert.Len(t, vals, 3)
}

func TestToDecimalSlice_NotListOrTuple(t *testing.T) {
	_, err := toDecimalSlice(starlarklib.String("not-a-list"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrArgType)
}

func TestToDecimalSlice_InvalidElement(t *testing.T) {
	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(1),
		starlarklib.Bool(true), // invalid
	})
	_, err := toDecimalSlice(list)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "element 1")
}

// --- newForecastBuiltins ---

func TestNewForecastBuiltins_NilLogger(t *testing.T) {
	builtins := newForecastBuiltins(nil)
	assert.NotNil(t, builtins)
	assert.Contains(t, builtins, "avg")
	assert.Contains(t, builtins, "sum")
	assert.Contains(t, builtins, "percentile")
	assert.Contains(t, builtins, "filter_by_hour")
	assert.Contains(t, builtins, "group_by_hour")
	assert.Contains(t, builtins, "duration")
	assert.Contains(t, builtins, "add_seconds")
	assert.Contains(t, builtins, "print")
	assert.Contains(t, builtins, "Decimal")
}

// --- sumBuiltin with DecimalValue input ---

func TestSumBuiltin_WithDecimalValues(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("sum", sumBuiltin)

	dv1, err := saga.NewDecimalValue("10.5")
	require.NoError(t, err)
	dv2, err := saga.NewDecimalValue("20.3")
	require.NoError(t, err)

	list := starlarklib.NewList([]starlarklib.Value{dv1, dv2})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	result, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "30.8", result.GetDecimal().String())
}

// --- filterByHourBuiltin happy path ---

func TestFilterByHourBuiltin_MatchingHour(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	d1 := starlarklib.NewDict(1)
	_ = d1.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T10:00:00Z"))
	d2 := starlarklib.NewDict(1)
	_ = d2.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T14:00:00Z"))
	d3 := starlarklib.NewDict(1)
	_ = d3.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T10:30:00Z"))

	list := starlarklib.NewList([]starlarklib.Value{d1, d2, d3})

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(10)}, nil)
	require.NoError(t, err)
	result, ok := val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 2, result.Len(), "expected two observations at hour 10")
}

func TestFilterByHourBuiltin_NoMatch(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	d1 := starlarklib.NewDict(1)
	_ = d1.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T10:00:00Z"))
	list := starlarklib.NewList([]starlarklib.Value{d1})

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(14)}, nil)
	require.NoError(t, err)
	result, ok := val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 0, result.Len())
}

// --- groupByHourBuiltin happy path ---

func TestGroupByHourBuiltin_GroupsCorrectly(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("group_by_hour", groupByHourBuiltin)

	d1 := starlarklib.NewDict(1)
	_ = d1.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T10:00:00Z"))
	d2 := starlarklib.NewDict(1)
	_ = d2.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T10:30:00Z"))
	d3 := starlarklib.NewDict(1)
	_ = d3.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T14:00:00Z"))

	list := starlarklib.NewList([]starlarklib.Value{d1, d2, d3})

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	dict, ok := val.(*starlarklib.Dict)
	require.True(t, ok)
	assert.Equal(t, 2, dict.Len(), "expected two groups: hour 10 and hour 14")

	// Check hour 10 has 2 items
	hour10Val, found, err := dict.Get(starlarklib.MakeInt(10))
	require.NoError(t, err)
	require.True(t, found)
	hour10List, ok := hour10Val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 2, hour10List.Len())

	// Check hour 14 has 1 item
	hour14Val, found, err := dict.Get(starlarklib.MakeInt(14))
	require.NoError(t, err)
	require.True(t, found)
	hour14List, ok := hour14Val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 1, hour14List.Len())
}

// --- addSecondsBuiltin success case ---

func TestAddSecondsBuiltin_Success(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("add_seconds", addSecondsBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{
		starlarklib.String("2026-02-10T00:00:00Z"),
		starlarklib.MakeInt(3600),
	}, nil)
	require.NoError(t, err)
	result, ok := val.(starlarklib.String)
	require.True(t, ok)
	assert.Equal(t, "2026-02-10T01:00:00Z", string(result))
}

func TestAddSecondsBuiltin_NegativeSeconds(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("add_seconds", addSecondsBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{
		starlarklib.String("2026-02-10T01:00:00Z"),
		starlarklib.MakeInt(-3600),
	}, nil)
	require.NoError(t, err)
	result, ok := val.(starlarklib.String)
	require.True(t, ok)
	assert.Equal(t, "2026-02-10T00:00:00Z", string(result))
}

// --- durationBuiltin edge cases ---

func TestDurationBuiltin_InvalidArgType(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("duration", durationBuiltin)

	// Pass a positional string arg where int is expected
	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{starlarklib.String("bad")}, nil)
	require.Error(t, err)
}

func TestDurationBuiltin_OnlyHours(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("duration", durationBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{}, []starlarklib.Tuple{
		{starlarklib.String("hours"), starlarklib.MakeInt(2)},
	})
	require.NoError(t, err)
	i, ok := val.(starlarklib.Int)
	require.True(t, ok)
	i64, ok := i.Int64()
	require.True(t, ok)
	assert.Equal(t, int64(7200), i64)
}

func TestDurationBuiltin_OnlyMinutes(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("duration", durationBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{}, []starlarklib.Tuple{
		{starlarklib.String("minutes"), starlarklib.MakeInt(45)},
	})
	require.NoError(t, err)
	i, ok := val.(starlarklib.Int)
	require.True(t, ok)
	i64, ok := i.Int64()
	require.True(t, ok)
	assert.Equal(t, int64(2700), i64)
}

func TestDurationBuiltin_OnlySeconds(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("duration", durationBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{}, []starlarklib.Tuple{
		{starlarklib.String("seconds"), starlarklib.MakeInt(90)},
	})
	require.NoError(t, err)
	i, ok := val.(starlarklib.Int)
	require.True(t, ok)
	i64, ok := i.Int64()
	require.True(t, ok)
	assert.Equal(t, int64(90), i64)
}

// --- print builtin ---

func TestPrintBuiltin(t *testing.T) {
	builtins := newForecastBuiltins(nil)
	thread := &starlarklib.Thread{Name: "test-print"}

	printFn := builtins["print"]
	val, err := starlarklib.Call(thread, printFn, starlarklib.Tuple{
		starlarklib.String("hello"),
		starlarklib.MakeInt(42),
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, starlarklib.None, val)
}

func TestDurationBuiltin_AllParameters(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("duration", durationBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{}, []starlarklib.Tuple{
		{starlarklib.String("hours"), starlarklib.MakeInt(1)},
		{starlarklib.String("minutes"), starlarklib.MakeInt(30)},
		{starlarklib.String("seconds"), starlarklib.MakeInt(45)},
	})
	require.NoError(t, err)
	i, ok := val.(starlarklib.Int)
	require.True(t, ok)
	i64, ok := i.Int64()
	require.True(t, ok)
	assert.Equal(t, int64(3600+1800+45), i64)
}

// --- toDecimal/toFloat64 overflow cases ---

func TestToDecimal_IntegerTooLarge(t *testing.T) {
	// Create a Starlark integer that overflows int64
	bigInt := starlarklib.MakeInt64(1)
	for i := 0; i < 65; i++ {
		bigInt = bigInt.Add(bigInt) // 2^65, larger than int64 max
	}
	_, err := toDecimal(bigInt)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConversion)
	assert.Contains(t, err.Error(), "integer too large")
}

func TestToFloat64_IntegerTooLarge(t *testing.T) {
	bigInt := starlarklib.MakeInt64(1)
	for i := 0; i < 65; i++ {
		bigInt = bigInt.Add(bigInt) // 2^65
	}
	_, err := toFloat64(bigInt)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConversion)
	assert.Contains(t, err.Error(), "integer too large")
}

// --- addSecondsBuiltin overflow ---

func TestAddSecondsBuiltin_SecondsOverflow(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("add_seconds", addSecondsBuiltin)

	bigInt := starlarklib.MakeInt64(1)
	for i := 0; i < 65; i++ {
		bigInt = bigInt.Add(bigInt)
	}

	_, err := starlarklib.Call(thread, b, starlarklib.Tuple{
		starlarklib.String("2026-02-10T00:00:00Z"),
		bigInt,
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seconds value too large")
}

func TestDurationBuiltin_NoArgs(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("duration", durationBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{}, nil)
	require.NoError(t, err)
	i, ok := val.(starlarklib.Int)
	require.True(t, ok)
	i64, ok := i.Int64()
	require.True(t, ok)
	assert.Equal(t, int64(0), i64)
}
