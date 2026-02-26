// Package domain re-exports the quantity types for payment-order service.
//
// This replaces the previous money.go which used shared/domain/money,
// migrating to the new Universal Asset System quantity package.
//
// # Design Constraint: Currency-Only
//
// Payment Order is intentionally restricted to CURRENCY dimension instruments.
// This is by design: payment orders represent fiat money movements (bank transfers,
// direct debits, credit card charges) which are always denominated in a currency.
//
// Non-currency assets (energy kWh, compute GPU_HOUR, carbon credits) are NOT
// supported here. For multi-asset position tracking, see the position-keeping service
// which uses the dimension-agnostic Amount type from shared/pkg/amount.
package domain

import (
	"errors"

	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/meridianhub/meridian/shared/platform/quantity/currency"
	"github.com/shopspring/decimal"
)

// Re-export errors for API compatibility
var (
	// ErrInvalidCurrency is returned when a currency code is not supported.
	ErrInvalidCurrency = errors.New("invalid currency")

	// ErrCurrencyMismatch is returned when operations are attempted on different currencies.
	ErrCurrencyMismatch = quantity.ErrInstrumentMismatch

	// ErrOverflow is kept for API compatibility but the new quantity package
	// uses arbitrary precision decimals so overflow is not a practical concern.
	ErrOverflow = errors.New("overflow")
)

// Money is an alias for the quantity.Money type (Qty[Monetary]).
type Money = quantity.Money

// Instrument is an alias for the quantity.Instrument type.
type Instrument = quantity.Instrument

// Re-export currency instruments for convenient access
var (
	InstrumentGBP = currency.InstrumentGBP
	InstrumentUSD = currency.InstrumentUSD
	InstrumentEUR = currency.InstrumentEUR
	InstrumentJPY = currency.InstrumentJPY
	InstrumentCHF = currency.InstrumentCHF
	InstrumentCAD = currency.InstrumentCAD
	InstrumentAUD = currency.InstrumentAUD
)

// NewMoney creates a new Money instance from a currency string and amount in minor units (cents).
// This provides backward compatibility with the old Money API where amounts were in minor units.
//
// Example:
//
//	money, err := NewMoney("GBP", 10000) // Creates £100.00
func NewMoney(currencyCode string, amountCents int64) (Money, error) {
	inst, ok := currency.ByCode(currencyCode)
	if !ok {
		return Money{}, ErrInvalidCurrency
	}
	// Convert cents to major units by shifting decimal point left by precision
	// e.g., 10000 cents with precision 2 becomes 100.00
	amount := decimal.NewFromInt(amountCents).Shift(-int32(inst.Precision))
	return quantity.NewMoney(amount, inst), nil
}

// NewMoneyDecimal creates Money from a decimal amount in major units and an Instrument.
// This is the preferred API for creating Money with the new quantity system.
//
// Example:
//
//	money := NewMoneyDecimal(decimal.NewFromInt(100), InstrumentGBP) // Creates £100.00
func NewMoneyDecimal(amount decimal.Decimal, inst Instrument) Money {
	return quantity.NewMoney(amount, inst)
}

// MustNewMoney creates Money from minor units and currency code, panicking on error.
// Use only in tests or when currency is known valid.
func MustNewMoney(currencyCode string, amountCents int64) Money {
	m, err := NewMoney(currencyCode, amountCents)
	if err != nil {
		panic("MustNewMoney: " + err.Error())
	}
	return m
}

// ToMinorUnits converts the Money amount to minor units (cents).
// This provides backward compatibility for code that needs cents.
// Panics if called on a zero-value Money (invalid instrument).
//
// Example:
//
//	money, _ := NewMoney("GBP", 10000) // £100.00
//	cents := ToMinorUnits(money)       // Returns 10000
func ToMinorUnits(m Money) int64 {
	if m.Instrument.Code == "" {
		panic("ToMinorUnits: called on zero-value Money with no instrument")
	}
	// Shift amount left by precision to convert major units to minor units
	// e.g., 100.00 with precision 2 becomes 10000
	shifted := m.Amount.Shift(int32(m.Instrument.Precision))
	return shifted.IntPart()
}

// CurrencyCode returns the currency code string from a Money value.
// This provides backward compatibility for code that accessed m.Currency().
// Panics if called on a zero-value Money (invalid instrument).
func CurrencyCode(m Money) string {
	if m.Instrument.Code == "" {
		panic("CurrencyCode: called on zero-value Money with no instrument")
	}
	return m.Instrument.Code
}
