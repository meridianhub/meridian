package schema

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

func TestGoToStarlarkValue(t *testing.T) {
	t.Run("nil returns None", func(t *testing.T) {
		val, err := goToStarlarkValue(nil)
		require.NoError(t, err)
		assert.Equal(t, starlark.None, val)
	})

	t.Run("string", func(t *testing.T) {
		val, err := goToStarlarkValue("hello")
		require.NoError(t, err)
		assert.Equal(t, starlark.String("hello"), val)
	})

	t.Run("int", func(t *testing.T) {
		val, err := goToStarlarkValue(42)
		require.NoError(t, err)
		assert.Equal(t, starlark.MakeInt(42), val)
	})

	t.Run("int64", func(t *testing.T) {
		val, err := goToStarlarkValue(int64(100))
		require.NoError(t, err)
		assert.Equal(t, starlark.MakeInt64(100), val)
	})

	t.Run("int32", func(t *testing.T) {
		val, err := goToStarlarkValue(int32(50))
		require.NoError(t, err)
		assert.Equal(t, starlark.MakeInt(50), val)
	})

	t.Run("uint32", func(t *testing.T) {
		val, err := goToStarlarkValue(uint32(999))
		require.NoError(t, err)
		assert.Equal(t, starlark.MakeUint(999), val)
	})

	t.Run("float64", func(t *testing.T) {
		val, err := goToStarlarkValue(3.14)
		require.NoError(t, err)
		assert.Equal(t, starlark.Float(3.14), val)
	})

	t.Run("bool", func(t *testing.T) {
		val, err := goToStarlarkValue(true)
		require.NoError(t, err)
		assert.Equal(t, starlark.Bool(true), val)
	})

	t.Run("decimal", func(t *testing.T) {
		d := decimal.NewFromFloat(123.45)
		val, err := goToStarlarkValue(d)
		require.NoError(t, err)
		// Decimal is converted to string for lossless representation
		assert.Equal(t, starlark.String(d.String()), val)
	})

	t.Run("unknown type falls back to string", func(t *testing.T) {
		type custom struct{ X int }
		val, err := goToStarlarkValue(custom{X: 42})
		require.NoError(t, err)
		assert.Equal(t, starlark.String("{42}"), val)
	})
}

func TestGoSliceToStarlark(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		list, err := goSliceToStarlark([]any{})
		require.NoError(t, err)
		assert.Equal(t, 0, list.Len())
	})

	t.Run("mixed types", func(t *testing.T) {
		list, err := goSliceToStarlark([]any{"hello", 42, true})
		require.NoError(t, err)
		assert.Equal(t, 3, list.Len())
	})
}

func TestGoStringSliceToStarlark(t *testing.T) {
	list := goStringSliceToStarlark([]string{"a", "b", "c"})
	assert.Equal(t, 3, list.Len())

	val := list.Index(0)
	assert.Equal(t, starlark.String("a"), val)
}

func TestGoMapToStarlark(t *testing.T) {
	t.Run("empty map", func(t *testing.T) {
		dict, err := goMapToStarlark(map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, 0, dict.Len())
	})

	t.Run("map with values", func(t *testing.T) {
		dict, err := goMapToStarlark(map[string]any{
			"key": "value",
			"num": 42,
		})
		require.NoError(t, err)
		assert.Equal(t, 2, dict.Len())

		val, found, err := dict.Get(starlark.String("key"))
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, starlark.String("value"), val)
	})
}
