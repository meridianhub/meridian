package quantity_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// Test fixtures for value tests - reusing the same pattern as quantity_test.go
var (
	usdInst, _     = quantity.NewInstrument("USD", 1, "CURRENCY", 2)
	eurInst, _     = quantity.NewInstrument("EUR", 1, "CURRENCY", 2)
	kwhInst, _     = quantity.NewInstrument("KWH", 1, "ENERGY", 4)
	gpuHourInst, _ = quantity.NewInstrument("GPU_HOUR", 1, "COMPUTE", 6)
	carbonInst, _  = quantity.NewInstrument("CARBON", 1, "CARBON", 2)
)

// =============================================================================
// Subtask 6.1: Value interface implementation tests
// =============================================================================

func TestValue_InterfaceImplementation(t *testing.T) {
	t.Run("Money implements Value", func(t *testing.T) {
		money := quantity.NewMoney(decimal.NewFromInt(100), usdInst)

		// Verify it satisfies the interface
		var qv quantity.Value = money
		assert.NotNil(t, qv)
	})

	t.Run("Asset implements Value", func(t *testing.T) {
		asset := quantity.NewAsset(decimal.NewFromInt(500), kwhInst)

		// Verify it satisfies the interface
		var qv quantity.Value = asset
		assert.NotNil(t, qv)
	})

	t.Run("Qty[Monetary] implements Value", func(t *testing.T) {
		q := quantity.New[quantity.Monetary](decimal.NewFromInt(100), usdInst)

		var qv quantity.Value = q
		assert.NotNil(t, qv)
	})

	t.Run("Qty[Commodity] implements Value", func(t *testing.T) {
		q := quantity.New[quantity.Commodity](decimal.NewFromInt(500), kwhInst)

		var qv quantity.Value = q
		assert.NotNil(t, qv)
	})
}

func TestValue_DimensionName(t *testing.T) {
	t.Run("monetary returns CURRENCY", func(t *testing.T) {
		money := quantity.NewMoney(decimal.NewFromInt(100), usdInst)
		assert.Equal(t, "CURRENCY", money.DimensionName())
	})

	t.Run("energy asset returns ENERGY", func(t *testing.T) {
		asset := quantity.NewAsset(decimal.NewFromInt(500), kwhInst)
		assert.Equal(t, "ENERGY", asset.DimensionName())
	})

	t.Run("compute asset returns COMPUTE", func(t *testing.T) {
		asset := quantity.NewAsset(decimal.NewFromInt(24), gpuHourInst)
		assert.Equal(t, "COMPUTE", asset.DimensionName())
	})

	t.Run("carbon asset returns CARBON", func(t *testing.T) {
		asset := quantity.NewAsset(decimal.NewFromInt(100), carbonInst)
		assert.Equal(t, "CARBON", asset.DimensionName())
	})
}

func TestValue_GetAmount(t *testing.T) {
	t.Run("money amount", func(t *testing.T) {
		amount := decimal.NewFromFloat(123.45)
		money := quantity.NewMoney(amount, usdInst)
		assert.True(t, amount.Equal(money.GetAmount()))
	})

	t.Run("asset amount", func(t *testing.T) {
		amount := decimal.NewFromFloat(1000.5678)
		asset := quantity.NewAsset(amount, kwhInst)
		assert.True(t, amount.Equal(asset.GetAmount()))
	})

	t.Run("zero amount", func(t *testing.T) {
		money := quantity.ZeroMoney(usdInst)
		assert.True(t, money.GetAmount().IsZero())
	})

	t.Run("negative amount", func(t *testing.T) {
		amount := decimal.NewFromFloat(-50.00)
		money := quantity.NewMoney(amount, usdInst)
		assert.True(t, amount.Equal(money.GetAmount()))
		assert.True(t, money.GetAmount().IsNegative())
	})
}

func TestValue_GetInstrument(t *testing.T) {
	t.Run("money instrument", func(t *testing.T) {
		money := quantity.NewMoney(decimal.NewFromInt(100), usdInst)
		inst := money.GetInstrument()
		assert.Equal(t, "USD", inst.Code)
		assert.Equal(t, uint32(1), inst.Version)
		assert.Equal(t, "CURRENCY", inst.Dimension)
		assert.Equal(t, 2, inst.Precision)
	})

	t.Run("asset instrument", func(t *testing.T) {
		asset := quantity.NewAsset(decimal.NewFromInt(500), kwhInst)
		inst := asset.GetInstrument()
		assert.Equal(t, "KWH", inst.Code)
		assert.Equal(t, uint32(1), inst.Version)
		assert.Equal(t, "ENERGY", inst.Dimension)
		assert.Equal(t, 4, inst.Precision)
	})
}

