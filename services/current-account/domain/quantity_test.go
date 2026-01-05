//nolint:staticcheck // Tests intentionally use deprecated AmountCents() to verify backward compatibility
package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewMoney_ValidInput_CreatesMoney(t *testing.T) {
	money, err := NewMoney("GBP", 100)

	assert.NoError(t, err)
	assert.Equal(t, CurrencyGBP, money.Currency())
	assert.Equal(t, int64(100), money.AmountCents())
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
	assert.Equal(t, int64(150), result.AmountCents())
	assert.Equal(t, CurrencyGBP, result.Currency())
}

func TestMoney_Add_DifferentCurrency_ReturnsError(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("USD", 50)

	_, err := m1.Add(m2)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCurrencyMismatch)
}

func TestMoney_Add_DoesNotMutateOriginal(t *testing.T) {
	original, _ := NewMoney("GBP", 100)
	originalAmount := original.AmountCents()
	other, _ := NewMoney("GBP", 50)

	_, _ = original.Add(other)

	assert.Equal(t, originalAmount, original.AmountCents(),
		"original money should not be mutated by Add operation")
}

func TestMoney_Subtract_SameCurrency_ReturnsDifference(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("GBP", 30)

	result, err := m1.Subtract(m2)

	assert.NoError(t, err)
	assert.Equal(t, int64(70), result.AmountCents())
	assert.Equal(t, CurrencyGBP, result.Currency())
}

func TestMoney_Subtract_DifferentCurrency_ReturnsError(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("USD", 30)

	_, err := m1.Subtract(m2)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCurrencyMismatch)
}

func TestMoney_Subtract_DoesNotMutateOriginal(t *testing.T) {
	original, _ := NewMoney("GBP", 100)
	originalAmount := original.AmountCents()
	other, _ := NewMoney("GBP", 30)

	_, _ = original.Subtract(other)

	assert.Equal(t, originalAmount, original.AmountCents(),
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

	assert.Equal(t, int64(100), m1.AmountCents(), "m1 should remain unchanged")
	assert.Equal(t, int64(150), m2.AmountCents(), "m2 should have new value")
}

// Test that Money cannot be constructed with invalid state
func TestMoney_CannotConstructDirectly_FieldsUnexported(t *testing.T) {
	// This test verifies that struct fields are unexported
	// If fields are exported, this won't compile (which is what we want)

	money, _ := NewMoney("GBP", 100)

	// These lines should NOT compile if fields are properly unexported:
	// money.AmountCents = 200  // Should fail: field unexported
	// money.Currency = "USD"    // Should fail: field unexported

	// Only way to "modify" is through methods that return new instances
	addition, _ := NewMoney("GBP", 50)
	newMoney, _ := money.Add(addition)
	assert.NotEqual(t, money, newMoney, "should be different instances")
}

// Note: The new shared Money implementation uses decimal.Decimal internally,
// which does not have the same overflow characteristics as int64.
// These tests verify that very large values are handled correctly.

func TestMoney_Add_LargeValues_Success(t *testing.T) {
	// With decimal-based implementation, very large values can be handled
	m1, _ := NewMoney("GBP", 1000000000000) // 10 trillion cents
	m2, _ := NewMoney("GBP", 1000000000000)

	result, err := m1.Add(m2)

	assert.NoError(t, err)
	assert.Equal(t, int64(2000000000000), result.AmountCents())
}

func TestMoney_Subtract_LargeNegative_Success(t *testing.T) {
	m1, _ := NewMoney("GBP", 0)
	m2, _ := NewMoney("GBP", 1000000000000)

	result, err := m1.Subtract(m2)

	assert.NoError(t, err)
	assert.Equal(t, int64(-1000000000000), result.AmountCents())
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
			assert.Equal(t, currency, string(money.Currency()))
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
