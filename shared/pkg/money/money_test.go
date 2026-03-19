package money

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("valid GBP from minor units", func(t *testing.T) {
		m, err := New("GBP", 10000)
		require.NoError(t, err)
		assert.Equal(t, "GBP", m.CurrencyCode())
		assert.True(t, decimal.NewFromInt(100).Equal(m.Amount()))
	})

	t.Run("valid USD from minor units", func(t *testing.T) {
		m, err := New("USD", 4999)
		require.NoError(t, err)
		assert.Equal(t, "USD", m.CurrencyCode())
		assert.True(t, decimal.NewFromFloat(49.99).Equal(m.Amount()))
	})

	t.Run("JPY zero precision", func(t *testing.T) {
		m, err := New("JPY", 1000)
		require.NoError(t, err)
		assert.Equal(t, "JPY", m.CurrencyCode())
		assert.True(t, decimal.NewFromInt(1000).Equal(m.Amount()))
	})

	t.Run("lowercase currency normalised", func(t *testing.T) {
		m, err := New("gbp", 500)
		require.NoError(t, err)
		assert.Equal(t, "GBP", m.CurrencyCode())
	})

	t.Run("invalid currency returns error", func(t *testing.T) {
		_, err := New("INVALID", 100)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidCurrency)
	})

	t.Run("zero amount", func(t *testing.T) {
		m, err := New("GBP", 0)
		require.NoError(t, err)
		assert.True(t, m.IsZero())
	})

	t.Run("negative amount", func(t *testing.T) {
		m, err := New("GBP", -500)
		require.NoError(t, err)
		assert.True(t, m.IsNegative())
		assert.True(t, decimal.NewFromFloat(-5.00).Equal(m.Amount()))
	})
}

func TestNewFromDecimal(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		m, err := NewFromDecimal(decimal.NewFromFloat(99.99), CurrencyGBP)
		require.NoError(t, err)
		assert.Equal(t, CurrencyGBP, m.Currency())
		assert.True(t, decimal.NewFromFloat(99.99).Equal(m.Amount()))
	})

	t.Run("invalid currency", func(t *testing.T) {
		_, err := NewFromDecimal(decimal.NewFromInt(1), Currency("XXX"))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidCurrency)
	})
}

func TestMustNewFromDecimal(t *testing.T) {
	t.Run("valid does not panic", func(t *testing.T) {
		assert.NotPanics(t, func() {
			m := MustNewFromDecimal(decimal.NewFromInt(50), CurrencyUSD)
			assert.Equal(t, CurrencyUSD, m.Currency())
		})
	})

	t.Run("invalid currency panics", func(t *testing.T) {
		assert.Panics(t, func() {
			MustNewFromDecimal(decimal.NewFromInt(1), Currency("NOPE"))
		})
	})
}

func TestNewFromMajorUnits(t *testing.T) {
	m, err := NewFromMajorUnits("EUR", 250)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(250).Equal(m.Amount()))
	assert.Equal(t, "EUR", m.CurrencyCode())
}

func TestNewFromMajorUnits_InvalidCurrency(t *testing.T) {
	_, err := NewFromMajorUnits("FAKE", 1)
	require.ErrorIs(t, err, ErrInvalidCurrency)
}

func TestNewFromQuantity(t *testing.T) {
	m1, _ := New("GBP", 5000)
	m2 := NewFromQuantity(m1.Quantity())
	assert.True(t, m1.Equals(m2))
}

func TestNewFromInstrument(t *testing.T) {
	t.Run("valid CURRENCY dimension", func(t *testing.T) {
		m, err := NewFromInstrument("GBP", "CURRENCY", 10000)
		require.NoError(t, err)
		assert.Equal(t, "GBP", m.CurrencyCode())
		assert.True(t, decimal.NewFromInt(100).Equal(m.Amount()))
	})

	t.Run("lowercase currency dimension accepted", func(t *testing.T) {
		m, err := NewFromInstrument("USD", "currency", 500)
		require.NoError(t, err)
		assert.Equal(t, "USD", m.CurrencyCode())
	})

	t.Run("non-currency dimension rejected", func(t *testing.T) {
		_, err := NewFromInstrument("kWh", "ENERGY", 100)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidCurrency)
	})
}

func TestZero(t *testing.T) {
	m, err := Zero("GBP")
	require.NoError(t, err)
	assert.True(t, m.IsZero())
	assert.Equal(t, "GBP", m.CurrencyCode())
}

func TestZero_InvalidCurrency(t *testing.T) {
	_, err := Zero("NOPE")
	require.ErrorIs(t, err, ErrInvalidCurrency)
}

func TestAccessors(t *testing.T) {
	m, _ := New("GBP", 12345)
	assert.Equal(t, "GBP", m.CurrencyCode())
	assert.Equal(t, CurrencyGBP, m.Currency())
	assert.Equal(t, "GBP", m.Instrument().Code)
	assert.NotNil(t, m.Quantity())
}

func TestAdd(t *testing.T) {
	t.Run("same currency", func(t *testing.T) {
		a, _ := New("GBP", 1000)
		b, _ := New("GBP", 2500)
		result, err := a.Add(b)
		require.NoError(t, err)
		assert.True(t, decimal.NewFromFloat(35.00).Equal(result.Amount()))
	})

	t.Run("different currencies returns error", func(t *testing.T) {
		a, _ := New("GBP", 1000)
		b, _ := New("USD", 2000)
		_, err := a.Add(b)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCurrencyMismatch)
	})
}

