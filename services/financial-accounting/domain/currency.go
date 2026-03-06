// Package domain re-exports the shared Currency type for financial-accounting service.
//
// This file maintains backward compatibility with the Currency type from shared/domain/money
// while providing helper functions to convert to Instrument for use with the new Qty[D] type.
package domain

import (
	"github.com/meridianhub/meridian/shared/domain/money" //nolint:staticcheck // Will migrate to refdata.InstrumentResolver
	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// Currency is an alias for the shared money.Currency type.
// This is maintained for backward compatibility with FinancialBookingLog.BaseCurrency.
type Currency = money.Currency //nolint:staticcheck // Will migrate to refdata.InstrumentResolver

// Supported currency codes following ISO 4217 standard.
//
//nolint:staticcheck // Will migrate to refdata.InstrumentResolver
const (
	CurrencyGBP = money.CurrencyGBP
	CurrencyUSD = money.CurrencyUSD
	CurrencyEUR = money.CurrencyEUR
	CurrencyJPY = money.CurrencyJPY
	CurrencyCHF = money.CurrencyCHF
	CurrencyCAD = money.CurrencyCAD
	CurrencyAUD = money.CurrencyAUD
)

// ParseCurrency converts a string to a Currency type with validation.
func ParseCurrency(s string) (Currency, error) {
	return money.ParseCurrency(s) //nolint:staticcheck // Will migrate to refdata.InstrumentResolver
}

// CurrencyToInstrument converts a Currency to an Instrument for use with Qty[Monetary].
// This enables creating Money quantities from Currency values.
//
// The instrument is created with:
//   - Code: the currency's ISO 4217 code (e.g., "GBP", "USD")
//   - Version: 1 (standard version for currencies)
//   - Dimension: "CURRENCY" (monetary dimension)
//   - Precision: derived from the currency's decimal places (e.g., 2 for GBP, 0 for JPY)
//
// Returns an error if the currency is invalid.
func CurrencyToInstrument(c Currency) (Instrument, error) {
	if !c.IsValid() { //nolint:staticcheck // Will migrate to refdata.InstrumentResolver
		return Instrument{}, ErrInvalidDimension
	}
	return quantity.NewInstrument(
		string(c),
		1,
		DimensionCurrency,
		int(c.DecimalPlaces()), //nolint:staticcheck // Will migrate to refdata.InstrumentResolver
	)
}

// MustCurrencyToInstrument converts a Currency to an Instrument, panicking on error.
// Use only in tests or when the currency is known to be valid.
func MustCurrencyToInstrument(c Currency) Instrument {
	inst, err := CurrencyToInstrument(c)
	if err != nil {
		panic(err)
	}
	return inst
}
