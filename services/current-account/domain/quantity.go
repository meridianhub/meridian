// Package domain re-exports the shared instrument-aware Money type for the current-account service.
//
// This file provides backward-compatible type aliases, constructors, and errors
// delegating to shared/pkg/money. The Currency() and AmountCents() methods are
// preserved for API compatibility during the migration.
//
// Deprecated: Direct cross-service imports of cadomain.Money should be replaced
// with shared/pkg/money.Money. This re-export layer maintains backward compatibility
// for callers within the current-account service boundary.
package domain

import (
	sharedmoney "github.com/meridianhub/meridian/shared/pkg/money"
	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// Re-export errors from shared package for API compatibility.
var (
	// ErrInvalidCurrency is returned when a currency code is not recognized.
	ErrInvalidCurrency = sharedmoney.ErrInvalidCurrency

	// ErrCurrencyMismatch is returned when arithmetic operations are attempted
	// on Money values with different instruments.
	ErrCurrencyMismatch = sharedmoney.ErrCurrencyMismatch

	// ErrAmountOverflow is returned when converting to minor units would overflow int64.
	ErrAmountOverflow = sharedmoney.ErrAmountOverflow
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

// Money is the instrument-aware monetary type for the current-account service.
// It is a type alias for shared/pkg/money.Money, allowing full backward compatibility.
type Money = sharedmoney.Money

// Constructor re-exports delegating to shared/pkg/money.

// NewMoney creates a new Money instance from a currency string and amount in minor units (cents).
// This preserves backward compatibility with the previous int64-based API.
//
// Example: NewMoney("GBP", 10000) creates £100.00
var NewMoney = sharedmoney.New

// NewMoneyFromInstrument creates Money from persisted instrument_code + dimension and minor-unit amount.
// Returns ErrInvalidCurrency if dimension is not "CURRENCY".
var NewMoneyFromInstrument = sharedmoney.NewFromInstrument

// NewMoneyFromMajorUnits creates Money from a currency code and major-unit int64 amount.
//
// Example: NewMoneyFromMajorUnits("GBP", 100) creates £100.00
var NewMoneyFromMajorUnits = sharedmoney.NewFromMajorUnits

// NewMoneyDecimal creates Money from a decimal amount and Currency type.
//
// Example: NewMoneyDecimal(decimal.NewFromInt(100), CurrencyGBP) creates £100.00
var NewMoneyDecimal = sharedmoney.NewFromDecimal

// MustNewMoneyDecimal creates Money from a decimal, panicking on invalid currency.
// Use only in tests or when currency is known valid.
var MustNewMoneyDecimal = sharedmoney.MustNewFromDecimal

// NewMoneyFromQuantity creates a Money wrapper from a quantity.Money value.
var NewMoneyFromQuantity = sharedmoney.NewFromQuantity

// ZeroMoney creates a zero Money value for the given currency.
var ZeroMoney = sharedmoney.Zero
