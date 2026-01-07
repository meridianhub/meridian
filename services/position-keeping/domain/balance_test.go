package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBalanceType_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		bt       BalanceType
		expected bool
	}{
		// Valid balance types
		{name: "OPENING is valid", bt: BalanceTypeOpening, expected: true},
		{name: "CLOSING is valid", bt: BalanceTypeClosing, expected: true},
		{name: "CURRENT is valid", bt: BalanceTypeCurrent, expected: true},
		{name: "AVAILABLE is valid", bt: BalanceTypeAvailable, expected: true},
		{name: "LEDGER is valid", bt: BalanceTypeLedger, expected: true},
		{name: "RESERVE is valid", bt: BalanceTypeReserve, expected: true},
		{name: "FREE is valid", bt: BalanceTypeFree, expected: true},

		// Invalid balance types
		{name: "UNKNOWN is invalid", bt: BalanceTypeUnknown, expected: false},
		{name: "empty string is invalid", bt: BalanceType(""), expected: false},
		{name: "lowercase opening is invalid", bt: BalanceType("opening"), expected: false},
		{name: "arbitrary string is invalid", bt: BalanceType("INVALID"), expected: false},
		{name: "mixed case is invalid", bt: BalanceType("Opening"), expected: false},
		{name: "numeric string is invalid", bt: BalanceType("123"), expected: false},
		{name: "whitespace is invalid", bt: BalanceType(" "), expected: false},
		{name: "OPENING with space is invalid", bt: BalanceType("OPENING "), expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.bt.IsValid()
			assert.Equal(t, tt.expected, result, "IsValid() for %q", tt.bt)
		})
	}
}

