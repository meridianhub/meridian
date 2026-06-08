package quantity_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// Test fixtures - valid instruments for reuse across tests.
var (
	usd, _     = quantity.NewInstrument("USD", 1, "CURRENCY", 2)
	usdV2, _   = quantity.NewInstrument("USD", 2, "CURRENCY", 2)
	eur, _     = quantity.NewInstrument("EUR", 1, "CURRENCY", 2)
	kwh, _     = quantity.NewInstrument("KWH", 1, "ENERGY", 4)
	gpuHour, _ = quantity.NewInstrument("GPU_HOUR", 1, "COMPUTE", 6)
)

// =============================================================================
// Subtask 4.1: Quantity[D] generic struct with Dimension constraint
// =============================================================================

func TestQty_New(t *testing.T) {
	amount := decimal.NewFromFloat(100.50)
	q := quantity.New[quantity.Monetary](amount, usd)

	assert.True(t, q.Amount.Equal(amount))
	assert.Equal(t, usd, q.Instrument)
}

func TestQty_NewFromString(t *testing.T) {
	t.Run("valid decimal string", func(t *testing.T) {
		q, err := quantity.NewFromString[quantity.Monetary]("123.45", usd)
		require.NoError(t, err)
		assert.Equal(t, "123.45", q.Amount.String())
		assert.Equal(t, usd, q.Instrument)
	})

	t.Run("invalid decimal string", func(t *testing.T) {
		_, err := quantity.NewFromString[quantity.Monetary]("not-a-number", usd)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInvalidDecimalString)
	})

	t.Run("empty string", func(t *testing.T) {
		_, err := quantity.NewFromString[quantity.Monetary]("", usd)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInvalidDecimalString)
	})
}

func TestQty_NewFromInt(t *testing.T) {
	q := quantity.NewFromInt[quantity.Monetary](100, usd)
	assert.Equal(t, "100", q.Amount.String())
	assert.Equal(t, usd, q.Instrument)
}

func TestQty_Zero(t *testing.T) {
	q := quantity.Zero[quantity.Monetary](usd)
	assert.True(t, q.Amount.IsZero())
	assert.True(t, q.IsZero())
	assert.Equal(t, usd, q.Instrument)
}

// TestQuantity_CompileTimeDimensionSafety documents the compile-time type safety.
// The following code would NOT compile if uncommented:
//
//	func wouldNotCompile() {
//		money := quantity.New[quantity.Monetary](decimal.NewFromInt(100), usd)
//		energy := quantity.New[quantity.Commodity](decimal.NewFromInt(50), kwh)
//		money = energy // compile error: cannot use energy (variable of type Quantity[Commodity]) as Quantity[Monetary] value
//		_, _ = money.Add(energy) // compile error: cannot use energy as Quantity[Monetary]
//	}
func TestQty_CompileTimeDimensionSafety(t *testing.T) {
	t.Log("Compile-time safety: Qty[Monetary] and Qty[Commodity] are distinct types")
	t.Log("Attempting to assign or operate between them results in a compile error")

	// We can verify they're different types at runtime
	var money quantity.Qty[quantity.Monetary]
	var energy quantity.Qty[quantity.Commodity]

	// These are zero values - just verifying the types are distinct
	// Zero value has empty dimension string
	assert.Equal(t, "", money.Instrument.Dimension)
	assert.Equal(t, "", energy.Instrument.Dimension)
}

// =============================================================================
// Subtask 4.2: Arithmetic operations with same-instrument validation
// =============================================================================

func TestQty_Add(t *testing.T) {
	t.Run("same instrument succeeds", func(t *testing.T) {
		q1, err := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		require.NoError(t, err)
		q2, err := quantity.NewFromString[quantity.Monetary]("50.25", usd)
		require.NoError(t, err)

		result, err := q1.Add(q2)
		require.NoError(t, err)
		assert.Equal(t, "150.25", result.Amount.String())
		assert.Equal(t, usd, result.Instrument)
	})

	t.Run("different currency code fails", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("50.00", eur)

		_, err := q1.Add(q2)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)
	})

	t.Run("different version fails", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("50.00", usdV2)

		_, err := q1.Add(q2)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)
	})

	t.Run("add negative amount", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("-30.00", usd)

		result, err := q1.Add(q2)
		require.NoError(t, err)
		assert.Equal(t, "70", result.Amount.String())
	})
}

