package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewMoney_ValidInput_CreatesMoney(t *testing.T) {
	money, err := NewMoney("GBP", 100)

	assert.NoError(t, err)
	assert.Equal(t, "GBP", money.InstrumentCode())
	assert.Equal(t, int64(100), toMinorUnits(money))
}

func TestNewMoney_EmptyCurrency_ReturnsError(t *testing.T) {
	_, err := NewMoney("", 100)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidCurrency)
}

func TestNewMoney_InvalidCurrency_ReturnsError(t *testing.T) {
	_, err := NewMoney("INVALID", 100)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidCurrency)
}

func TestMoney_Add_SameCurrency_ReturnsSum(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("GBP", 50)

	result, err := m1.Add(m2)

	assert.NoError(t, err)
	assert.Equal(t, int64(150), toMinorUnits(result))
	assert.Equal(t, "GBP", result.InstrumentCode())
}

func TestMoney_Add_DifferentCurrency_ReturnsError(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("USD", 50)

	_, err := m1.Add(m2)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentMismatch)
}

func TestMoney_Add_DoesNotMutateOriginal(t *testing.T) {
	original, _ := NewMoney("GBP", 100)
	originalAmount := toMinorUnits(original)
	other, _ := NewMoney("GBP", 50)

	_, _ = original.Add(other)

	assert.Equal(t, originalAmount, toMinorUnits(original),
		"original money should not be mutated by Add operation")
}

func TestMoney_Subtract_SameCurrency_ReturnsDifference(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("GBP", 30)

	result, err := m1.Subtract(m2)

	assert.NoError(t, err)
	assert.Equal(t, int64(70), toMinorUnits(result))
	assert.Equal(t, "GBP", result.InstrumentCode())
}

func TestMoney_Subtract_DifferentCurrency_ReturnsError(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("USD", 30)

	_, err := m1.Subtract(m2)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentMismatch)
}

func TestMoney_Subtract_DoesNotMutateOriginal(t *testing.T) {
	original, _ := NewMoney("GBP", 100)
	originalAmount := toMinorUnits(original)
	other, _ := NewMoney("GBP", 30)

	_, _ = original.Subtract(other)

	assert.Equal(t, originalAmount, toMinorUnits(original),
		"original money should not be mutated by Subtract operation")
}

func TestMoney_IsPositive_PositiveAmount_ReturnsTrue(t *testing.T) {
	money, _ := NewMoney("GBP", 100)

	assert.True(t, money.IsPositive())
}

func TestMoney_IsPositive_ZeroAmount_ReturnsFalse(t *testing.T) {
	money, _ := NewMoney("GBP", 0)

	assert.False(t, money.IsPositive())
}

func TestMoney_IsPositive_NegativeAmount_ReturnsFalse(t *testing.T) {
	money, _ := NewMoney("GBP", -50)

	assert.False(t, money.IsPositive())
}

func TestMoney_IsZero_ZeroAmount_ReturnsTrue(t *testing.T) {
	money, _ := NewMoney("GBP", 0)

	assert.True(t, money.IsZero())
}

func TestMoney_IsZero_NonZeroAmount_ReturnsFalse(t *testing.T) {
	money, _ := NewMoney("GBP", 100)

	assert.False(t, money.IsZero())
}

func TestMoney_Equals_SameAmountAndCurrency_ReturnsTrue(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("GBP", 100)

	assert.True(t, m1.Equals(m2))
}

func TestMoney_Equals_DifferentAmount_ReturnsFalse(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("GBP", 50)

	assert.False(t, m1.Equals(m2))
}

func TestMoney_Equals_DifferentCurrency_ReturnsFalse(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("USD", 100)

	assert.False(t, m1.Equals(m2))
}

// Test value semantics: copying should create independent instance
func TestMoney_ValueSemantics_CopyIsIndependent(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2 := m1 // Copy by value

	addition, _ := NewMoney("GBP", 50)
	m2, _ = m2.Add(addition)

	assert.Equal(t, int64(100), toMinorUnits(m1), "m1 should remain unchanged")
	assert.Equal(t, int64(150), toMinorUnits(m2), "m2 should have new value")
}

// Test that Amount cannot be constructed with invalid state via NewMoney
func TestMoney_CannotConstructDirectly_FieldsUnexported(t *testing.T) {
	money, _ := NewMoney("GBP", 100)

	// Only way to "modify" is through methods that return new instances
	addition, _ := NewMoney("GBP", 50)
	newMoney, _ := money.Add(addition)
	assert.NotEqual(t, money, newMoney, "should be different instances")
}

// Note: The Amount implementation uses decimal.Decimal internally,
// which does not have the same overflow characteristics as int64.
// These tests verify that very large values are handled correctly.

func TestMoney_Add_LargeValues_Success(t *testing.T) {
	// With decimal-based implementation, very large values can be handled
	m1, _ := NewMoney("GBP", 1000000000000) // 10 trillion cents
	m2, _ := NewMoney("GBP", 1000000000000)

	result, err := m1.Add(m2)

	assert.NoError(t, err)
	assert.Equal(t, int64(2000000000000), toMinorUnits(result))
}

