// Package domain re-exports the shared dimension-agnostic Amount type for the current-account service.
//
// This file provides backward-compatible type aliases, constructors, and errors
// delegating to shared/pkg/amount. The Money type is preserved as an alias for Amount
// for API compatibility during the migration. Callers within the service boundary
// may use either Money or Amount; they refer to the same underlying type.
//
// Cross-service note: External services should import shared/pkg/amount directly
// rather than this package's Amount type. This re-export layer maintains backward
// compatibility for callers within the current-account service boundary.
package domain

import (
	"strings"

	sharedamount "github.com/meridianhub/meridian/shared/pkg/amount"
	sharedmoney "github.com/meridianhub/meridian/shared/pkg/money"
	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// Re-export errors from shared packages for API compatibility.
var (
	// ErrInvalidCurrency is returned when a currency code is not recognized.
	ErrInvalidCurrency = sharedmoney.ErrInvalidCurrency

	// ErrInstrumentMismatch is returned when arithmetic operations are attempted
	// on Amount values with different instruments.
	ErrInstrumentMismatch = sharedamount.ErrInstrumentMismatch

	// ErrCurrencyMismatch is an alias for ErrInstrumentMismatch for backward compatibility.
	//
	// Deprecated: Use ErrInstrumentMismatch for new code.
	ErrCurrencyMismatch = sharedamount.ErrInstrumentMismatch

	// ErrAmountOverflow is returned when converting to minor units would overflow int64.
	ErrAmountOverflow = sharedamount.ErrAmountOverflow
)

// Instrument is re-exported from the quantity package.
type Instrument = quantity.Instrument

// Currency represents an ISO 4217 currency code.
// This is a type alias re-export from shared/pkg/money for current-account service use.
type Currency = sharedmoney.Currency

// Currency constants for supported ISO 4217 currencies.
const (
	CurrencyGBP Currency = sharedmoney.CurrencyGBP
	CurrencyUSD Currency = sharedmoney.CurrencyUSD
	CurrencyEUR Currency = sharedmoney.CurrencyEUR
	CurrencyJPY Currency = sharedmoney.CurrencyJPY
	CurrencyCHF Currency = sharedmoney.CurrencyCHF
	CurrencyCAD Currency = sharedmoney.CurrencyCAD
	CurrencyAUD Currency = sharedmoney.CurrencyAUD
)

// Amount is the dimension-agnostic value type used for all account balances.
// It supports CURRENCY, ENERGY, CARBON, COMPUTE, and other valid dimensions.
// This is a type alias for shared/pkg/amount.Amount.
type Amount = sharedamount.Amount

// Money is an alias for Amount, preserved for backward compatibility.
//
// Deprecated: Use Amount for new code.
type Money = sharedamount.Amount

// Constructor re-exports for Amount.

// NewAmountFromInstrument creates an Amount from persisted instrument_code, dimension, precision,
// and a minor-unit amount. For CURRENCY dimension, the precision parameter is ignored and the
// canonical precision from the currency registry is used instead.
// Returns ErrInstrumentMismatch (wrapped) if the dimension is not recognized.
var NewAmountFromInstrument = sharedamount.NewFromInstrument

// NewMoney creates a new Amount (Money) instance from a currency string and amount in minor units (cents).
// This preserves backward compatibility with the previous int64-based API.
// Only CURRENCY instruments are supported via this constructor.
// Returns ErrInvalidCurrency if the currency code is not recognized.
//
// Example: NewMoney("GBP", 10000) creates £100.00
var NewMoney = func(currencyCode string, amountMinorUnits int64) (Amount, error) {
	a, err := sharedamount.NewFromInstrument(currencyCode, "CURRENCY", 2, amountMinorUnits)
	if err != nil {
		return Amount{}, ErrInvalidCurrency
	}
	return a, nil
}

// NewMoneyFromInstrument creates an Amount from persisted instrument_code + dimension and minor-unit amount.
// Returns ErrInvalidCurrency if dimension is not "CURRENCY".
//
// Deprecated: Use NewAmountFromInstrument for new code which supports all dimensions.
var NewMoneyFromInstrument = func(instrumentCode, dimension string, amountMinorUnits int64) (Amount, error) {
	if strings.ToUpper(dimension) != quantity.DimensionCurrency {
		return Amount{}, ErrInvalidCurrency
	}
	return sharedamount.NewFromInstrument(instrumentCode, quantity.DimensionCurrency, 2, amountMinorUnits)
}

// ZeroAmount creates a zero Amount for the given instrument.
var ZeroAmount = sharedamount.Zero

// ZeroMoney creates a zero Amount for the given currency code.
//
// Deprecated: Use NewAmountFromInstrument with 0 minor units for new code.
var ZeroMoney = func(currencyCode string) (Amount, error) {
	return sharedamount.NewFromInstrument(currencyCode, "CURRENCY", 2, 0)
}
