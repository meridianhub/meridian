package quantity

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustInstrument(code string, version uint32, dimension string, precision int) Instrument {
	inst, err := NewInstrument(code, version, dimension, precision)
	if err != nil {
		panic(err)
	}
	return inst
}

func TestParseQuantity_currency_returns_money(t *testing.T) {
	inst := mustInstrument("GBP", 1, "CURRENCY", 2)
	amount := decimal.NewFromFloat(100.50)

	val, err := ParseQuantity(amount, inst)
	require.NoError(t, err)

	assert.Equal(t, "CURRENCY", val.DimensionName())
	assert.True(t, amount.Equal(val.GetAmount()))
	assert.Equal(t, "GBP", val.GetInstrument().Code)

	money, ok := val.AsMoney()
	assert.True(t, ok)
	assert.True(t, amount.Equal(money.Amount))
}

func TestParseQuantity_energy_returns_asset(t *testing.T) {
	inst := mustInstrument("KWH", 1, "ENERGY", 4)
	amount := decimal.NewFromFloat(500.1234)

	val, err := ParseQuantity(amount, inst)
	require.NoError(t, err)

	assert.Equal(t, "ENERGY", val.DimensionName())
	assert.True(t, amount.Equal(val.GetAmount()))

	asset, ok := val.AsAsset()
	assert.True(t, ok)
	assert.True(t, amount.Equal(asset.Amount))
}

func TestParseQuantity_all_valid_commodity_dimensions(t *testing.T) {
	commodityDimensions := []string{"ENERGY", "MASS", "VOLUME", "TIME", "COMPUTE", "CARBON", "DATA", "COUNT"}

	for _, dim := range commodityDimensions {
		t.Run(dim, func(t *testing.T) {
			inst := mustInstrument("TEST", 1, dim, 2)
			val, err := ParseQuantity(decimal.NewFromInt(1), inst)
			require.NoError(t, err)
			assert.Equal(t, dim, val.DimensionName())

			_, ok := val.AsAsset()
			assert.True(t, ok)
		})
	}
}

func TestParseQuantity_empty_dimension_returns_error(t *testing.T) {
	inst := Instrument{Code: "BAD", Version: 1, Dimension: "", Precision: 2}

	_, err := ParseQuantity(decimal.NewFromInt(1), inst)
	assert.ErrorIs(t, err, ErrUnknownDimension)
}

func TestParseQuantity_invalid_dimension_returns_error(t *testing.T) {
	inst := Instrument{Code: "BAD", Version: 1, Dimension: "INVALID", Precision: 2}

	_, err := ParseQuantity(decimal.NewFromInt(1), inst)
	assert.ErrorIs(t, err, ErrUnknownDimension)
}

func TestParseQuantityFromString_valid(t *testing.T) {
	inst := mustInstrument("USD", 1, "CURRENCY", 2)

	val, err := ParseQuantityFromString("99.99", inst)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(99.99).Equal(val.GetAmount()))
}

func TestParseQuantityFromString_invalid_decimal(t *testing.T) {
	inst := mustInstrument("USD", 1, "CURRENCY", 2)

	_, err := ParseQuantityFromString("not-a-number", inst)
	assert.ErrorIs(t, err, ErrInvalidDecimalString)
}

func TestParseQuantityFromString_empty_string(t *testing.T) {
	inst := mustInstrument("USD", 1, "CURRENCY", 2)

	_, err := ParseQuantityFromString("", inst)
	assert.ErrorIs(t, err, ErrInvalidDecimalString)
}

func TestParseQuantity_negative_amount(t *testing.T) {
	inst := mustInstrument("USD", 1, "CURRENCY", 2)
	amount := decimal.NewFromFloat(-50.25)

	val, err := ParseQuantity(amount, inst)
	require.NoError(t, err)
	assert.True(t, amount.Equal(val.GetAmount()))
}

func TestParseQuantity_zero_amount(t *testing.T) {
	inst := mustInstrument("USD", 1, "CURRENCY", 2)

	val, err := ParseQuantity(decimal.Zero, inst)
	require.NoError(t, err)
	assert.True(t, decimal.Zero.Equal(val.GetAmount()))
}

func TestParseQuantity_high_precision_amount(t *testing.T) {
	inst := mustInstrument("BTC", 1, "CURRENCY", 8)
	amount, _ := decimal.NewFromString("0.00000001")

	val, err := ParseQuantity(amount, inst)
	require.NoError(t, err)
	assert.True(t, amount.Equal(val.GetAmount()))
}