// =============================================================================
// AsMoney and AsAsset type-safe accessor tests
// =============================================================================

func TestValue_AsMoney(t *testing.T) {
	t.Run("Money returns value and true", func(t *testing.T) {
		money := quantity.NewMoney(decimal.NewFromFloat(100.50), usdInst)

		result, ok := money.AsMoney()
		require.True(t, ok, "AsMoney should return true for Money")
		assert.True(t, money.Amount.Equal(result.Amount))
		assert.True(t, money.Instrument.Equal(result.Instrument))
	})

	t.Run("Asset returns zero and false", func(t *testing.T) {
		asset := quantity.NewAsset(decimal.NewFromFloat(500.1234), kwhInst)

		result, ok := asset.AsMoney()
		require.False(t, ok, "AsMoney should return false for Asset")
		assert.True(t, result.Amount.IsZero(), "returned Money should be zero value")
		assert.Equal(t, "", result.Instrument.Code, "returned Money should have empty instrument")
	})

	t.Run("through interface", func(t *testing.T) {
		var qv quantity.Value = quantity.NewMoney(decimal.NewFromInt(200), eurInst)

		result, ok := qv.AsMoney()
		require.True(t, ok)
		assert.Equal(t, "200", result.Amount.String())
		assert.Equal(t, "EUR", result.Instrument.Code)
	})
}

func TestValue_AsAsset(t *testing.T) {
	t.Run("Asset returns value and true", func(t *testing.T) {
		asset := quantity.NewAsset(decimal.NewFromFloat(500.1234), kwhInst)

		result, ok := asset.AsAsset()
		require.True(t, ok, "AsAsset should return true for Asset")
		assert.True(t, asset.Amount.Equal(result.Amount))
		assert.True(t, asset.Instrument.Equal(result.Instrument))
	})

	t.Run("Money returns zero and false", func(t *testing.T) {
		money := quantity.NewMoney(decimal.NewFromFloat(100.50), usdInst)

		result, ok := money.AsAsset()
		require.False(t, ok, "AsAsset should return false for Money")
		assert.True(t, result.Amount.IsZero(), "returned Asset should be zero value")
		assert.Equal(t, "", result.Instrument.Code, "returned Asset should have empty instrument")
	})

	t.Run("through interface", func(t *testing.T) {
		var qv quantity.Value = quantity.NewAsset(decimal.NewFromInt(1000), kwhInst)

		result, ok := qv.AsAsset()
		require.True(t, ok)
		assert.Equal(t, "1000", result.Amount.String())
		assert.Equal(t, "KWH", result.Instrument.Code)
	})

	t.Run("different commodity dimensions", func(t *testing.T) {
		// ENERGY
		energy := quantity.NewAsset(decimal.NewFromInt(100), kwhInst)
		_, ok := energy.AsAsset()
		assert.True(t, ok, "ENERGY dimension should be Asset")

		// COMPUTE
		compute := quantity.NewAsset(decimal.NewFromInt(24), gpuHourInst)
		_, ok = compute.AsAsset()
		assert.True(t, ok, "COMPUTE dimension should be Asset")

		// CARBON
		carbon := quantity.NewAsset(decimal.NewFromInt(50), carbonInst)
		_, ok = carbon.AsAsset()
		assert.True(t, ok, "CARBON dimension should be Asset")
	})
}

// =============================================================================
// Subtask 6.2: ParseQuantity bridge function tests
// =============================================================================

func TestParseQuantity_CurrencyToMoney(t *testing.T) {
	amount := decimal.NewFromFloat(123.45)

	qv, err := quantity.ParseQuantity(amount, usdInst)
	require.NoError(t, err)
	require.NotNil(t, qv)

	// Verify it's a Money
	assert.Equal(t, "CURRENCY", qv.DimensionName())

	money, ok := qv.AsMoney()
	require.True(t, ok, "ParseQuantity with CURRENCY should return Money")
	assert.True(t, amount.Equal(money.Amount))
	assert.Equal(t, "USD", money.Instrument.Code)

	// Should NOT be an Asset
	_, ok = qv.AsAsset()
	assert.False(t, ok)
}