func TestQty_Subtract(t *testing.T) {
	t.Run("same instrument succeeds", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("30.25", usd)

		result, err := q1.Subtract(q2)
		require.NoError(t, err)
		assert.Equal(t, "69.75", result.Amount.String())
	})

	t.Run("different instrument fails", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("30.00", eur)

		_, err := q1.Subtract(q2)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)
	})

	t.Run("result can be negative", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("30.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)

		result, err := q1.Subtract(q2)
		require.NoError(t, err)
		assert.Equal(t, "-70", result.Amount.String())
		assert.True(t, result.IsNegative())
	})
}

func TestQty_Multiply(t *testing.T) {
	t.Run("multiply by integer", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("10.00", usd)
		result := q.Multiply(decimal.NewFromInt(3))
		assert.Equal(t, "30.00", result.Amount.StringFixed(2))
	})

	t.Run("multiply by decimal with rounding", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		// 100 * 0.333 = 33.3 -> rounds to 33.30 (precision 2)
		result := q.Multiply(decimal.NewFromFloat(0.333))
		assert.Equal(t, "33.30", result.Amount.StringFixed(2))
	})

	t.Run("multiply by zero", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		result := q.Multiply(decimal.Zero)
		assert.True(t, result.IsZero())
	})

	t.Run("multiply by negative", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("50.00", usd)
		result := q.Multiply(decimal.NewFromInt(-2))
		assert.Equal(t, "-100.00", result.Amount.StringFixed(2))
	})

	t.Run("banker's rounding (round half to even)", func(t *testing.T) {
		// Banker's rounding: 0.5 rounds to nearest even
		q, _ := quantity.NewFromString[quantity.Monetary]("1.00", usd)

		// 1.00 * 2.345 = 2.345 -> banker's rounds to 2.34 (4 is even)
		result := q.Multiply(decimal.NewFromFloat(2.345))
		assert.Equal(t, "2.34", result.Amount.StringFixed(2))

		// 1.00 * 2.355 = 2.355 -> banker's rounds to 2.36 (6 is even)
		result = q.Multiply(decimal.NewFromFloat(2.355))
		assert.Equal(t, "2.36", result.Amount.StringFixed(2))
	})
}

func TestQty_MultiplyString(t *testing.T) {
	t.Run("valid factor string", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		result, err := q.MultiplyString("0.5")
		require.NoError(t, err)
		assert.Equal(t, "50.00", result.Amount.StringFixed(2))
	})

	t.Run("invalid factor string", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		_, err := q.MultiplyString("invalid")
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInvalidDecimalString)
	})
}

func TestQty_Divide(t *testing.T) {
	t.Run("divide evenly", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		result, err := q.Divide(decimal.NewFromInt(4))
		require.NoError(t, err)
		assert.Equal(t, "25.00", result.Amount.StringFixed(2))
	})

	t.Run("divide with rounding", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		// 100 / 3 = 33.333... -> rounds to 33.33
		result, err := q.Divide(decimal.NewFromInt(3))
		require.NoError(t, err)
		assert.Equal(t, "33.33", result.Amount.StringFixed(2))
	})

	t.Run("division by zero fails", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		_, err := q.Divide(decimal.Zero)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrDivisionByZero)
	})

	t.Run("divide negative by positive", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("-100.00", usd)
		result, err := q.Divide(decimal.NewFromInt(4))
		require.NoError(t, err)
		assert.Equal(t, "-25.00", result.Amount.StringFixed(2))
	})

	t.Run("high precision instrument", func(t *testing.T) {
		// KWH has precision 4
		q, _ := quantity.NewFromString[quantity.Commodity]("1.0000", kwh)
		result, err := q.Divide(decimal.NewFromInt(3))
		require.NoError(t, err)
		assert.Equal(t, "0.3333", result.Amount.StringFixed(4))
	})
}

