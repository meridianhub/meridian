package domain

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMoneyFromInstrumentCode_Currency(t *testing.T) {
	tests := []struct {
		name    string
		amount  decimal.Decimal
		code    string
		wantErr bool
	}{
		{
			name:   "valid GBP",
			amount: decimal.NewFromFloat(100.50),
			code:   "GBP",
		},
		{
			name:   "valid USD",
			amount: decimal.NewFromFloat(250.00),
			code:   "USD",
		},
		{
			name:   "valid EUR",
			amount: decimal.NewFromFloat(75.25),
			code:   "EUR",
		},
		{
			name:   "zero amount GBP",
			amount: decimal.Zero,
			code:   "GBP",
		},
		{
			name:   "negative amount",
			amount: decimal.NewFromFloat(-50.00),
			code:   "GBP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewMoneyFromInstrumentCode(tt.amount, tt.code)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.amount, m.Amount)
			assert.Equal(t, tt.code, m.Instrument.Code)
		})
	}
}

func TestNewMoneyFromInstrumentCode_NonCurrency(t *testing.T) {
	amount := decimal.NewFromFloat(42.50)
	m, err := NewMoneyFromInstrumentCode(amount, "KWH")
	require.NoError(t, err)
	assert.Equal(t, amount, m.Amount)
	assert.Equal(t, "KWH", m.Instrument.Code)
}

func TestNewMoneyFromInstrumentCode_EmptyCode(t *testing.T) {
	_, err := NewMoneyFromInstrumentCode(decimal.NewFromInt(100), "")
	assert.ErrorIs(t, err, ErrEmptyCode)
}

func TestMoneyCurrency(t *testing.T) {
	m, err := NewMoney(decimal.NewFromFloat(100), CurrencyGBP)
	require.NoError(t, err)

	cur := MoneyCurrency(m)
	assert.Equal(t, CurrencyGBP, cur)
}

func TestMoneyToMinorUnits(t *testing.T) {
	tests := []struct {
		name      string
		amount    decimal.Decimal
		currency  Currency
		wantMinor int64
		wantErr   bool
	}{
		{
			name:      "GBP 100.50",
			amount:    decimal.NewFromFloat(100.50),
			currency:  CurrencyGBP,
			wantMinor: 10050,
		},
		{
			name:      "USD 0.01",
			amount:    decimal.NewFromFloat(0.01),
			currency:  CurrencyUSD,
			wantMinor: 1,
		},
		{
			name:      "zero amount",
			amount:    decimal.Zero,
			currency:  CurrencyEUR,
			wantMinor: 0,
		},
		{
			name:      "negative amount",
			amount:    decimal.NewFromFloat(-50.25),
			currency:  CurrencyGBP,
			wantMinor: -5025,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewMoney(tt.amount, tt.currency)
			require.NoError(t, err)

			minorUnits, err := MoneyToMinorUnits(m)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMinor, minorUnits)
		})
	}
}

func TestMoneyToMinorUnitsUnchecked(t *testing.T) {
	m, err := NewMoney(decimal.NewFromFloat(99.99), CurrencyGBP)
	require.NoError(t, err)

	minor := MoneyToMinorUnitsUnchecked(m)
	assert.Equal(t, int64(9999), minor)
}

func TestNewMoneyFromMinorUnits(t *testing.T) {
	tests := []struct {
		name       string
		code       string
		minorUnits int64
		wantAmount decimal.Decimal
		wantErr    bool
	}{
		{
			name:       "GBP 10000 minor = 100.00",
			code:       "GBP",
			minorUnits: 10000,
			wantAmount: decimal.NewFromFloat(100.00),
		},
		{
			name:       "USD 1 minor = 0.01",
			code:       "USD",
			minorUnits: 1,
			wantAmount: decimal.NewFromFloat(0.01),
		},
		{
			name:       "zero minor units",
			code:       "EUR",
			minorUnits: 0,
			wantAmount: decimal.Zero,
		},
		{
			name:    "invalid currency",
			code:    "INVALID",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewMoneyFromMinorUnits(tt.code, tt.minorUnits)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.True(t, tt.wantAmount.Equal(m.Amount),
				"expected %s, got %s", tt.wantAmount, m.Amount)
		})
	}
}