func TestParseQuantity_CommodityToAsset(t *testing.T) {
	t.Run("ENERGY dimension", func(t *testing.T) {
		amount := decimal.NewFromFloat(500.1234)

		qv, err := quantity.ParseQuantity(amount, kwhInst)
		require.NoError(t, err)
		require.NotNil(t, qv)

		assert.Equal(t, "ENERGY", qv.DimensionName())

		asset, ok := qv.AsAsset()
		require.True(t, ok, "ParseQuantity with ENERGY should return Asset")
		assert.True(t, amount.Equal(asset.Amount))
		assert.Equal(t, "KWH", asset.Instrument.Code)

		_, ok = qv.AsMoney()
		assert.False(t, ok)
	})

	t.Run("COMPUTE dimension", func(t *testing.T) {
		amount := decimal.NewFromFloat(24.5)

		qv, err := quantity.ParseQuantity(amount, gpuHourInst)
		require.NoError(t, err)

		assert.Equal(t, "COMPUTE", qv.DimensionName())

		asset, ok := qv.AsAsset()
		require.True(t, ok)
		assert.Equal(t, "GPU_HOUR", asset.Instrument.Code)
	})

	t.Run("CARBON dimension", func(t *testing.T) {
		amount := decimal.NewFromFloat(100)

		qv, err := quantity.ParseQuantity(amount, carbonInst)
		require.NoError(t, err)

		assert.Equal(t, "CARBON", qv.DimensionName())

		asset, ok := qv.AsAsset()
		require.True(t, ok)
		assert.Equal(t, "CARBON", asset.Instrument.Code)
	})
}

func TestParseQuantity_UnknownDimension(t *testing.T) {
	// Create an instrument with empty dimension (invalid state)
	// This shouldn't happen in practice due to Instrument validation,
	// but we test the error handling anyway
	invalidInst := quantity.Instrument{
		Code:      "INVALID",
		Version:   1,
		Dimension: "", // Empty dimension
		Precision: 2,
	}

	_, err := quantity.ParseQuantity(decimal.NewFromInt(100), invalidInst)
	require.Error(t, err)
	assert.ErrorIs(t, err, quantity.ErrUnknownDimension)
}

func TestParseQuantityFromString(t *testing.T) {
	t.Run("valid amount string", func(t *testing.T) {
		qv, err := quantity.ParseQuantityFromString("123.45", usdInst)
		require.NoError(t, err)

		money, ok := qv.AsMoney()
		require.True(t, ok)
		assert.Equal(t, "123.45", money.Amount.String())
	})

	t.Run("invalid amount string", func(t *testing.T) {
		_, err := quantity.ParseQuantityFromString("not-a-number", usdInst)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInvalidDecimalString)
	})

	t.Run("empty string", func(t *testing.T) {
		_, err := quantity.ParseQuantityFromString("", usdInst)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInvalidDecimalString)
	})
}

// =============================================================================
// Subtask 6.3: NewQuantityValidated tests
// =============================================================================

func TestNewQuantityValidated_MonetaryMatching(t *testing.T) {
	t.Run("CURRENCY dimension matches Monetary", func(t *testing.T) {
		amount := decimal.NewFromFloat(100.50)

		money, err := quantity.NewQuantityValidated[quantity.Monetary](amount, usdInst)
		require.NoError(t, err)
		assert.True(t, amount.Equal(money.Amount))
		assert.Equal(t, "USD", money.Instrument.Code)
	})

	t.Run("EUR also matches Monetary", func(t *testing.T) {
		amount := decimal.NewFromFloat(200.00)

		money, err := quantity.NewQuantityValidated[quantity.Monetary](amount, eurInst)
		require.NoError(t, err)
		assert.Equal(t, "EUR", money.Instrument.Code)
	})
}

func TestNewQuantityValidated_MonetaryMismatch(t *testing.T) {
	t.Run("ENERGY dimension fails for Monetary", func(t *testing.T) {
		amount := decimal.NewFromFloat(500)

		_, err := quantity.NewQuantityValidated[quantity.Monetary](amount, kwhInst)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrDimensionMismatch)
	})

	t.Run("COMPUTE dimension fails for Monetary", func(t *testing.T) {
		amount := decimal.NewFromFloat(24)

		_, err := quantity.NewQuantityValidated[quantity.Monetary](amount, gpuHourInst)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrDimensionMismatch)
	})

	t.Run("CARBON dimension fails for Monetary", func(t *testing.T) {
		amount := decimal.NewFromFloat(100)

		_, err := quantity.NewQuantityValidated[quantity.Monetary](amount, carbonInst)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrDimensionMismatch)
	})
}

