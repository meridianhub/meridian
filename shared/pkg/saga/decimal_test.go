package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

func TestDecimalValue_Construction(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "valid positive decimal",
			input:   "123.45",
			wantErr: false,
		},
		{
			name:    "valid negative decimal",
			input:   "-123.45",
			wantErr: false,
		},
		{
			name:    "valid integer",
			input:   "100",
			wantErr: false,
		},
		{
			name:    "valid zero",
			input:   "0",
			wantErr: false,
		},
		{
			name:    "valid small decimal",
			input:   "0.00001",
			wantErr: false,
		},
		{
			name:    "invalid string",
			input:   "abc",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := NewDecimalValue(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, val)
			} else {
				require.NoError(t, err)
				require.NotNil(t, val)
				assert.Equal(t, "Decimal", val.Type())
			}
		})
	}
}

func TestDecimalValue_FloatRejection(t *testing.T) {
	// The Decimal constructor should only accept strings
	// Float-to-Decimal conversion must be blocked
	decimalBuiltin := DecimalBuiltin()

	thread := &starlark.Thread{Name: "test"}

	// Test with float argument - should fail
	_, err := starlark.Call(thread, decimalBuiltin, starlark.Tuple{starlark.Float(1.5)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "string")

	// Test with int argument - should fail
	_, err = starlark.Call(thread, decimalBuiltin, starlark.Tuple{starlark.MakeInt(42)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "string")

	// Test with string argument - should succeed
	val, err := starlark.Call(thread, decimalBuiltin, starlark.Tuple{starlark.String("123.45")}, nil)
	require.NoError(t, err)
	require.NotNil(t, val)
	_, ok := val.(*DecimalValue)
	assert.True(t, ok, "expected DecimalValue")
}

func TestDecimalValue_Arithmetic(t *testing.T) {
	tests := []struct {
		name     string
		left     string
		right    string
		op       string
		expected string
	}{
		{
			name:     "addition",
			left:     "10.5",
			right:    "3.3",
			op:       "+",
			expected: "13.8",
		},
		{
			name:     "subtraction",
			left:     "10.5",
			right:    "3.3",
			op:       "-",
			expected: "7.2",
		},
		{
			name:     "multiplication",
			left:     "10.5",
			right:    "2",
			op:       "*",
			expected: "21",
		},
		{
			name:     "division",
			left:     "10",
			right:    "4",
			op:       "/",
			expected: "2.5",
		},
		{
			name:     "precision test 0.1 + 0.2",
			left:     "0.1",
			right:    "0.2",
			op:       "+",
			expected: "0.3",
		},
		{
			name:     "large number addition",
			left:     "999999999999.999999",
			right:    "0.000001",
			op:       "+",
			expected: "1000000000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left, err := NewDecimalValue(tt.left)
			require.NoError(t, err)
			right, err := NewDecimalValue(tt.right)
			require.NoError(t, err)

			var result starlark.Value
			var opErr error

			switch tt.op {
			case "+":
				result, opErr = left.Binary(syntax.PLUS, right, starlark.Left)
			case "-":
				result, opErr = left.Binary(syntax.MINUS, right, starlark.Left)
			case "*":
				result, opErr = left.Binary(syntax.STAR, right, starlark.Left)
			case "/":
				result, opErr = left.Binary(syntax.SLASH, right, starlark.Left)
			}

			require.NoError(t, opErr)
			require.NotNil(t, result)

			decResult, ok := result.(*DecimalValue)
			require.True(t, ok)

			expected, err := NewDecimalValue(tt.expected)
			require.NoError(t, err)
			assert.True(t, decResult.decimal.Equal(expected.decimal),
				"expected %s, got %s", tt.expected, decResult.String())
		})
	}
}

func TestDecimalValue_DivisionByZero(t *testing.T) {
	left, err := NewDecimalValue("10")
	require.NoError(t, err)
	right, err := NewDecimalValue("0")
	require.NoError(t, err)

	_, err = left.Binary(syntax.SLASH, right, starlark.Left)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "division by zero")
}

func TestDecimalValue_TypeMismatch(t *testing.T) {
	left, err := NewDecimalValue("10")
	require.NoError(t, err)

	// Addition with non-Decimal should fail
	_, err = left.Binary(syntax.PLUS, starlark.MakeInt(5), starlark.Left)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "type")
}

func TestDecimalValue_StarlarkInterface(t *testing.T) {
	val, err := NewDecimalValue("123.45")
	require.NoError(t, err)

	// Test Type()
	assert.Equal(t, "Decimal", val.Type())

	// Test String()
	assert.Equal(t, "123.45", val.String())

	// Test Truth() - non-zero is truthy
	assert.True(t, bool(val.Truth()))

	// Test zero is falsy
	zero, err := NewDecimalValue("0")
	require.NoError(t, err)
	assert.False(t, bool(zero.Truth()))

	// Test Freeze() - should not panic
	val.Freeze()

	// Test Hash() - should return a hash
	hash, err := val.Hash()
	require.NoError(t, err)
	assert.NotZero(t, hash)
}

func TestDecimalValue_Comparison(t *testing.T) {
	a, _ := NewDecimalValue("10.5")
	b, _ := NewDecimalValue("10.5")
	c, _ := NewDecimalValue("5.0")

	// Test equality
	eq, err := starlark.Compare(syntax.EQL, a, b)
	require.NoError(t, err)
	assert.True(t, eq)

	// Test inequality
	neq, err := starlark.Compare(syntax.NEQ, a, c)
	require.NoError(t, err)
	assert.True(t, neq)

	// Test less than
	lt, err := starlark.Compare(syntax.LT, c, a)
	require.NoError(t, err)
	assert.True(t, lt)

	// Test greater than
	gt, err := starlark.Compare(syntax.GT, a, c)
	require.NoError(t, err)
	assert.True(t, gt)

	// Equal-value boundary: < and > must be strict (false on equal values).
	// a and b are both "10.5". These assertions kill the CONDITIONALS_BOUNDARY
	// mutants that weaken `cmp < 0` to `cmp <= 0` (decimal.go:112) and `cmp > 0`
	// to `cmp >= 0` (decimal.go:116) in the Starlark Decimal comparison path
	// used by tenant saga scripts.
	ltEqual, err := starlark.Compare(syntax.LT, a, b)
	require.NoError(t, err)
	assert.False(t, ltEqual, "LT must be false for equal Decimals")

	gtEqual, err := starlark.Compare(syntax.GT, a, b)
	require.NoError(t, err)
	assert.False(t, gtEqual, "GT must be false for equal Decimals")
}

func TestDecimalValue_NegativeOperations(t *testing.T) {
	a, _ := NewDecimalValue("-5.5")
	b, _ := NewDecimalValue("3.5")

	// Test addition with negative
	result, err := a.Binary(syntax.PLUS, b, starlark.Left)
	require.NoError(t, err)
	decResult := result.(*DecimalValue)
	expected, _ := NewDecimalValue("-2")
	assert.True(t, decResult.decimal.Equal(expected.decimal))

	// Test subtraction resulting in negative
	c, _ := NewDecimalValue("2")
	d, _ := NewDecimalValue("5")
	result, err = c.Binary(syntax.MINUS, d, starlark.Left)
	require.NoError(t, err)
	decResult = result.(*DecimalValue)
	expected, _ = NewDecimalValue("-3")
	assert.True(t, decResult.decimal.Equal(expected.decimal))
}
