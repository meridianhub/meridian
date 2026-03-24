package starlark

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	starlarklib "go.starlark.net/starlark"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// --- avgBuiltin with DecimalValue inputs ---

func TestAvgBuiltin_WithDecimalValues(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("avg", avgBuiltin)

	dv1, err := saga.NewDecimalValue("10.0")
	require.NoError(t, err)
	dv2, err := saga.NewDecimalValue("20.0")
	require.NoError(t, err)
	dv3, err := saga.NewDecimalValue("30.0")
	require.NoError(t, err)

	list := starlarklib.NewList([]starlarklib.Value{dv1, dv2, dv3})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	result, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "20", result.GetDecimal().String())
}

func TestAvgBuiltin_SingleElement(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("avg", avgBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{starlarklib.MakeInt(42)})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	result, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "42", result.GetDecimal().String())
}

func TestAvgBuiltin_WithStringDecimals(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("avg", avgBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.String("1.5"),
		starlarklib.String("2.5"),
	})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	result, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "2", result.GetDecimal().String())
}

// --- sumBuiltin with mixed types ---

func TestSumBuiltin_MixedIntAndDecimalValues(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("sum", sumBuiltin)

	dv, err := saga.NewDecimalValue("5.5")
	require.NoError(t, err)

	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(10),
		dv,
		starlarklib.Float(4.5),
	})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	result, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "20", result.GetDecimal().String())
}

func TestSumBuiltin_WithTuple(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("sum", sumBuiltin)

	tuple := starlarklib.Tuple{starlarklib.MakeInt(3), starlarklib.MakeInt(7)}
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{tuple}, nil)
	require.NoError(t, err)
	result, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	assert.Equal(t, "10", result.GetDecimal().String())
}

// --- percentileBuiltin additional cases ---

func TestPercentileBuiltin_P50_OddCount(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	// 5-element list: P50 should be the median (3rd element after sort)
	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(5),
		starlarklib.MakeInt(1),
		starlarklib.MakeInt(3),
		starlarklib.MakeInt(7),
		starlarklib.MakeInt(9),
	})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(50)}, nil)
	require.NoError(t, err)
	result, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
	// Sorted: [1, 3, 5, 7, 9], P50 at index 2 = 5
	assert.Equal(t, "5", result.GetDecimal().String())
}

func TestPercentileBuiltin_P25(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("percentile", percentileBuiltin)

	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.Float(10.0),
		starlarklib.Float(20.0),
		starlarklib.Float(30.0),
		starlarklib.Float(40.0),
	})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(25)}, nil)
	require.NoError(t, err)
	_, ok := val.(*saga.DecimalValue)
	require.True(t, ok)
}

// --- filterByHourBuiltin edge cases ---

func TestFilterByHourBuiltin_MidnightHour(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	d1 := starlarklib.NewDict(1)
	_ = d1.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-01-15T00:00:00Z"))
	d2 := starlarklib.NewDict(1)
	_ = d2.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-01-15T01:00:00Z"))

	list := starlarklib.NewList([]starlarklib.Value{d1, d2})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(0)}, nil)
	require.NoError(t, err)
	result, ok := val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 1, result.Len(), "expected only midnight observation")
}

func TestFilterByHourBuiltin_Hour23(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("filter_by_hour", filterByHourBuiltin)

	d1 := starlarklib.NewDict(1)
	_ = d1.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-01-15T23:00:00Z"))
	d2 := starlarklib.NewDict(1)
	_ = d2.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-01-15T22:59:00Z"))

	list := starlarklib.NewList([]starlarklib.Value{d1, d2})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list, starlarklib.MakeInt(23)}, nil)
	require.NoError(t, err)
	result, ok := val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 1, result.Len())
}

// --- groupByHourBuiltin edge cases ---

func TestGroupByHourBuiltin_EmptyList(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("group_by_hour", groupByHourBuiltin)

	list := starlarklib.NewList(nil)
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	dict, ok := val.(*starlarklib.Dict)
	require.True(t, ok)
	assert.Equal(t, 0, dict.Len())
}

func TestGroupByHourBuiltin_AllSameHour(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("group_by_hour", groupByHourBuiltin)

	makeObs := func(ts string) *starlarklib.Dict {
		d := starlarklib.NewDict(1)
		_ = d.SetKey(starlarklib.String("timestamp"), starlarklib.String(ts))
		return d
	}

	list := starlarklib.NewList([]starlarklib.Value{
		makeObs("2026-03-01T08:00:00Z"),
		makeObs("2026-03-02T08:00:00Z"),
		makeObs("2026-03-03T08:00:00Z"),
	})
	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{list}, nil)
	require.NoError(t, err)
	dict, ok := val.(*starlarklib.Dict)
	require.True(t, ok)
	assert.Equal(t, 1, dict.Len(), "all observations should be in hour 8 group")

	hour8Val, found, err := dict.Get(starlarklib.MakeInt(8))
	require.NoError(t, err)
	require.True(t, found)
	hour8List, ok := hour8Val.(*starlarklib.List)
	require.True(t, ok)
	assert.Equal(t, 3, hour8List.Len())
}

// --- durationBuiltin edge cases ---

func TestDurationBuiltin_ZeroValues(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("duration", durationBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{}, []starlarklib.Tuple{
		{starlarklib.String("hours"), starlarklib.MakeInt(0)},
	})
	require.NoError(t, err)
	result, ok := val.(starlarklib.Int)
	require.True(t, ok)
	n, _ := result.Int64()
	assert.Equal(t, int64(0), n)
}

func TestDurationBuiltin_LargeHours(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("duration", durationBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{}, []starlarklib.Tuple{
		{starlarklib.String("hours"), starlarklib.MakeInt(168)}, // 1 week
	})
	require.NoError(t, err)
	result, ok := val.(starlarklib.Int)
	require.True(t, ok)
	n, _ := result.Int64()
	assert.Equal(t, int64(168*3600), n)
}

// --- addSecondsBuiltin additional cases ---

func TestAddSecondsBuiltin_ZeroSeconds(t *testing.T) {
	thread := &starlarklib.Thread{Name: "test"}
	b := starlarklib.NewBuiltin("add_seconds", addSecondsBuiltin)

	val, err := starlarklib.Call(thread, b, starlarklib.Tuple{
		starlarklib.String("2026-01-01T00:00:00Z"),
		starlarklib.MakeInt(0),
	}, nil)
	require.NoError(t, err)
	result, ok := val.(starlarklib.String)
	require.True(t, ok)
	assert.Equal(t, "2026-01-01T00:00:00Z", string(result))
}

// --- toDecimalSlice with mixed valid types ---

func TestToDecimalSlice_MixedValidTypes(t *testing.T) {
	dv, err := saga.NewDecimalValue("5.5")
	require.NoError(t, err)

	list := starlarklib.NewList([]starlarklib.Value{
		starlarklib.MakeInt(1),
		starlarklib.Float(2.5),
		starlarklib.String("3.0"),
		dv,
	})

	result, err := toDecimalSlice(list)
	require.NoError(t, err)
	require.Len(t, result, 4)
	assert.Equal(t, "1", result[0].String())
	assert.Equal(t, "2.5", result[1].String())
	assert.Equal(t, "3", result[2].String())
	assert.Equal(t, "5.5", result[3].String())
}
