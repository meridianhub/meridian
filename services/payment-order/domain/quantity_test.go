package domain

import (
	"testing"

	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMoney(t *testing.T) {
	t.Run("GBP with standard precision", func(t *testing.T) {
		m, err := NewMoney("GBP", 10000)
		require.NoError(t, err)
		assert.Equal(t, "GBP", CurrencyCode(m))
		assert.Equal(t, int64(10000), ToMinorUnits(m))
		assert.True(t, m.Amount.Equal(decimal.NewFromInt(100)))
	})

	t.Run("JPY with zero precision", func(t *testing.T) {
		m, err := NewMoney("JPY", 1000)
		require.NoError(t, err)
		assert.Equal(t, "JPY", CurrencyCode(m))
		assert.Equal(t, int64(1000), ToMinorUnits(m))
		// JPY has precision 0, so 1000 minor units = 1000 major units
		assert.True(t, m.Amount.Equal(decimal.NewFromInt(1000)))
	})

	t.Run("invalid currency", func(t *testing.T) {
		_, err := NewMoney("INVALID", 100)
		assert.ErrorIs(t, err, ErrInvalidCurrency)
	})
}

func TestToMinorUnits(t *testing.T) {
	t.Run("GBP conversion", func(t *testing.T) {
		m := MustNewMoney("GBP", 12345)
		assert.Equal(t, int64(12345), ToMinorUnits(m))
	})

	t.Run("JPY conversion - zero precision", func(t *testing.T) {
		m := MustNewMoney("JPY", 12345)
		assert.Equal(t, int64(12345), ToMinorUnits(m))
	})

	t.Run("sub-cent precision rounds down", func(t *testing.T) {
		// Create money with sub-cent precision (e.g., after division)
		// £100.00 / 3 = £33.333...
		m := MustNewMoney("GBP", 10000)
		divided := NewMoneyDecimal(m.Amount.Div(decimal.NewFromInt(3)), InstrumentGBP)
		// 33.333... GBP = 3333.33... cents, truncated to 3333
		assert.Equal(t, int64(3333), ToMinorUnits(divided))
	})

	t.Run("panics on zero-value Money", func(t *testing.T) {
		assert.PanicsWithValue(t, "ToMinorUnits: called on zero-value Money with no instrument", func() {
			ToMinorUnits(Money{})
		})
	})
}

func TestCurrencyCode(t *testing.T) {
	t.Run("returns correct code", func(t *testing.T) {
		gbp := MustNewMoney("GBP", 100)
		assert.Equal(t, "GBP", CurrencyCode(gbp))

		usd := MustNewMoney("USD", 100)
		assert.Equal(t, "USD", CurrencyCode(usd))

		jpy := MustNewMoney("JPY", 100)
		assert.Equal(t, "JPY", CurrencyCode(jpy))
	})

	t.Run("panics on zero-value Money", func(t *testing.T) {
		assert.PanicsWithValue(t, "CurrencyCode: called on zero-value Money with no instrument", func() {
			CurrencyCode(Money{})
		})
	})
}

func TestMustNewMoney(t *testing.T) {
	t.Run("succeeds with valid currency", func(t *testing.T) {
		m := MustNewMoney("GBP", 100)
		assert.Equal(t, "GBP", CurrencyCode(m))
	})

	t.Run("panics with invalid currency", func(t *testing.T) {
		assert.Panics(t, func() {
			MustNewMoney("INVALID", 100)
		})
	})
}

func TestNewMoneyDecimal(t *testing.T) {
	t.Run("creates money from decimal", func(t *testing.T) {
		m := NewMoneyDecimal(decimal.NewFromFloat(123.45), InstrumentGBP)
		assert.Equal(t, "GBP", CurrencyCode(m))
		assert.Equal(t, int64(12345), ToMinorUnits(m))
	})

	t.Run("JPY with decimal", func(t *testing.T) {
		m := NewMoneyDecimal(decimal.NewFromInt(1000), InstrumentJPY)
		assert.Equal(t, "JPY", CurrencyCode(m))
		assert.Equal(t, int64(1000), ToMinorUnits(m))
	})
}

func TestValidateCurrencyDimension(t *testing.T) {
	tests := []struct {
		name      string
		inst      Instrument
		wantErr   bool
		errTarget error
	}{
		{
			name:    "CURRENCY dimension accepted",
			inst:    quantity.Instrument{Code: "GBP", Dimension: quantity.DimensionCurrency, Precision: 2},
			wantErr: false,
		},
		{
			name:      "ENERGY dimension rejected",
			inst:      quantity.Instrument{Code: "KWH", Dimension: "ENERGY", Precision: 3},
			wantErr:   true,
			errTarget: ErrNonCurrencyInstrument,
		},
		{
			name:      "COMPUTE dimension rejected",
			inst:      quantity.Instrument{Code: "GPU_HOUR", Dimension: "COMPUTE", Precision: 6},
			wantErr:   true,
			errTarget: ErrNonCurrencyInstrument,
		},
		{
			name:      "empty dimension rejected",
			inst:      quantity.Instrument{Code: "XXX", Dimension: "", Precision: 2},
			wantErr:   true,
			errTarget: ErrNonCurrencyInstrument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCurrencyDimension(tt.inst)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.errTarget)
				assert.Contains(t, err.Error(), tt.inst.Code)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMoneyIsPositive(t *testing.T) {
	t.Run("positive amount", func(t *testing.T) {
		m := MustNewMoney("GBP", 100)
		assert.True(t, m.IsPositive())
	})

	t.Run("zero amount", func(t *testing.T) {
		m := MustNewMoney("GBP", 0)
		assert.False(t, m.IsPositive())
	})

	t.Run("negative amount", func(t *testing.T) {
		m := MustNewMoney("GBP", -100)
		assert.False(t, m.IsPositive())
	})
}
