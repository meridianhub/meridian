package currency_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/pkg/platform/quantity"
	"github.com/meridianhub/meridian/pkg/platform/quantity/currency"
)

func TestInstrumentConstants(t *testing.T) {
	tests := []struct {
		name       string
		instrument quantity.Instrument
		code       string
		precision  int
	}{
		{"USD", currency.InstrumentUSD, "USD", 2},
		{"EUR", currency.InstrumentEUR, "EUR", 2},
		{"GBP", currency.InstrumentGBP, "GBP", 2},
		{"JPY", currency.InstrumentJPY, "JPY", 0},
		{"CHF", currency.InstrumentCHF, "CHF", 2},
		{"AUD", currency.InstrumentAUD, "AUD", 2},
		{"CAD", currency.InstrumentCAD, "CAD", 2},
		{"NZD", currency.InstrumentNZD, "NZD", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.code, tt.instrument.Code)
			assert.Equal(t, tt.precision, tt.instrument.Precision)
			assert.Equal(t, quantity.DimensionCurrency, tt.instrument.Dimension)
			assert.Equal(t, uint32(0), tt.instrument.Version)
			assert.True(t, tt.instrument.IsMonetary())
			assert.NoError(t, tt.instrument.Validate())
		})
	}
}

func TestByCode(t *testing.T) {
	tests := []struct {
		code     string
		wantOk   bool
		wantCode string
		wantPrec int
	}{
		{"USD", true, "USD", 2},
		{"EUR", true, "EUR", 2},
		{"GBP", true, "GBP", 2},
		{"JPY", true, "JPY", 0},
		{"CHF", true, "CHF", 2},
		{"AUD", true, "AUD", 2},
		{"CAD", true, "CAD", 2},
		{"NZD", true, "NZD", 2},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			inst, ok := currency.ByCode(tt.code)
			assert.Equal(t, tt.wantOk, ok)
			if tt.wantOk {
				assert.Equal(t, tt.wantCode, inst.Code)
				assert.Equal(t, tt.wantPrec, inst.Precision)
			}
		})
	}
}

func TestByCode_UnknownCurrency(t *testing.T) {
	unknownCodes := []string{
		"XYZ",   // completely invalid
		"usd",   // lowercase
		"Usd",   // mixed case
		"",      // empty
		"USDZZ", // too long
		"US",    // too short
		"CNY",   // not in our list
		"INR",   // not in our list
	}

	for _, code := range unknownCodes {
		t.Run(code, func(t *testing.T) {
			inst, ok := currency.ByCode(code)
			assert.False(t, ok)
			assert.Equal(t, quantity.Instrument{}, inst)
		})
	}
}

func TestConstructorHelpers(t *testing.T) {
	amount := decimal.NewFromInt(100)

	tests := []struct {
		name     string
		money    quantity.Money
		wantCode string
		wantPrec int
	}{
		{"USD", currency.USD(amount), "USD", 2},
		{"EUR", currency.EUR(amount), "EUR", 2},
		{"GBP", currency.GBP(amount), "GBP", 2},
		{"JPY", currency.JPY(amount), "JPY", 0},
		{"CHF", currency.CHF(amount), "CHF", 2},
		{"AUD", currency.AUD(amount), "AUD", 2},
		{"CAD", currency.CAD(amount), "CAD", 2},
		{"NZD", currency.NZD(amount), "NZD", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.money.Amount.Equal(amount))
			assert.Equal(t, tt.wantCode, tt.money.Instrument.Code)
			assert.Equal(t, tt.wantPrec, tt.money.Instrument.Precision)
		})
	}
}

func TestConstructorHelpers_WithDecimalValues(t *testing.T) {
	t.Run("USD with cents", func(t *testing.T) {
		amount := decimal.NewFromFloat(99.99)
		money := currency.USD(amount)
		assert.True(t, money.Amount.Equal(amount))
		assert.Equal(t, "99.99 USD", money.String())
	})

	t.Run("JPY rounds to whole number", func(t *testing.T) {
		amount := decimal.NewFromInt(10000)
		money := currency.JPY(amount)
		assert.True(t, money.Amount.Equal(amount))
		assert.Equal(t, "10000 JPY", money.String())
	})
}

func TestMoneyArithmetic(t *testing.T) {
	usd100 := currency.USD(decimal.NewFromInt(100))
	usd50 := currency.USD(decimal.NewFromInt(50))

	t.Run("add same currency", func(t *testing.T) {
		result, err := usd100.Add(usd50)
		require.NoError(t, err)
		assert.True(t, result.Amount.Equal(decimal.NewFromInt(150)))
		assert.Equal(t, "USD", result.Instrument.Code)
	})

	t.Run("subtract same currency", func(t *testing.T) {
		result, err := usd100.Subtract(usd50)
		require.NoError(t, err)
		assert.True(t, result.Amount.Equal(decimal.NewFromInt(50)))
	})

	t.Run("cannot add different currencies", func(t *testing.T) {
		eur100 := currency.EUR(decimal.NewFromInt(100))
		_, err := usd100.Add(eur100)
		require.Error(t, err)
		assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)
	})
}

func TestJPYPrecision(t *testing.T) {
	// JPY has 0 decimal places
	jpy := currency.JPY(decimal.NewFromFloat(12345.67))

	// Multiply and check rounding
	result := jpy.Multiply(decimal.NewFromFloat(1.5))
	// 12345.67 * 1.5 = 18518.505, rounded to 18519 (banker's rounding)
	assert.True(t, result.Amount.Equal(decimal.NewFromInt(18519)))
}

func TestAll(t *testing.T) {
	all := currency.All()
	assert.Len(t, all, 8) // USD, EUR, GBP, JPY, CHF, AUD, CAD, NZD

	// Verify all are valid
	for _, inst := range all {
		assert.NoError(t, inst.Validate())
		assert.Equal(t, quantity.DimensionCurrency, inst.Dimension)
	}
}

func TestCodes(t *testing.T) {
	codes := currency.Codes()
	assert.Len(t, codes, 8)

	// All codes should be valid lookups
	for _, code := range codes {
		_, ok := currency.ByCode(code)
		assert.True(t, ok, "code %s should be valid", code)
	}
}