func TestQty_DivideString(t *testing.T) {
	t.Run("valid divisor string", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		result, err := q.DivideString("4")
		require.NoError(t, err)
		assert.Equal(t, "25.00", result.Amount.StringFixed(2))
	})

	t.Run("invalid divisor string", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		_, err := q.DivideString("invalid")
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInvalidDecimalString)
	})

	t.Run("zero string divisor", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		_, err := q.DivideString("0")
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrDivisionByZero)
	})
}

func TestQty_Negate(t *testing.T) {
	t.Run("negate positive", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		result := q.Negate()
		assert.Equal(t, "-100", result.Amount.String())
		assert.True(t, result.IsNegative())
	})

	t.Run("negate negative", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("-100.00", usd)
		result := q.Negate()
		assert.Equal(t, "100", result.Amount.String())
		assert.True(t, result.IsPositive())
	})

	t.Run("negate zero", func(t *testing.T) {
		q := quantity.Zero[quantity.Monetary](usd)
		result := q.Negate()
		assert.True(t, result.IsZero())
	})
}

func TestQty_Abs(t *testing.T) {
	t.Run("abs of positive", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		result := q.Abs()
		assert.Equal(t, "100", result.Amount.String())
	})

	t.Run("abs of negative", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("-100.00", usd)
		result := q.Abs()
		assert.Equal(t, "100", result.Amount.String())
	})

	t.Run("abs of zero", func(t *testing.T) {
		q := quantity.Zero[quantity.Monetary](usd)
		result := q.Abs()
		assert.True(t, result.IsZero())
	})
}

func TestQty_IsZero(t *testing.T) {
	t.Run("zero amount", func(t *testing.T) {
		q := quantity.Zero[quantity.Monetary](usd)
		assert.True(t, q.IsZero())
	})

	t.Run("non-zero positive", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("0.01", usd)
		assert.False(t, q.IsZero())
	})

	t.Run("non-zero negative", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("-0.01", usd)
		assert.False(t, q.IsZero())
	})
}

func TestQty_IsNegative(t *testing.T) {
	t.Run("negative amount", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("-100.00", usd)
		assert.True(t, q.IsNegative())
	})

	t.Run("positive amount", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		assert.False(t, q.IsNegative())
	})

	t.Run("zero amount", func(t *testing.T) {
		q := quantity.Zero[quantity.Monetary](usd)
		assert.False(t, q.IsNegative())
	})
}

func TestQty_IsPositive(t *testing.T) {
	t.Run("positive amount", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		assert.True(t, q.IsPositive())
	})

	t.Run("negative amount", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("-100.00", usd)
		assert.False(t, q.IsPositive())
	})

	t.Run("zero amount", func(t *testing.T) {
		q := quantity.Zero[quantity.Monetary](usd)
		assert.False(t, q.IsPositive())
	})
}

func TestQty_Compare(t *testing.T) {
	t.Run("less than", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("50.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)

		cmp, err := q1.Compare(q2)
		require.NoError(t, err)
		assert.Equal(t, -1, cmp)
	})

	t.Run("equal", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)

		cmp, err := q1.Compare(q2)
		require.NoError(t, err)
		assert.Equal(t, 0, cmp)
	})

	t.Run("greater than", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("150.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)

		cmp, err := q1.Compare(q2)
		require.NoError(t, err)
		assert.Equal(t, 1, cmp)
	})

	t.Run("different instrument fails", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("100.00", eur)

		_, err := q1.Compare(q2)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)
	})
}

func TestQty_Equal(t *testing.T) {
	t.Run("equal quantities", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)

		assert.True(t, q1.Equal(q2))
	})

	t.Run("different amounts", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("50.00", usd)

		assert.False(t, q1.Equal(q2))
	})

	t.Run("different instruments returns false (no error)", func(t *testing.T) {
		q1, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
		q2, _ := quantity.NewFromString[quantity.Monetary]("100.00", eur)

		assert.False(t, q1.Equal(q2))
	})
}

