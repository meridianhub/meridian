package schema

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoerceInt32(t *testing.T) {
	t.Run("valid int64 in range", func(t *testing.T) {
		val, err := coerceInt32(int64(42))
		require.NoError(t, err)
		assert.Equal(t, int32(42), val)
	})

	t.Run("zero", func(t *testing.T) {
		val, err := coerceInt32(int64(0))
		require.NoError(t, err)
		assert.Equal(t, int32(0), val)
	})

	t.Run("negative", func(t *testing.T) {
		val, err := coerceInt32(int64(-100))
		require.NoError(t, err)
		assert.Equal(t, int32(-100), val)
	})

	t.Run("max int32", func(t *testing.T) {
		val, err := coerceInt32(int64(math.MaxInt32))
		require.NoError(t, err)
		assert.Equal(t, int32(math.MaxInt32), val)
	})

	t.Run("min int32", func(t *testing.T) {
		val, err := coerceInt32(int64(math.MinInt32))
		require.NoError(t, err)
		assert.Equal(t, int32(math.MinInt32), val)
	})

	t.Run("overflow above max", func(t *testing.T) {
		_, err := coerceInt32(int64(math.MaxInt32) + 1)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverflow)
		assert.Contains(t, err.Error(), "int32")
	})

	t.Run("overflow below min", func(t *testing.T) {
		_, err := coerceInt32(int64(math.MinInt32) - 1)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverflow)
	})

	t.Run("large positive overflow", func(t *testing.T) {
		_, err := coerceInt32(int64(math.MaxInt64))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverflow)
	})

	t.Run("large negative overflow", func(t *testing.T) {
		_, err := coerceInt32(int64(math.MinInt64))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverflow)
	})

	t.Run("from string large int", func(t *testing.T) {
		_, err := coerceInt32("99999999999999999999")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTypeCoercion)
	})

	t.Run("unsupported type", func(t *testing.T) {
		_, err := coerceInt32([]string{"not", "a", "number"})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTypeCoercion)
	})
}

func TestCoerceInt64(t *testing.T) {
	t.Run("valid int64", func(t *testing.T) {
		val, err := coerceInt64(int64(42))
		require.NoError(t, err)
		assert.Equal(t, int64(42), val)
	})

	t.Run("max int64", func(t *testing.T) {
		val, err := coerceInt64(int64(math.MaxInt64))
		require.NoError(t, err)
		assert.Equal(t, int64(math.MaxInt64), val)
	})

	t.Run("min int64", func(t *testing.T) {
		val, err := coerceInt64(int64(math.MinInt64))
		require.NoError(t, err)
		assert.Equal(t, int64(math.MinInt64), val)
	})

	t.Run("from string too large", func(t *testing.T) {
		_, err := coerceInt64("99999999999999999999")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverflow)
	})

	t.Run("from non-numeric string", func(t *testing.T) {
		_, err := coerceInt64("not-a-number")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTypeCoercion)
		assert.NotErrorIs(t, err, ErrOverflow)
	})

	t.Run("unsupported type", func(t *testing.T) {
		_, err := coerceInt64(true)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTypeCoercion)
	})
}

func TestCoerceUint32(t *testing.T) {
	t.Run("valid int64 in range", func(t *testing.T) {
		val, err := coerceUint32(int64(42))
		require.NoError(t, err)
		assert.Equal(t, uint32(42), val)
	})

	t.Run("zero", func(t *testing.T) {
		val, err := coerceUint32(int64(0))
		require.NoError(t, err)
		assert.Equal(t, uint32(0), val)
	})

	t.Run("max uint32", func(t *testing.T) {
		val, err := coerceUint32(int64(math.MaxUint32))
		require.NoError(t, err)
		assert.Equal(t, uint32(math.MaxUint32), val)
	})

	t.Run("negative value", func(t *testing.T) {
		_, err := coerceUint32(int64(-1))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverflow)
		assert.Contains(t, err.Error(), "uint32")
	})

	t.Run("overflow above max", func(t *testing.T) {
		_, err := coerceUint32(int64(math.MaxUint32) + 1)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverflow)
	})

	t.Run("large int64", func(t *testing.T) {
		_, err := coerceUint32(int64(math.MaxInt64))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverflow)
	})

	t.Run("from string large int", func(t *testing.T) {
		_, err := coerceUint32("99999999999999999999")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTypeCoercion)
	})

	t.Run("unsupported type", func(t *testing.T) {
		_, err := coerceUint32(3.14)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTypeCoercion)
	})
}

func TestCoerceDecimalValue(t *testing.T) {
	t.Run("from string", func(t *testing.T) {
		val, err := coerceDecimalValue("123.45")
		require.NoError(t, err)
		assert.Equal(t, "123.45", val.String())
	})

	t.Run("from int64", func(t *testing.T) {
		val, err := coerceDecimalValue(int64(100))
		require.NoError(t, err)
		assert.Equal(t, "100", val.String())
	})

	t.Run("from float64", func(t *testing.T) {
		val, err := coerceDecimalValue(float64(99.99))
		require.NoError(t, err)
		assert.True(t, val.GreaterThan(decimal.NewFromFloat(99.98)))
	})

	t.Run("from decimal passthrough", func(t *testing.T) {
		d := decimal.NewFromFloat(55.55)
		val, err := coerceDecimalValue(d)
		require.NoError(t, err)
		assert.True(t, d.Equal(val))
	})

	t.Run("from int", func(t *testing.T) {
		val, err := coerceDecimalValue(42)
		require.NoError(t, err)
		assert.Equal(t, "42", val.String())
	})

	t.Run("invalid string", func(t *testing.T) {
		_, err := coerceDecimalValue("not-a-number")
		require.Error(t, err)
	})

	t.Run("unsupported type", func(t *testing.T) {
		_, err := coerceDecimalValue(true)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrDecimalConversion)
	})
}

