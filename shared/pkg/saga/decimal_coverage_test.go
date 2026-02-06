package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

func TestDecimalValue_Cmp(t *testing.T) {
	t.Run("equal values return 0", func(t *testing.T) {
		a, _ := NewDecimalValue("10.5")
		b, _ := NewDecimalValue("10.5")
		result, err := a.Cmp(b, 0)
		require.NoError(t, err)
		assert.Equal(t, 0, result)
	})

	t.Run("less than returns -1", func(t *testing.T) {
		a, _ := NewDecimalValue("5.0")
		b, _ := NewDecimalValue("10.5")
		result, err := a.Cmp(b, 0)
		require.NoError(t, err)
		assert.Equal(t, -1, result)
	})

	t.Run("greater than returns 1", func(t *testing.T) {
		a, _ := NewDecimalValue("10.5")
		b, _ := NewDecimalValue("5.0")
		result, err := a.Cmp(b, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, result)
	})

	t.Run("type mismatch returns error", func(t *testing.T) {
		a, _ := NewDecimalValue("10.5")
		_, err := a.Cmp(starlark.MakeInt(5), 0)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrDecimalTypeMismatch)
	})
}

func TestDecimalValue_GetDecimal(t *testing.T) {
	d, err := NewDecimalValue("123.45")
	require.NoError(t, err)

	dec := d.GetDecimal()
	assert.Equal(t, "123.45", dec.String())
}

func TestDecimalValue_Binary_RightSide(t *testing.T) {
	a, _ := NewDecimalValue("10")
	b, _ := NewDecimalValue("3")

	// Test subtraction from Right side: b - a
	result, err := a.Binary(syntax.MINUS, b, starlark.Right)
	require.NoError(t, err)
	decResult := result.(*DecimalValue)
	expected, _ := NewDecimalValue("-7")
	assert.True(t, decResult.decimal.Equal(expected.decimal),
		"expected -7, got %s", decResult.String())
}

func TestDecimalValue_Binary_DivisionRightSide(t *testing.T) {
	a, _ := NewDecimalValue("2")
	b, _ := NewDecimalValue("10")

	// Right side division: b / a
	result, err := a.Binary(syntax.SLASH, b, starlark.Right)
	require.NoError(t, err)
	decResult := result.(*DecimalValue)
	expected, _ := NewDecimalValue("5")
	assert.True(t, decResult.decimal.Equal(expected.decimal),
		"expected 5, got %s", decResult.String())
}

func TestDecimalValue_Binary_DivisionByZeroRightSide(t *testing.T) {
	zero, _ := NewDecimalValue("0")
	b, _ := NewDecimalValue("10")

	// Right side: b / zero, but a is the receiver with value 0
	_, err := zero.Binary(syntax.SLASH, b, starlark.Right)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDivisionByZero)
}

func TestDecimalValue_Binary_UnsupportedOperator(t *testing.T) {
	a, _ := NewDecimalValue("10")
	b, _ := NewDecimalValue("3")

	_, err := a.Binary(syntax.PERCENT, b, starlark.Left)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedDecimalOperation)
}

func TestDecimalValue_CompareSameType_UnsupportedOp(t *testing.T) {
	a, _ := NewDecimalValue("10")
	b, _ := NewDecimalValue("5")

	// Use an unsupported token (e.g., IN)
	_, err := a.CompareSameType(syntax.IN, b, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedDecimalOperation)
}

func TestDecimalBuiltin_WrongArgCount(t *testing.T) {
	builtin := DecimalBuiltin()
	thread := &starlark.Thread{Name: "test"}

	// No arguments
	_, err := starlark.Call(thread, builtin, starlark.Tuple{}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDecimalArgCount)

	// Two arguments
	_, err = starlark.Call(thread, builtin, starlark.Tuple{starlark.String("1"), starlark.String("2")}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDecimalArgCount)
}