func TestQty_ComparisonHelpers(t *testing.T) {
	q1, _ := quantity.NewFromString[quantity.Monetary]("50.00", usd)
	q2, _ := quantity.NewFromString[quantity.Monetary]("100.00", usd)
	q3, _ := quantity.NewFromString[quantity.Monetary]("50.00", usd)

	t.Run("LessThan", func(t *testing.T) {
		lt, err := q1.LessThan(q2)
		require.NoError(t, err)
		assert.True(t, lt)

		lt, err = q2.LessThan(q1)
		require.NoError(t, err)
		assert.False(t, lt)

		// Equal-value boundary: LessThan must be strict (false on equal). This
		// kills the CONDITIONALS_BOUNDARY mutant that weakens `cmp < 0` to
		// `cmp <= 0` (quantity.go:268).
		lt, err = q1.LessThan(q3)
		require.NoError(t, err)
		assert.False(t, lt, "LessThan must be false for equal quantities")
	})

	t.Run("LessThanOrEqual", func(t *testing.T) {
		lte, err := q1.LessThanOrEqual(q2)
		require.NoError(t, err)
		assert.True(t, lte)

		lte, err = q1.LessThanOrEqual(q3)
		require.NoError(t, err)
		assert.True(t, lte)

		lte, err = q2.LessThanOrEqual(q1)
		require.NoError(t, err)
		assert.False(t, lte)
	})

	t.Run("GreaterThan", func(t *testing.T) {
		gt, err := q2.GreaterThan(q1)
		require.NoError(t, err)
		assert.True(t, gt)

		gt, err = q1.GreaterThan(q2)
		require.NoError(t, err)
		assert.False(t, gt)

		// Equal-value boundary: GreaterThan must be strict (false on equal). This
		// kills the CONDITIONALS_BOUNDARY mutant that weakens `cmp > 0` to
		// `cmp >= 0` (quantity.go:288).
		gt, err = q1.GreaterThan(q3)
		require.NoError(t, err)
		assert.False(t, gt, "GreaterThan must be false for equal quantities")
	})

	t.Run("GreaterThanOrEqual", func(t *testing.T) {
		gte, err := q2.GreaterThanOrEqual(q1)
		require.NoError(t, err)
		assert.True(t, gte)

		gte, err = q1.GreaterThanOrEqual(q3)
		require.NoError(t, err)
		assert.True(t, gte)

		gte, err = q1.GreaterThanOrEqual(q2)
		require.NoError(t, err)
		assert.False(t, gte)
	})

	t.Run("comparison with different instrument errors", func(t *testing.T) {
		qEur, _ := quantity.NewFromString[quantity.Monetary]("50.00", eur)

		_, err := q1.LessThan(qEur)
		assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)

		_, err = q1.LessThanOrEqual(qEur)
		assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)

		_, err = q1.GreaterThan(qEur)
		assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)

		_, err = q1.GreaterThanOrEqual(qEur)
		assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)
	})
}

func TestQty_Round(t *testing.T) {
	t.Run("round to precision", func(t *testing.T) {
		// USD has precision 2
		q, _ := quantity.NewFromString[quantity.Monetary]("123.456", usd)
		result := q.Round()
		assert.Equal(t, "123.46", result.Amount.StringFixed(2))
	})

	t.Run("already at precision", func(t *testing.T) {
		q, _ := quantity.NewFromString[quantity.Monetary]("123.45", usd)
		result := q.Round()
		assert.Equal(t, "123.45", result.Amount.StringFixed(2))
	})

	t.Run("high precision instrument", func(t *testing.T) {
		// GPU_HOUR has precision 6
		q, _ := quantity.NewFromString[quantity.Commodity]("1.12345678", gpuHour)
		result := q.Round()
		assert.Equal(t, "1.123457", result.Amount.StringFixed(6))
	})
}

func TestQty_String(t *testing.T) {
	q, _ := quantity.NewFromString[quantity.Monetary]("123.45", usd)
	assert.Equal(t, "123.45 USD", q.String())

	qEnergy, _ := quantity.NewFromString[quantity.Commodity]("1000.1234", kwh)
	assert.Equal(t, "1000.1234 KWH", qEnergy.String())
}

// =============================================================================
// Subtask 4.3: Money and Asset type aliases
// =============================================================================