func TestBalanceType_String(t *testing.T) {
	tests := []struct {
		name     string
		bt       BalanceType
		expected string
	}{
		{name: "OPENING string", bt: BalanceTypeOpening, expected: "OPENING"},
		{name: "CLOSING string", bt: BalanceTypeClosing, expected: "CLOSING"},
		{name: "CURRENT string", bt: BalanceTypeCurrent, expected: "CURRENT"},
		{name: "AVAILABLE string", bt: BalanceTypeAvailable, expected: "AVAILABLE"},
		{name: "LEDGER string", bt: BalanceTypeLedger, expected: "LEDGER"},
		{name: "RESERVE string", bt: BalanceTypeReserve, expected: "RESERVE"},
		{name: "FREE string", bt: BalanceTypeFree, expected: "FREE"},
		{name: "UNKNOWN string", bt: BalanceTypeUnknown, expected: "UNKNOWN"},
		{name: "arbitrary value returns itself", bt: BalanceType("CUSTOM"), expected: "CUSTOM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.bt.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseBalanceType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected BalanceType
	}{
		// Valid parsing
		{name: "parse OPENING", input: "OPENING", expected: BalanceTypeOpening},
		{name: "parse CLOSING", input: "CLOSING", expected: BalanceTypeClosing},
		{name: "parse CURRENT", input: "CURRENT", expected: BalanceTypeCurrent},
		{name: "parse AVAILABLE", input: "AVAILABLE", expected: BalanceTypeAvailable},
		{name: "parse LEDGER", input: "LEDGER", expected: BalanceTypeLedger},
		{name: "parse RESERVE", input: "RESERVE", expected: BalanceTypeReserve},
		{name: "parse FREE", input: "FREE", expected: BalanceTypeFree},

		// Invalid parsing returns UNKNOWN
		{name: "empty string returns UNKNOWN", input: "", expected: BalanceTypeUnknown},
		{name: "lowercase returns UNKNOWN", input: "opening", expected: BalanceTypeUnknown},
		{name: "mixed case returns UNKNOWN", input: "Opening", expected: BalanceTypeUnknown},
		{name: "invalid string returns UNKNOWN", input: "INVALID", expected: BalanceTypeUnknown},
		{name: "UNKNOWN string returns UNKNOWN", input: "UNKNOWN", expected: BalanceTypeUnknown},
		{name: "whitespace returns UNKNOWN", input: " ", expected: BalanceTypeUnknown},
		{name: "OPENING with space returns UNKNOWN", input: "OPENING ", expected: BalanceTypeUnknown},
		{name: "numeric returns UNKNOWN", input: "123", expected: BalanceTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseBalanceType(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBalance_Creation(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name       string
		balType    BalanceType
		amount     decimal.Decimal
		currency   Currency
		assertFunc func(t *testing.T, b Balance)
	}{
		{
			name:     "create balance with positive amount",
			balType:  BalanceTypeCurrent,
			amount:   decimal.NewFromInt(1000),
			currency: CurrencyGBP,
			assertFunc: func(t *testing.T, b Balance) {
				assert.Equal(t, BalanceTypeCurrent, b.Type)
				assert.True(t, b.Amount.Amount.Equal(decimal.NewFromInt(1000)))
				assert.Equal(t, "GBP", b.Amount.Instrument.Code)
			},
		},
		{
			name:     "create balance with zero amount",
			balType:  BalanceTypeOpening,
			amount:   decimal.Zero,
			currency: CurrencyUSD,
			assertFunc: func(t *testing.T, b Balance) {
				assert.Equal(t, BalanceTypeOpening, b.Type)
				assert.True(t, b.Amount.Amount.IsZero())
				assert.Equal(t, "USD", b.Amount.Instrument.Code)
			},
		},
		{
			name:     "create balance with negative amount (overdraft scenario)",
			balType:  BalanceTypeAvailable,
			amount:   decimal.NewFromInt(-500),
			currency: CurrencyEUR,
			assertFunc: func(t *testing.T, b Balance) {
				assert.Equal(t, BalanceTypeAvailable, b.Type)
				assert.True(t, b.Amount.Amount.Equal(decimal.NewFromInt(-500)))
				assert.True(t, b.Amount.Amount.IsNegative())
			},
		},
		{
			name:     "create balance with decimal amount",
			balType:  BalanceTypeLedger,
			amount:   decimal.NewFromFloat(1234.56),
			currency: CurrencyGBP,
			assertFunc: func(t *testing.T, b Balance) {
				assert.Equal(t, BalanceTypeLedger, b.Type)
				expected, _ := decimal.NewFromString("1234.56")
				assert.True(t, b.Amount.Amount.Equal(expected))
			},
		},
		{
			name:     "create balance with very large amount",
			balType:  BalanceTypeClosing,
			amount:   decimal.NewFromInt(999999999999),
			currency: CurrencyGBP,
			assertFunc: func(t *testing.T, b Balance) {
				assert.Equal(t, BalanceTypeClosing, b.Type)
				assert.True(t, b.Amount.Amount.Equal(decimal.NewFromInt(999999999999)))
			},
		},
		{
			name:     "create balance with JPY (zero decimal places)",
			balType:  BalanceTypeReserve,
			amount:   decimal.NewFromInt(10000),
			currency: CurrencyJPY,
			assertFunc: func(t *testing.T, b Balance) {
				assert.Equal(t, BalanceTypeReserve, b.Type)
				assert.True(t, b.Amount.Amount.Equal(decimal.NewFromInt(10000)))
				assert.Equal(t, "JPY", b.Amount.Instrument.Code)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money := MustNewMoney(tt.amount, tt.currency)
			balance := Balance{
				Type:   tt.balType,
				Amount: money,
				AsOf:   now,
			}

			tt.assertFunc(t, balance)
			assert.Equal(t, now, balance.AsOf)
		})
	}
}

func TestBalance_AllBalanceTypes(t *testing.T) {
	// Verify all 7 valid balance types can be used to create a Balance
	validTypes := []BalanceType{
		BalanceTypeOpening,
		BalanceTypeClosing,
		BalanceTypeCurrent,
		BalanceTypeAvailable,
		BalanceTypeLedger,
		BalanceTypeReserve,
		BalanceTypeFree,
	}

	now := time.Now().UTC()
	amount := MustNewMoney(decimal.NewFromInt(100), CurrencyGBP)

	for _, bt := range validTypes {
		t.Run(string(bt), func(t *testing.T) {
			balance := Balance{
				Type:   bt,
				Amount: amount,
				AsOf:   now,
			}

			require.True(t, balance.Type.IsValid(), "Balance type %s should be valid", bt)
			assert.Equal(t, bt, balance.Type)
			assert.Equal(t, amount, balance.Amount)
			assert.Equal(t, now, balance.AsOf)
		})
	}
}

func TestBalance_TimeStamp(t *testing.T) {
	amount := MustNewMoney(decimal.NewFromInt(100), CurrencyGBP)

	t.Run("balance with specific timestamp", func(t *testing.T) {
		specificTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		balance := Balance{
			Type:   BalanceTypeCurrent,
			Amount: amount,
			AsOf:   specificTime,
		}

		assert.Equal(t, specificTime, balance.AsOf)
	})

	t.Run("balance with zero time", func(t *testing.T) {
		balance := Balance{
			Type:   BalanceTypeCurrent,
			Amount: amount,
			AsOf:   time.Time{},
		}

		assert.True(t, balance.AsOf.IsZero())
	})

	t.Run("balance preserves timezone", func(t *testing.T) {
		loc, _ := time.LoadLocation("America/New_York")
		nyTime := time.Date(2024, 1, 15, 10, 30, 0, 0, loc)
		balance := Balance{
			Type:   BalanceTypeCurrent,
			Amount: amount,
			AsOf:   nyTime,
		}

		assert.Equal(t, nyTime, balance.AsOf)
		assert.Equal(t, loc, balance.AsOf.Location())
	})
}