func TestNewQuantityValidated_CommodityMatching(t *testing.T) {
	t.Run("ENERGY dimension matches Commodity", func(t *testing.T) {
		amount := decimal.NewFromFloat(500.1234)

		asset, err := quantity.NewQuantityValidated[quantity.Commodity](amount, kwhInst)
		require.NoError(t, err)
		assert.True(t, amount.Equal(asset.Amount))
		assert.Equal(t, "KWH", asset.Instrument.Code)
	})

	t.Run("COMPUTE dimension matches Commodity", func(t *testing.T) {
		amount := decimal.NewFromFloat(24.5)

		asset, err := quantity.NewQuantityValidated[quantity.Commodity](amount, gpuHourInst)
		require.NoError(t, err)
		assert.Equal(t, "GPU_HOUR", asset.Instrument.Code)
	})

	t.Run("CARBON dimension matches Commodity", func(t *testing.T) {
		amount := decimal.NewFromFloat(100)

		asset, err := quantity.NewQuantityValidated[quantity.Commodity](amount, carbonInst)
		require.NoError(t, err)
		assert.Equal(t, "CARBON", asset.Instrument.Code)
	})
}

func TestNewQuantityValidated_CommodityMismatch(t *testing.T) {
	t.Run("CURRENCY dimension fails for Commodity", func(t *testing.T) {
		amount := decimal.NewFromFloat(100)

		_, err := quantity.NewQuantityValidated[quantity.Commodity](amount, usdInst)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrDimensionMismatch)
	})

	t.Run("EUR also fails for Commodity", func(t *testing.T) {
		amount := decimal.NewFromFloat(200)

		_, err := quantity.NewQuantityValidated[quantity.Commodity](amount, eurInst)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrDimensionMismatch)
	})
}

// =============================================================================
// Integration tests: Real-world scenarios
// =============================================================================

func TestValue_DatabaseRoundTrip(t *testing.T) {
	// Simulate loading a quantity from database where we don't know the dimension
	// at compile time, then converting to typed quantity for operations

	t.Run("monetary quantity round trip", func(t *testing.T) {
		// Simulate database values
		amountStr := "1000.00"
		code := "USD"
		version := uint32(1)
		dimension := "CURRENCY"
		precision := 2

		// Reconstruct instrument
		inst, err := quantity.NewInstrument(code, version, dimension, precision)
		require.NoError(t, err)

		// Parse quantity (runtime dimension detection)
		amount, err := decimal.NewFromString(amountStr)
		require.NoError(t, err)

		qv, err := quantity.ParseQuantity(amount, inst)
		require.NoError(t, err)

		// Convert to typed quantity for operations
		money, ok := qv.AsMoney()
		require.True(t, ok)

		// Perform typed operations
		otherMoney, _ := quantity.NewMoneyFromString("250.00", usdInst)
		total, err := money.Add(otherMoney)
		require.NoError(t, err)
		assert.Equal(t, "1250.00", total.Amount.StringFixed(2))
	})

	t.Run("commodity quantity round trip", func(t *testing.T) {
		// Simulate database values
		amountStr := "500.5000"
		code := "KWH"
		version := uint32(1)
		dimension := "ENERGY"
		precision := 4

		// Reconstruct instrument
		inst, err := quantity.NewInstrument(code, version, dimension, precision)
		require.NoError(t, err)

		// Parse quantity
		amount, err := decimal.NewFromString(amountStr)
		require.NoError(t, err)

		qv, err := quantity.ParseQuantity(amount, inst)
		require.NoError(t, err)

		// Convert to typed quantity
		asset, ok := qv.AsAsset()
		require.True(t, ok)

		// Perform typed operations
		otherAsset, _ := quantity.NewAssetFromString("100.2500", kwhInst)
		total, err := asset.Add(otherAsset)
		require.NoError(t, err)
		assert.Equal(t, "600.7500", total.Amount.StringFixed(4))
	})
}

func TestValue_MixedCollection(t *testing.T) {
	// Scenario: A portfolio contains both currencies and commodities
	// stored as Value for uniform handling

	portfolio := []quantity.Value{
		quantity.NewMoney(decimal.NewFromInt(10000), usdInst),
		quantity.NewMoney(decimal.NewFromInt(5000), eurInst),
		quantity.NewAsset(decimal.NewFromInt(1000), kwhInst),
		quantity.NewAsset(decimal.NewFromInt(50), gpuHourInst),
	}

	// Count by dimension type
	var monetaryCount, commodityCount int
	for _, qv := range portfolio {
		if _, ok := qv.AsMoney(); ok {
			monetaryCount++
		}
		if _, ok := qv.AsAsset(); ok {
			commodityCount++
		}
	}

	assert.Equal(t, 2, monetaryCount, "should have 2 monetary quantities")
	assert.Equal(t, 2, commodityCount, "should have 2 commodity quantities")

	// Sum USD amounts only
	var usdTotal decimal.Decimal
	for _, qv := range portfolio {
		if money, ok := qv.AsMoney(); ok {
			if money.Instrument.Code == "USD" {
				usdTotal = usdTotal.Add(money.Amount)
			}
		}
	}
	assert.Equal(t, "10000", usdTotal.String())
}