func TestMoney_TypeAlias(t *testing.T) {
	// Money is Qty[Monetary]
	m := quantity.NewMoney(decimal.NewFromInt(100), usd)

	assert.Equal(t, "100", m.Amount.String())
	assert.Equal(t, usd, m.Instrument)
}

func TestNewMoney(t *testing.T) {
	m := quantity.NewMoney(decimal.NewFromFloat(123.45), usd)
	assert.Equal(t, "123.45", m.Amount.String())
	assert.Equal(t, usd, m.Instrument)
}

func TestNewMoneyFromString(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		m, err := quantity.NewMoneyFromString("999.99", usd)
		require.NoError(t, err)
		assert.Equal(t, "999.99", m.Amount.String())
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := quantity.NewMoneyFromString("invalid", usd)
		require.Error(t, err)
	})
}

func TestNewMoneyFromInt(t *testing.T) {
	m := quantity.NewMoneyFromInt(500, eur)
	assert.Equal(t, "500", m.Amount.String())
	assert.Equal(t, eur, m.Instrument)
}

func TestZeroMoney(t *testing.T) {
	m := quantity.ZeroMoney(usd)
	assert.True(t, m.IsZero())
	assert.Equal(t, usd, m.Instrument)
}

func TestAsset_TypeAlias(t *testing.T) {
	// Asset is Qty[Commodity]
	a := quantity.NewAsset(decimal.NewFromInt(1000), kwh)

	assert.Equal(t, "1000", a.Amount.String())
	assert.Equal(t, kwh, a.Instrument)
}

func TestNewAsset(t *testing.T) {
	a := quantity.NewAsset(decimal.NewFromFloat(500.1234), kwh)
	assert.Equal(t, "500.1234", a.Amount.String())
	assert.Equal(t, kwh, a.Instrument)
}

func TestNewAssetFromString(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		a, err := quantity.NewAssetFromString("1000.5678", kwh)
		require.NoError(t, err)
		assert.Equal(t, "1000.5678", a.Amount.String())
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := quantity.NewAssetFromString("invalid", kwh)
		require.Error(t, err)
	})
}

func TestNewAssetFromInt(t *testing.T) {
	a := quantity.NewAssetFromInt(2000, gpuHour)
	assert.Equal(t, "2000", a.Amount.String())
	assert.Equal(t, gpuHour, a.Instrument)
}

func TestZeroAsset(t *testing.T) {
	a := quantity.ZeroAsset(kwh)
	assert.True(t, a.IsZero())
	assert.Equal(t, kwh, a.Instrument)
}

// =============================================================================
// Integration tests: Real-world scenarios
// =============================================================================

func TestQty_RealWorldScenario_CurrencyExchange(t *testing.T) {
	// Scenario: Convert 1000 USD to EUR at rate 0.92
	usdAmount, _ := quantity.NewMoneyFromString("1000.00", usd)

	// Cannot directly convert - must go through exchange rate multiplication
	// This produces a dimensionless value (the EUR amount)
	eurAmount := usdAmount.Multiply(decimal.NewFromFloat(0.92))

	// Note: The result is still in USD instrument (just the amount changed)
	// In a real system, you'd create a new EUR quantity with this amount
	assert.Equal(t, "920.00", eurAmount.Amount.StringFixed(2))
}

func TestQty_RealWorldScenario_EnergyBilling(t *testing.T) {
	// Scenario: Calculate energy bill
	// 500 KWH consumed at $0.15 per KWH

	energyConsumed, _ := quantity.NewAssetFromString("500.0000", kwh)
	pricePerKwh := decimal.NewFromFloat(0.15)

	// Calculate total cost (multiply energy by price rate)
	totalEnergy := energyConsumed.Multiply(pricePerKwh)

	// The result is 75.00 in the same instrument (KWH)
	// In a real billing system, this would be converted to currency
	assert.Equal(t, "75.0000", totalEnergy.Amount.StringFixed(4))
}

