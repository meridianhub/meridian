package quantity_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// TestAsset_AllNonCurrencyDimensions verifies that NewAsset works with every supported
// non-CURRENCY dimension and that the resulting Asset carries the correct metadata.
func TestAsset_AllNonCurrencyDimensions(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		dimension string
		precision int
	}{
		{"ENERGY / KWH", "KWH", "ENERGY", 3},
		{"CARBON / CARBON_CREDIT", "CARBON_CREDIT", "CARBON", 4},
		{"COMPUTE / GPU_HOUR", "GPU_HOUR", "COMPUTE", 6},
		{"DATA / GB", "GB", "DATA", 0},
		{"TIME / SECOND", "SECOND", "TIME", 0},
		{"MASS / KG", "KG", "MASS", 3},
		{"VOLUME / LITER", "LITER", "VOLUME", 3},
		{"COUNT / UNIT", "UNIT", "COUNT", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst, err := quantity.NewInstrument(tt.code, 0, tt.dimension, tt.precision)
			require.NoError(t, err)

			amount := decimal.NewFromInt(42)
			asset := quantity.NewAsset(amount, inst)

			assert.True(t, asset.Amount.Equal(amount))
			assert.Equal(t, tt.code, asset.Instrument.Code)
			assert.Equal(t, tt.dimension, asset.Instrument.Dimension)
			assert.Equal(t, tt.precision, asset.Instrument.Precision)
			assert.True(t, inst.IsCommodity(), "instrument %s should be commodity", tt.code)
			assert.False(t, inst.IsMonetary(), "instrument %s should not be monetary", tt.code)
		})
	}
}

// TestNewQuantityValidated_Commodity_RejectsCurrency verifies that
// NewQuantityValidated[Commodity] rejects instruments with CURRENCY dimension.
func TestNewQuantityValidated_Commodity_RejectsCurrency(t *testing.T) {
	instGBP, err := quantity.NewInstrument("GBP", 0, "CURRENCY", 2)
	require.NoError(t, err)

	_, err = quantity.NewQuantityValidated[quantity.Commodity](decimal.NewFromInt(100), instGBP)
	require.Error(t, err)
	assert.ErrorIs(t, err, quantity.ErrDimensionMismatch)
}

// TestNewQuantityValidated_Monetary_RejectsNonCurrency verifies that
// NewQuantityValidated[Monetary] rejects all non-CURRENCY instruments.
func TestNewQuantityValidated_Monetary_RejectsNonCurrency(t *testing.T) {
	nonCurrencyInstruments := []struct {
		code      string
		dimension string
	}{
		{"KWH", "ENERGY"},
		{"CARBON_CREDIT", "CARBON"},
		{"GPU_HOUR", "COMPUTE"},
		{"GB", "DATA"},
	}

	for _, tc := range nonCurrencyInstruments {
		t.Run(tc.code, func(t *testing.T) {
			inst, err := quantity.NewInstrument(tc.code, 0, tc.dimension, 2)
			require.NoError(t, err)

			_, err = quantity.NewQuantityValidated[quantity.Monetary](decimal.NewFromInt(100), inst)
			require.Error(t, err)
			assert.ErrorIs(t, err, quantity.ErrDimensionMismatch)
		})
	}
}

// TestNewQuantityValidated_Commodity_AcceptsAllNonCurrencyDimensions verifies that
// NewQuantityValidated[Commodity] accepts instruments for all non-CURRENCY dimensions.
func TestNewQuantityValidated_Commodity_AcceptsAllNonCurrencyDimensions(t *testing.T) {
	nonCurrencyInstruments := []struct {
		code      string
		dimension string
	}{
		{"KWH", "ENERGY"},
		{"CARBON_CREDIT", "CARBON"},
		{"GPU_HOUR", "COMPUTE"},
		{"GB", "DATA"},
		{"SECOND", "TIME"},
		{"KG", "MASS"},
		{"LITER", "VOLUME"},
		{"UNIT", "COUNT"},
	}

	for _, tc := range nonCurrencyInstruments {
		t.Run(tc.code, func(t *testing.T) {
			inst, err := quantity.NewInstrument(tc.code, 0, tc.dimension, 2)
			require.NoError(t, err)

			asset, err := quantity.NewQuantityValidated[quantity.Commodity](decimal.NewFromInt(100), inst)
			require.NoError(t, err)
			assert.Equal(t, tc.code, asset.Instrument.Code)
		})
	}
}

// TestAsset_ArithmeticSameDimension verifies that Assets with same instruments
// support Add and Subtract correctly.
func TestAsset_ArithmeticSameDimension(t *testing.T) {
	inst, err := quantity.NewInstrument("KWH", 0, "ENERGY", 3)
	require.NoError(t, err)

	a := quantity.NewAsset(decimal.NewFromFloat(3.5), inst)
	b := quantity.NewAsset(decimal.NewFromFloat(1.5), inst)

	sum, err := a.Add(b)
	require.NoError(t, err)
	assert.Equal(t, "5", sum.Amount.String())

	diff, err := a.Subtract(b)
	require.NoError(t, err)
	assert.Equal(t, "2", diff.Amount.String())
}

// TestAsset_ArithmeticMismatchedInstruments verifies that Assets with different instruments
// return ErrInstrumentMismatch on arithmetic.
func TestAsset_ArithmeticMismatchedInstruments(t *testing.T) {
	kwhInst, _ := quantity.NewInstrument("KWH", 0, "ENERGY", 3)
	gpuInst, _ := quantity.NewInstrument("GPU_HOUR", 0, "COMPUTE", 6)

	kwh := quantity.NewAsset(decimal.NewFromFloat(10), kwhInst)
	gpu := quantity.NewAsset(decimal.NewFromFloat(5), gpuInst)

	_, err := kwh.Add(gpu)
	require.Error(t, err)
	assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)
}

// TestAsset_AsAsset verifies the AsAsset() conversion returns correctly for commodities.
func TestAsset_AsAsset(t *testing.T) {
	inst, _ := quantity.NewInstrument("KWH", 0, "ENERGY", 3)
	asset := quantity.NewAsset(decimal.NewFromFloat(5.5), inst)

	result, ok := asset.AsAsset()
	assert.True(t, ok)
	assert.True(t, result.Amount.Equal(decimal.NewFromFloat(5.5)))
}

// TestAsset_AsMoney verifies that commodity quantities return (zero, false) from AsMoney().
func TestAsset_AsMoney(t *testing.T) {
	inst, _ := quantity.NewInstrument("KWH", 0, "ENERGY", 3)
	asset := quantity.NewAsset(decimal.NewFromFloat(5.5), inst)

	_, ok := asset.AsMoney()
	assert.False(t, ok)
}
