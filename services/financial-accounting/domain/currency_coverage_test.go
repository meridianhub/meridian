package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrencyToInstrument_ValidCurrencies(t *testing.T) {
	tests := []struct {
		name     string
		currency Currency
		wantCode string
	}{
		{"GBP", CurrencyGBP, "GBP"},
		{"USD", CurrencyUSD, "USD"},
		{"EUR", CurrencyEUR, "EUR"},
		{"JPY", CurrencyJPY, "JPY"},
		{"CHF", CurrencyCHF, "CHF"},
		{"CAD", CurrencyCAD, "CAD"},
		{"AUD", CurrencyAUD, "AUD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst, err := CurrencyToInstrument(tt.currency)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCode, inst.Code)
		})
	}
}

func TestCurrencyToInstrument_InvalidCurrency(t *testing.T) {
	_, err := CurrencyToInstrument(Currency("XXX"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDimension)
}

func TestCurrencyToInstrument_EmptyCurrency(t *testing.T) {
	_, err := CurrencyToInstrument(Currency(""))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDimension)
}

func TestMustCurrencyToInstrument_ValidCurrency(t *testing.T) {
	inst := MustCurrencyToInstrument(CurrencyGBP)
	assert.Equal(t, "GBP", inst.Code)
}

func TestMustCurrencyToInstrument_InvalidCurrency_Panics(t *testing.T) {
	assert.Panics(t, func() {
		MustCurrencyToInstrument(Currency("INVALID"))
	})
}