func TestMoney_Subtract_LargeNegative_Success(t *testing.T) {
	m1, _ := NewMoney("GBP", 0)
	m2, _ := NewMoney("GBP", 1000000000000)

	result, err := m1.Subtract(m2)

	assert.NoError(t, err)
	assert.Equal(t, int64(-1000000000000), toMinorUnits(result))
}

func TestMoney_Equals_ZeroAmountDifferentCurrency_ReturnsFalse(t *testing.T) {
	// Both have zero amount but different currencies
	gbpZero, _ := NewMoney("GBP", 0)
	usdZero, _ := NewMoney("USD", 0)

	result := gbpZero.Equals(usdZero)

	assert.False(t, result,
		"Zero amounts with different currencies should not be equal")
}

// Test currency validation
func TestNewMoney_SupportedCurrencies(t *testing.T) {
	currencies := []string{"GBP", "USD", "EUR", "JPY", "CHF", "CAD", "AUD"}

	for _, currency := range currencies {
		t.Run(currency, func(t *testing.T) {
			money, err := NewMoney(currency, 100)
			assert.NoError(t, err)
			assert.Equal(t, currency, money.InstrumentCode())
		})
	}
}

func TestNewMoney_UnsupportedCurrencies_ReturnsError(t *testing.T) {
	unsupported := []string{"XYZ", "ABC", "123", "   ", ""}

	for _, currency := range unsupported {
		t.Run(currency, func(t *testing.T) {
			_, err := NewMoney(currency, 100)
			assert.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidCurrency)
		})
	}
}

// Tests for NewAmountFromInstrument - multi-asset support
func TestNewAmountFromInstrument_CURRENCY(t *testing.T) {
	a, err := NewAmountFromInstrument("GBP", "CURRENCY", 2, 10000)

	assert.NoError(t, err)
	assert.Equal(t, "GBP", a.InstrumentCode())
	assert.Equal(t, "CURRENCY", a.Dimension())
	assert.Equal(t, int64(10000), toMinorUnits(a))
}

func TestNewAmountFromInstrument_ENERGY(t *testing.T) {
	a, err := NewAmountFromInstrument("KWH", "ENERGY", 3, 1500)

	assert.NoError(t, err)
	assert.Equal(t, "KWH", a.InstrumentCode())
	assert.Equal(t, "ENERGY", a.Dimension())
	assert.Equal(t, int64(1500), toMinorUnits(a))
}

func TestNewAmountFromInstrument_CARBON(t *testing.T) {
	a, err := NewAmountFromInstrument("CARBON_CREDIT", "CARBON", 0, 100)

	assert.NoError(t, err)
	assert.Equal(t, "CARBON_CREDIT", a.InstrumentCode())
	assert.Equal(t, "CARBON", a.Dimension())
	assert.Equal(t, int64(100), toMinorUnits(a))
}

func TestNewAmountFromInstrument_COMPUTE(t *testing.T) {
	a, err := NewAmountFromInstrument("GPU_HOUR", "COMPUTE", 6, 2000000)

	assert.NoError(t, err)
	assert.Equal(t, "GPU_HOUR", a.InstrumentCode())
	assert.Equal(t, "COMPUTE", a.Dimension())
	assert.Equal(t, int64(2000000), toMinorUnits(a))
}

func TestNewAmountFromInstrument_AllDimensions(t *testing.T) {
	tests := []struct {
		instrument string
		dimension  string
		precision  int
		minorUnits int64
	}{
		{"GBP", "CURRENCY", 2, 10000},
		{"KWH", "ENERGY", 3, 1500},
		{"CARBON_CREDIT", "CARBON", 0, 100},
		{"GPU_HOUR", "COMPUTE", 6, 2000000},
	}

	for _, tc := range tests {
		t.Run(tc.instrument, func(t *testing.T) {
			a, err := NewAmountFromInstrument(tc.instrument, tc.dimension, tc.precision, tc.minorUnits)
			assert.NoError(t, err)
			assert.Equal(t, tc.instrument, a.InstrumentCode())
			assert.Equal(t, tc.dimension, a.Dimension())
			assert.Equal(t, tc.minorUnits, toMinorUnits(a))
		})
	}
}

func TestNewAmountFromInstrument_InvalidDimension_ReturnsError(t *testing.T) {
	_, err := NewAmountFromInstrument("XYZ", "INVALID_DIM", 0, 100)

	assert.Error(t, err)
	// NewAmountFromInstrument returns ErrInvalidDimension from shared/pkg/amount for invalid dimensions
	// (not ErrInstrumentMismatch which is for arithmetic on different instruments)
}

func TestNewMoneyFromInstrument_ValidCurrency(t *testing.T) {
	m, err := NewMoneyFromInstrument("GBP", "CURRENCY", 5000)
	assert.NoError(t, err)
	assert.Equal(t, "GBP", m.InstrumentCode())
	assert.Equal(t, int64(5000), toMinorUnits(m))
}

func TestNewMoneyFromInstrument_NonCurrencyDimension(t *testing.T) {
	_, err := NewMoneyFromInstrument("KWH", "ENERGY", 100)
	assert.ErrorIs(t, err, ErrInvalidCurrency)
}

func TestZeroMoney_ValidCurrency(t *testing.T) {
	m, err := ZeroMoney("GBP")
	assert.NoError(t, err)
	assert.True(t, m.IsZero())
	assert.Equal(t, "GBP", m.InstrumentCode())
}