func TestCoerceBool(t *testing.T) {
	t.Run("bool true", func(t *testing.T) {
		val, err := coerceBoolValue(true)
		require.NoError(t, err)
		assert.True(t, val)
	})

	t.Run("bool false", func(t *testing.T) {
		val, err := coerceBoolValue(false)
		require.NoError(t, err)
		assert.False(t, val)
	})

	t.Run("unsupported type", func(t *testing.T) {
		_, err := coerceBoolValue("true")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTypeCoercion)
	})
}

func TestCoerceString(t *testing.T) {
	t.Run("string passthrough", func(t *testing.T) {
		val, err := coerceStringValue("hello")
		require.NoError(t, err)
		assert.Equal(t, "hello", val)
	})

	t.Run("unsupported type", func(t *testing.T) {
		_, err := coerceStringValue(42)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTypeCoercion)
	})
}

func TestCoerceValue(t *testing.T) {
	t.Run("int32 coercion", func(t *testing.T) {
		val, err := CoerceValue(int64(42), TypeInt32)
		require.NoError(t, err)
		assert.Equal(t, int32(42), val)
	})

	t.Run("int64 coercion", func(t *testing.T) {
		val, err := CoerceValue(int64(42), TypeInt64)
		require.NoError(t, err)
		assert.Equal(t, int64(42), val)
	})

	t.Run("uint32 coercion", func(t *testing.T) {
		val, err := CoerceValue(int64(42), TypeUint32)
		require.NoError(t, err)
		assert.Equal(t, uint32(42), val)
	})

	t.Run("Decimal coercion", func(t *testing.T) {
		val, err := CoerceValue("100.50", TypeDecimal)
		require.NoError(t, err)
		d, ok := val.(decimal.Decimal)
		require.True(t, ok)
		assert.True(t, decimal.NewFromFloat(100.50).Equal(d))
	})

	t.Run("bool coercion", func(t *testing.T) {
		val, err := CoerceValue(true, TypeBool)
		require.NoError(t, err)
		assert.Equal(t, true, val)
	})

	t.Run("string coercion", func(t *testing.T) {
		val, err := CoerceValue("hello", TypeString)
		require.NoError(t, err)
		assert.Equal(t, "hello", val)
	})

	t.Run("uuid coercion", func(t *testing.T) {
		val, err := CoerceValue("550e8400-e29b-41d4-a716-446655440000", TypeUUID)
		require.NoError(t, err)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", val)
	})

	t.Run("nil for optional params", func(t *testing.T) {
		val, err := CoerceValue(nil, TypeString)
		require.NoError(t, err)
		assert.Nil(t, val)
	})

	t.Run("enum passthrough as string", func(t *testing.T) {
		val, err := CoerceValue("DEBIT", TypeEnum)
		require.NoError(t, err)
		assert.Equal(t, "DEBIT", val)
	})

	t.Run("array passthrough", func(t *testing.T) {
		arr := []any{"a", "b"}
		val, err := CoerceValue(arr, TypeArray)
		require.NoError(t, err)
		assert.Equal(t, arr, val)
	})

	t.Run("map passthrough", func(t *testing.T) {
		m := map[string]any{"key": "val"}
		val, err := CoerceValue(m, TypeMap)
		require.NoError(t, err)
		assert.Equal(t, m, val)
	})

	t.Run("int32 overflow error includes context", func(t *testing.T) {
		_, err := CoerceValue(int64(math.MaxInt64), TypeInt32)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverflow)
	})
}

func TestCoerceParams(t *testing.T) {
	handlerDef := &HandlerDef{
		Params: map[string]*FieldDef{
			"count":     {Type: TypeInt32, Required: true},
			"amount":    {Type: TypeDecimal, Required: true},
			"name":      {Type: TypeString, Required: true},
			"active":    {Type: TypeBool, Required: false},
			"version":   {Type: TypeUint32, Required: false},
			"timestamp": {Type: TypeInt64, Required: false},
		},
	}

	t.Run("coerces all param types", func(t *testing.T) {
		params := map[string]any{
			"count":     int64(10),
			"amount":    "99.99",
			"name":      "test",
			"active":    true,
			"version":   int64(3),
			"timestamp": int64(1706000000),
		}

		err := CoerceParams(params, handlerDef)
		require.NoError(t, err)

		assert.IsType(t, int32(0), params["count"])
		assert.Equal(t, int32(10), params["count"])

		d, ok := params["amount"].(decimal.Decimal)
		require.True(t, ok)
		assert.Equal(t, "99.99", d.String())

		assert.Equal(t, "test", params["name"])
		assert.Equal(t, true, params["active"])
		assert.Equal(t, uint32(3), params["version"])
		assert.Equal(t, int64(1706000000), params["timestamp"])
	})

	t.Run("skips absent optional params", func(t *testing.T) {
		params := map[string]any{
			"count":  int64(10),
			"amount": "99.99",
			"name":   "test",
			// active, version, timestamp are absent
		}

		err := CoerceParams(params, handlerDef)
		require.NoError(t, err)
		// No error, absent optionals are fine
	})

	t.Run("error includes param name", func(t *testing.T) {
		params := map[string]any{
			"count":  int64(math.MaxInt64), // overflow for int32
			"amount": "100",
			"name":   "test",
		}

		err := CoerceParams(params, handlerDef)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count")
	})

	t.Run("error includes expected type", func(t *testing.T) {
		params := map[string]any{
			"count":  int64(math.MaxInt64),
			"amount": "100",
			"name":   "test",
		}

		err := CoerceParams(params, handlerDef)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "int32")
	})
}