func TestSubtract(t *testing.T) {
	t.Run("same currency", func(t *testing.T) {
		a, _ := New("GBP", 5000)
		b, _ := New("GBP", 2000)
		result, err := a.Subtract(b)
		require.NoError(t, err)
		assert.True(t, decimal.NewFromFloat(30.00).Equal(result.Amount()))
	})

	t.Run("result can be negative", func(t *testing.T) {
		a, _ := New("GBP", 1000)
		b, _ := New("GBP", 5000)
		result, err := a.Subtract(b)
		require.NoError(t, err)
		assert.True(t, result.IsNegative())
	})

	t.Run("different currencies returns error", func(t *testing.T) {
		a, _ := New("GBP", 1000)
		b, _ := New("EUR", 1000)
		_, err := a.Subtract(b)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCurrencyMismatch)
	})
}

func TestNegate(t *testing.T) {
	m, _ := New("GBP", 5000)
	neg := m.Negate()
	assert.True(t, neg.IsNegative())
	assert.True(t, decimal.NewFromFloat(-50.00).Equal(neg.Amount()))

	// Double negate returns original
	assert.True(t, neg.Negate().Equals(m))
}

func TestAbs(t *testing.T) {
	neg, _ := New("GBP", -3000)
	pos := neg.Abs()
	assert.True(t, pos.IsPositive())
	assert.True(t, decimal.NewFromFloat(30.00).Equal(pos.Amount()))

	// Abs of positive is unchanged
	pos2, _ := New("GBP", 3000)
	assert.True(t, pos2.Abs().Equals(pos2))
}

func TestMultiply(t *testing.T) {
	m, _ := New("GBP", 1000) // £10.00
	result := m.Multiply(decimal.NewFromFloat(2.5))
	assert.True(t, decimal.NewFromFloat(25.00).Equal(result.Amount()))
}

func TestDivide(t *testing.T) {
	t.Run("valid division", func(t *testing.T) {
		m, _ := New("GBP", 10000) // £100.00
		result, err := m.Divide(decimal.NewFromInt(4))
		require.NoError(t, err)
		assert.True(t, decimal.NewFromFloat(25.00).Equal(result.Amount()))
	})

	t.Run("divide by zero returns error", func(t *testing.T) {
		m, _ := New("GBP", 10000)
		_, err := m.Divide(decimal.Zero)
		require.Error(t, err)
	})
}

func TestPredicates(t *testing.T) {
	zero, _ := New("GBP", 0)
	pos, _ := New("GBP", 100)
	neg, _ := New("GBP", -100)

	assert.True(t, zero.IsZero())
	assert.False(t, zero.IsPositive())
	assert.False(t, zero.IsNegative())

	assert.False(t, pos.IsZero())
	assert.True(t, pos.IsPositive())
	assert.False(t, pos.IsNegative())

	assert.False(t, neg.IsZero())
	assert.False(t, neg.IsPositive())
	assert.True(t, neg.IsNegative())
}

func TestEquals(t *testing.T) {
	a, _ := New("GBP", 5000)
	b, _ := New("GBP", 5000)
	c, _ := New("GBP", 6000)
	d, _ := New("USD", 5000)

	assert.True(t, a.Equals(b))
	assert.False(t, a.Equals(c))
	assert.False(t, a.Equals(d))
}

func TestCompare(t *testing.T) {
	a, _ := New("GBP", 1000)
	b, _ := New("GBP", 2000)
	c, _ := New("GBP", 1000)

	cmp, err := a.Compare(b)
	require.NoError(t, err)
	assert.Equal(t, -1, cmp)

	cmp, err = b.Compare(a)
	require.NoError(t, err)
	assert.Equal(t, 1, cmp)

	cmp, err = a.Compare(c)
	require.NoError(t, err)
	assert.Equal(t, 0, cmp)
}

func TestCompare_DifferentCurrencies(t *testing.T) {
	a, _ := New("GBP", 1000)
	b, _ := New("USD", 1000)
	_, err := a.Compare(b)
	require.Error(t, err)
}

func TestString(t *testing.T) {
	m, _ := New("GBP", 12345)
	s := m.String()
	assert.NotEmpty(t, s)
}

func TestCurrencyString(t *testing.T) {
	c := CurrencyGBP
	assert.Equal(t, "GBP", c.String())
}

func TestToMinorUnits(t *testing.T) {
	t.Run("normal conversion", func(t *testing.T) {
		m, _ := New("GBP", 12345)
		cents, err := m.ToMinorUnits()
		require.NoError(t, err)
		assert.Equal(t, int64(12345), cents)
	})

	t.Run("zero", func(t *testing.T) {
		m, _ := New("GBP", 0)
		cents, err := m.ToMinorUnits()
		require.NoError(t, err)
		assert.Equal(t, int64(0), cents)
	})

	t.Run("negative", func(t *testing.T) {
		m, _ := New("GBP", -500)
		cents, err := m.ToMinorUnits()
		require.NoError(t, err)
		assert.Equal(t, int64(-500), cents)
	})

	t.Run("overflow returns error", func(t *testing.T) {
		// Create a huge amount that would overflow int64 when converted to minor units
		huge := decimal.New(math.MaxInt64, 0)
		m, _ := NewFromDecimal(huge, CurrencyGBP)
		_, err := m.ToMinorUnits()
		require.ErrorIs(t, err, ErrAmountOverflow)
	})
}

func TestToMinorUnitsUnchecked(t *testing.T) {
	m, _ := New("GBP", 9999)
	assert.Equal(t, int64(9999), m.ToMinorUnitsUnchecked())
}

func TestAmountCents(t *testing.T) {
	m, _ := New("USD", 4200)
	assert.Equal(t, int64(4200), m.AmountCents())
}