func TestNewInstrument(t *testing.T) {
	t.Run("valid currency instrument", func(t *testing.T) {
		inst, err := NewInstrument("GBP", 1, DimensionCurrency, 2)
		require.NoError(t, err)
		assert.Equal(t, "GBP", inst.Code)
		assert.Equal(t, 2, inst.Precision)
	})

	t.Run("empty code returns error", func(t *testing.T) {
		_, err := NewInstrument("", 1, DimensionCurrency, 2)
		assert.ErrorIs(t, err, ErrEmptyCode)
	})

	t.Run("negative precision returns error", func(t *testing.T) {
		_, err := NewInstrument("GBP", 1, DimensionCurrency, -1)
		assert.ErrorIs(t, err, ErrNegativePrecision)
	})
}

func TestMustNewInstrument_Panics(t *testing.T) {
	assert.Panics(t, func() {
		MustNewInstrument("", 1, DimensionCurrency, 2)
	})
}

func TestMustNewInstrument_Valid(t *testing.T) {
	assert.NotPanics(t, func() {
		inst := MustNewInstrument("GBP", 1, DimensionCurrency, 2)
		assert.Equal(t, "GBP", inst.Code)
	})
}

func TestNewAsset(t *testing.T) {
	inst := MustNewInstrument("KWH", 1, "ENERGY", 3)
	amount := decimal.NewFromFloat(123.456)

	a := NewAsset(amount, inst)
	assert.True(t, amount.Equal(a.Amount))
	assert.Equal(t, "KWH", a.Instrument.Code)
}

func TestNewAssetFromString(t *testing.T) {
	inst := MustNewInstrument("KWH", 1, "ENERGY", 3)

	t.Run("valid string", func(t *testing.T) {
		a, err := NewAssetFromString("42.500", inst)
		require.NoError(t, err)
		assert.True(t, decimal.NewFromFloat(42.5).Equal(a.Amount))
	})

	t.Run("invalid string", func(t *testing.T) {
		_, err := NewAssetFromString("not-a-number", inst)
		assert.ErrorIs(t, err, ErrInvalidDecimalString)
	})
}

func TestZeroFunctions(t *testing.T) {
	t.Run("Zero money", func(t *testing.T) {
		m, err := Zero(CurrencyGBP)
		require.NoError(t, err)
		assert.True(t, m.Amount.IsZero())
		assert.Equal(t, "GBP", m.Instrument.Code)
	})

	t.Run("ZeroAsset", func(t *testing.T) {
		inst := MustNewInstrument("KWH", 1, "ENERGY", 3)
		a := ZeroAsset(inst)
		assert.True(t, a.Amount.IsZero())
	})
}

func TestParseCurrency(t *testing.T) {
	t.Run("valid currency", func(t *testing.T) {
		cur, err := ParseCurrency("GBP")
		require.NoError(t, err)
		assert.Equal(t, CurrencyGBP, cur)
	})

	t.Run("invalid currency", func(t *testing.T) {
		_, err := ParseCurrency("INVALID")
		require.Error(t, err)
	})

	t.Run("empty string", func(t *testing.T) {
		_, err := ParseCurrency("")
		require.Error(t, err)
	})
}

func TestNewAmount(t *testing.T) {
	inst := MustNewInstrument("KWH", 1, "ENERGY", 3)
	a := NewAmount(inst, 1500)
	assert.Equal(t, "KWH", a.InstrumentCode())
	minorUnits, err := a.ToMinorUnits()
	require.NoError(t, err)
	assert.Equal(t, int64(1500), minorUnits)
}

func TestNewAmountFromDecimal(t *testing.T) {
	inst := MustNewInstrument("GBP", 1, DimensionCurrency, 2)
	a := NewAmountFromDecimal(inst, decimal.NewFromFloat(10.00))
	assert.Equal(t, "GBP", a.InstrumentCode())
	minorUnits, err := a.ToMinorUnits()
	require.NoError(t, err)
	assert.Equal(t, int64(1000), minorUnits)
}

func TestZeroAmount(t *testing.T) {
	inst := MustNewInstrument("EUR", 1, DimensionCurrency, 2)
	a := ZeroAmount(inst)
	assert.True(t, a.IsZero())
}