func TestValue_TypeDispatch(t *testing.T) {
	// Demonstrate type dispatch pattern with Value

	processQuantity := func(qv quantity.Value) string {
		if money, ok := qv.AsMoney(); ok {
			return "Monetary: " + money.String()
		}
		if asset, ok := qv.AsAsset(); ok {
			return "Commodity: " + asset.String()
		}
		return "Unknown"
	}

	money := quantity.NewMoney(decimal.NewFromFloat(99.99), usdInst)
	asset := quantity.NewAsset(decimal.NewFromFloat(123.4567), kwhInst)

	assert.Equal(t, "Monetary: 99.99 USD", processQuantity(money))
	assert.Equal(t, "Commodity: 123.4567 KWH", processQuantity(asset))
}

// =============================================================================
// Edge cases
// =============================================================================

func TestValue_ZeroValues(t *testing.T) {
	t.Run("zero money", func(t *testing.T) {
		money := quantity.ZeroMoney(usdInst)
		assert.True(t, money.GetAmount().IsZero())

		result, ok := money.AsMoney()
		require.True(t, ok)
		assert.True(t, result.IsZero())
	})

	t.Run("zero asset", func(t *testing.T) {
		asset := quantity.ZeroAsset(kwhInst)
		assert.True(t, asset.GetAmount().IsZero())

		result, ok := asset.AsAsset()
		require.True(t, ok)
		assert.True(t, result.IsZero())
	})
}

func TestValue_NegativeAmounts(t *testing.T) {
	t.Run("negative money", func(t *testing.T) {
		money := quantity.NewMoney(decimal.NewFromFloat(-100.50), usdInst)

		assert.True(t, money.GetAmount().IsNegative())

		result, ok := money.AsMoney()
		require.True(t, ok)
		assert.True(t, result.IsNegative())
	})

	t.Run("negative asset", func(t *testing.T) {
		asset := quantity.NewAsset(decimal.NewFromFloat(-500.1234), kwhInst)

		assert.True(t, asset.GetAmount().IsNegative())

		result, ok := asset.AsAsset()
		require.True(t, ok)
		assert.True(t, result.IsNegative())
	})
}

func TestValue_LargeAmounts(t *testing.T) {
	t.Run("very large money", func(t *testing.T) {
		amount, _ := decimal.NewFromString("999999999999999999.99")
		money := quantity.NewMoney(amount, usdInst)

		result, ok := money.AsMoney()
		require.True(t, ok)
		assert.True(t, amount.Equal(result.Amount))
	})

	t.Run("very large asset", func(t *testing.T) {
		amount, _ := decimal.NewFromString("999999999999999999.9999")
		asset := quantity.NewAsset(amount, kwhInst)

		result, ok := asset.AsAsset()
		require.True(t, ok)
		assert.True(t, amount.Equal(result.Amount))
	})
}

func TestValue_HighPrecision(t *testing.T) {
	highPrecisionInst, _ := quantity.NewInstrument("PRECISE", 1, "COUNT", 18)
	amount, _ := decimal.NewFromString("1.123456789012345678")

	asset := quantity.NewAsset(amount, highPrecisionInst)

	result, ok := asset.AsAsset()
	require.True(t, ok)
	assert.Equal(t, "1.123456789012345678", result.Amount.String())
}

func TestParseQuantity_InvalidNonEmptyDimension(t *testing.T) {
	// Create an instrument with a non-empty dimension that is not in ValidDimensions.
	// This bypasses the empty-string case and exercises the ValidDimensions check.
	invalidInst := quantity.Instrument{
		Code:      "BOGUS",
		Version:   1,
		Dimension: "NOT_A_VALID_DIMENSION",
		Precision: 2,
	}

	_, err := quantity.ParseQuantity(decimal.NewFromInt(42), invalidInst)
	require.Error(t, err)
	assert.ErrorIs(t, err, quantity.ErrUnknownDimension)
}