func TestQty_RealWorldScenario_LedgerBalance(t *testing.T) {
	// Scenario: Calculate account balance from transactions
	opening, _ := quantity.NewMoneyFromString("1000.00", usd)
	deposit1, _ := quantity.NewMoneyFromString("500.00", usd)
	withdrawal, _ := quantity.NewMoneyFromString("200.00", usd)
	deposit2, _ := quantity.NewMoneyFromString("150.00", usd)

	balance := opening
	var err error

	balance, err = balance.Add(deposit1)
	require.NoError(t, err)

	balance, err = balance.Subtract(withdrawal)
	require.NoError(t, err)

	balance, err = balance.Add(deposit2)
	require.NoError(t, err)

	assert.Equal(t, "1450.00", balance.Amount.StringFixed(2))
	assert.True(t, balance.IsPositive())
}

func TestQty_RealWorldScenario_GPUBilling(t *testing.T) {
	// Scenario: Meridian's multi-asset capability
	// Bill for 24.5 GPU hours at a rate, then add two different jobs

	job1, _ := quantity.NewAssetFromString("24.500000", gpuHour)
	job2, _ := quantity.NewAssetFromString("12.250000", gpuHour)

	total, err := job1.Add(job2)
	require.NoError(t, err)

	assert.Equal(t, "36.750000", total.Amount.StringFixed(6))
}

func TestQty_PreventCrossAssetMixing(t *testing.T) {
	// This is a critical test demonstrating Meridian's type safety
	// Energy (commodity) cannot be directly added to currency (monetary)
	// This is enforced at COMPILE TIME by the generic type parameter

	// The following would NOT compile:
	// money, _ := quantity.NewMoneyFromString("100.00", usd)
	// energy, _ := quantity.NewAssetFromString("500.00", kwh)
	// _, err := money.Add(energy) // compile error!

	t.Log("Cross-dimension operations fail at compile time")
	t.Log("Quantity[Monetary].Add(Quantity[Commodity]) does not compile")
}

// =============================================================================
// Immutability tests
// =============================================================================

func TestQty_Immutability(t *testing.T) {
	original, _ := quantity.NewMoneyFromString("100.00", usd)
	originalAmount := original.Amount.String()

	// All operations return new values, original unchanged
	_ = original.Negate()
	assert.Equal(t, originalAmount, original.Amount.String())

	_ = original.Abs()
	assert.Equal(t, originalAmount, original.Amount.String())

	_ = original.Multiply(decimal.NewFromInt(2))
	assert.Equal(t, originalAmount, original.Amount.String())

	_, _ = original.Divide(decimal.NewFromInt(2))
	assert.Equal(t, originalAmount, original.Amount.String())

	other, _ := quantity.NewMoneyFromString("50.00", usd)
	_, _ = original.Add(other)
	assert.Equal(t, originalAmount, original.Amount.String())

	_, _ = original.Subtract(other)
	assert.Equal(t, originalAmount, original.Amount.String())

	_ = original.Round()
	assert.Equal(t, originalAmount, original.Amount.String())
}

// =============================================================================
// Edge cases
// =============================================================================

func TestQty_EdgeCases(t *testing.T) {
	t.Run("very large numbers", func(t *testing.T) {
		q, err := quantity.NewMoneyFromString("999999999999999999.99", usd)
		require.NoError(t, err)
		assert.False(t, q.IsZero())

		doubled := q.Multiply(decimal.NewFromInt(2))
		assert.True(t, doubled.Amount.GreaterThan(q.Amount))
	})

	t.Run("very small numbers", func(t *testing.T) {
		q, err := quantity.NewMoneyFromString("0.01", usd)
		require.NoError(t, err)
		assert.False(t, q.IsZero())
		assert.True(t, q.IsPositive())
	})

	t.Run("negative zero", func(t *testing.T) {
		q, err := quantity.NewMoneyFromString("-0.00", usd)
		require.NoError(t, err)
		// Decimal library normalizes -0 to 0
		assert.True(t, q.IsZero())
		assert.False(t, q.IsNegative())
	})

	t.Run("precision boundary", func(t *testing.T) {
		// Max precision is 18
		highPrecisionInstr, _ := quantity.NewInstrument("PRECISE", 1, "COUNT", 18)
		q, err := quantity.NewFromString[quantity.Commodity]("1.123456789012345678", highPrecisionInstr)
		require.NoError(t, err)
		assert.Equal(t, "1.123456789012345678", q.Amount.String())
	})
}
