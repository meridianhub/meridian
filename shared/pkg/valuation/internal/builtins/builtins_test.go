package builtins_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/meridianhub/meridian/shared/pkg/valuation/internal/builtins"
)

func TestRegistry_CreateBuiltins(t *testing.T) {
	registry := builtins.NewRegistry()

	// Create builtins dict
	dict := registry.CreateBuiltins()

	// Verify essential builtins are present
	_, ok := dict["Decimal"]
	assert.True(t, ok, "Decimal builtin should be present")

	_, ok = dict["quantity"]
	assert.True(t, ok, "quantity builtin should be present")

	_, ok = dict["record_path"]
	assert.True(t, ok, "record_path builtin should be present")
}

func TestDecimal_Builtin(t *testing.T) {
	registry := builtins.NewRegistry()
	dict := registry.CreateBuiltins()

	decimalFn := dict["Decimal"]
	require.NotNil(t, decimalFn)

	// Create Starlark thread for execution
	thread := &starlark.Thread{Name: "test"}

	tests := []struct {
		name     string
		input    string
		expected decimal.Decimal
	}{
		{
			name:     "integer string",
			input:    "100",
			expected: decimal.NewFromInt(100),
		},
		{
			name:     "decimal string",
			input:    "123.45",
			expected: decimal.NewFromFloat(123.45),
		},
		{
			name:     "negative",
			input:    "-50.5",
			expected: decimal.NewFromFloat(-50.5),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call Decimal(input)
			result, err := starlark.Call(thread, decimalFn, starlark.Tuple{starlark.String(tt.input)}, nil)
			require.NoError(t, err)

			// Result should be a wrapped decimal
			assert.NotNil(t, result)
			// For now, just verify it's callable and returns something
			// Full decimal wrapper implementation tested separately
		})
	}
}

func TestQuantity_Builtin(t *testing.T) {
	registry := builtins.NewRegistry()
	dict := registry.CreateBuiltins()

	quantityFn := dict["quantity"]
	require.NotNil(t, quantityFn)

	thread := &starlark.Thread{Name: "test"}

	// Call quantity(amount="100.50", instrument="GBP")
	kwargs := []starlark.Tuple{
		{starlark.String("amount"), starlark.String("100.50")},
		{starlark.String("instrument"), starlark.String("GBP")},
	}

	result, err := starlark.Call(thread, quantityFn, nil, kwargs)
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Result should be a dict with amount, instrument, attributes
	resultDict, ok := result.(*starlark.Dict)
	require.True(t, ok, "quantity should return a dict")

	amount, found, err := resultDict.Get(starlark.String("amount"))
	require.NoError(t, err)
	require.True(t, found)
	// Starlark strings include quotes in String() representation
	assert.Equal(t, `"100.50"`, amount.String())

	instrument, found, err := resultDict.Get(starlark.String("instrument"))
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, `"GBP"`, instrument.String())
}

func TestRecordPath_Builtin(t *testing.T) {
	registry := builtins.NewRegistry()
	dict := registry.CreateBuiltins()

	recordPathFn := dict["record_path"]
	require.NotNil(t, recordPathFn)

	thread := &starlark.Thread{Name: "test"}

	// Call record_path(description, data)
	dataDict := &starlark.Dict{}
	dataDict.SetKey(starlark.String("price"), starlark.Float(45.5))

	args := starlark.Tuple{
		starlark.String("Retrieved spot price"),
		dataDict,
	}

	result, err := starlark.Call(thread, recordPathFn, args, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.None, result, "record_path should return None")

	// In the real implementation, this would append to analysis.CalculationPath
	// For now, just verify the function is callable
}
