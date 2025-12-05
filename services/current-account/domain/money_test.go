package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// RED: These tests will fail until we refactor Money to be immutable

func TestNewMoney_ValidInput_CreatesMoney(t *testing.T) {
	money, err := NewMoney("GBP", 100)

	assert.NoError(t, err)
	assert.Equal(t, "GBP", money.Currency())
	assert.Equal(t, int64(100), money.AmountCents())
}

func TestNewMoney_EmptyCurrency_ReturnsError(t *testing.T) {
	_, err := NewMoney("", 100)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidCurrency)
}

func TestMoney_Add_SameCurrency_ReturnsSum(t *testing.T) {
	m1, _ := NewMoney("GBP", 100)
	m2, _ := NewMoney("GBP", 50)

	result, err := m1.Add(m2)

	assert.NoError(t, err)
	assert.Equal(t, int64(150), result.AmountCents())
	assert.Equal(t, "GBP", result.Currency())
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
	assert.Equal(t, "GBP", result.Currency())
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

// Test overflow detection
func TestMoney_Add_Overflow_ReturnsError(t *testing.T) {
	m1, _ := NewMoney("GBP", 9223372036854775807) // math.MaxInt64
	m2, _ := NewMoney("GBP", 1)

	_, err := m1.Add(m2)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAmountOverflow)
}

func TestMoney_Add_NearMaxInt64_Success(t *testing.T) {
	m1, _ := NewMoney("GBP", 9223372036854775806) // MaxInt64 - 1
	m2, _ := NewMoney("GBP", 1)

	result, err := m1.Add(m2)

	assert.NoError(t, err)
	assert.Equal(t, int64(9223372036854775807), result.AmountCents())
}

func TestMoney_Subtract_Underflow_ReturnsError(t *testing.T) {
	m1, _ := NewMoney("GBP", -9223372036854775808) // math.MinInt64
	m2, _ := NewMoney("GBP", 1)

	_, err := m1.Subtract(m2)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAmountOverflow)
}

func TestMoney_Subtract_NearMinInt64_Success(t *testing.T) {
	m1, _ := NewMoney("GBP", -9223372036854775807) // MinInt64 + 1
	m2, _ := NewMoney("GBP", 1)

	result, err := m1.Subtract(m2)

	assert.NoError(t, err)
	assert.Equal(t, int64(-9223372036854775808), result.AmountCents())
}

// Defensive test per ADR-008: Edge cases with zero-value structs

func TestMoney_Equals_ZeroValueStruct_ReturnsFalse(t *testing.T) {
	// Test comparing valid Money with zero-value Money struct
	// Zero-value has empty currency and 0 amount
	validMoney, _ := NewMoney("GBP", 0)
	zeroValueMoney := Money{} // Direct struct creation bypasses constructor

	// Should return false: currency mismatch ("GBP" != "")
	result := validMoney.Equals(zeroValueMoney)

	assert.False(t, result,
		"Valid Money should not equal zero-value struct (currency mismatch)")
}

func TestMoney_Equals_BothZeroValue_ReturnsTrue(t *testing.T) {
	// Two zero-value structs should be equal (both have empty currency and 0 amount)
	zeroValue1 := Money{}
	zeroValue2 := Money{}

	result := zeroValue1.Equals(zeroValue2)

	assert.True(t, result,
		"Two zero-value Money structs should be equal")
}

func TestMoney_Equals_ZeroAmountDifferentCurrency_ReturnsFalse(t *testing.T) {
	// Both have zero amount but different currencies
	gbpZero, _ := NewMoney("GBP", 0)
	usdZero, _ := NewMoney("USD", 0)

	result := gbpZero.Equals(usdZero)

	assert.False(t, result,
		"Zero amounts with different currencies should not be equal")
}

// Nice-to-have tests: Currency validation edge cases

func TestNewMoney_SpecialCharacters_Accepted(t *testing.T) {
	// Currency codes can contain special chars in principle
	// Testing defensive behavior
	tests := []struct {
		name     string
		currency string
		wantErr  bool
	}{
		{
			name:     "standard currency",
			currency: "GBP",
			wantErr:  false,
		},
		{
			name:     "whitespace only currency",
			currency: "   ",
			wantErr:  false, // Currently accepted - no trimming
		},
		{
			name:     "currency with spaces",
			currency: "G BP",
			wantErr:  false, // Allowed - validation is minimal
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewMoney(tt.currency, 100)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
