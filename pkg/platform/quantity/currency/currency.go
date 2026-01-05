// Package currency provides predefined Instrument instances for major fiat currencies.
//
// # ISO 4217 Standard
//
// Currency codes and precisions follow the ISO 4217 standard:
//   - Most currencies use 2 decimal places (cents, pence, etc.)
//   - JPY uses 0 decimal places (no minor units)
//
// # Usage
//
// Use predefined instruments for compile-time safety:
//
//	usdAmount := currency.USD(decimal.NewFromInt(100))    // $100.00
//	jpyAmount := currency.JPY(decimal.NewFromInt(10000))  // ¥10000
//
// Look up currencies by code:
//
//	instrument, ok := currency.ByCode("EUR")
//	if ok {
//	    euroAmount := quantity.NewMoney(decimal.NewFromInt(50), instrument)
//	}
package currency

import (
	"github.com/shopspring/decimal"

	"github.com/meridianhub/meridian/pkg/platform/quantity"
)

// Currency instruments for major fiat currencies.
// All instruments use version 0 (initial/unversioned) and CURRENCY dimension.
var (
	// InstrumentUSD is the US Dollar instrument (2 decimal places).
	InstrumentUSD = mustNewCurrency("USD", 2)

	// InstrumentEUR is the Euro instrument (2 decimal places).
	InstrumentEUR = mustNewCurrency("EUR", 2)

	// InstrumentGBP is the British Pound Sterling instrument (2 decimal places).
	InstrumentGBP = mustNewCurrency("GBP", 2)

	// InstrumentJPY is the Japanese Yen instrument (0 decimal places).
	InstrumentJPY = mustNewCurrency("JPY", 0)

	// InstrumentCHF is the Swiss Franc instrument (2 decimal places).
	InstrumentCHF = mustNewCurrency("CHF", 2)

	// InstrumentAUD is the Australian Dollar instrument (2 decimal places).
	InstrumentAUD = mustNewCurrency("AUD", 2)

	// InstrumentCAD is the Canadian Dollar instrument (2 decimal places).
	InstrumentCAD = mustNewCurrency("CAD", 2)

	// InstrumentNZD is the New Zealand Dollar instrument (2 decimal places).
	InstrumentNZD = mustNewCurrency("NZD", 2)
)

// currencies is the lookup map for currency instruments by code.
var currencies = map[string]quantity.Instrument{
	"USD": InstrumentUSD,
	"EUR": InstrumentEUR,
	"GBP": InstrumentGBP,
	"JPY": InstrumentJPY,
	"CHF": InstrumentCHF,
	"AUD": InstrumentAUD,
	"CAD": InstrumentCAD,
	"NZD": InstrumentNZD,
}

// mustNewCurrency creates a currency instrument, panicking on error.
// This is safe because currency codes are compile-time constants.
func mustNewCurrency(code string, precision int) quantity.Instrument {
	inst, err := quantity.NewInstrument(code, 0, quantity.DimensionCurrency, precision)
	if err != nil {
		panic("invalid currency instrument: " + err.Error())
	}
	return inst
}

// ByCode returns the currency Instrument for the given ISO 4217 code.
// Returns the instrument and true if found, or a zero Instrument and false if not found.
//
// Supported codes: USD, EUR, GBP, JPY, CHF, AUD, CAD, NZD.
func ByCode(code string) (quantity.Instrument, bool) {
	inst, ok := currencies[code]
	return inst, ok
}

// All returns a copy of all supported currency instruments.
func All() []quantity.Instrument {
	result := make([]quantity.Instrument, 0, len(currencies))
	for _, inst := range currencies {
		result = append(result, inst)
	}
	return result
}

// Codes returns all supported currency codes.
func Codes() []string {
	codes := make([]string, 0, len(currencies))
	for code := range currencies {
		codes = append(codes, code)
	}
	return codes
}

// USD creates a Money quantity in US Dollars.
func USD(amount decimal.Decimal) quantity.Money {
	return quantity.NewMoney(amount, InstrumentUSD)
}

// EUR creates a Money quantity in Euros.
func EUR(amount decimal.Decimal) quantity.Money {
	return quantity.NewMoney(amount, InstrumentEUR)
}

// GBP creates a Money quantity in British Pounds.
func GBP(amount decimal.Decimal) quantity.Money {
	return quantity.NewMoney(amount, InstrumentGBP)
}

// JPY creates a Money quantity in Japanese Yen.
func JPY(amount decimal.Decimal) quantity.Money {
	return quantity.NewMoney(amount, InstrumentJPY)
}

// CHF creates a Money quantity in Swiss Francs.
func CHF(amount decimal.Decimal) quantity.Money {
	return quantity.NewMoney(amount, InstrumentCHF)
}

// AUD creates a Money quantity in Australian Dollars.
func AUD(amount decimal.Decimal) quantity.Money {
	return quantity.NewMoney(amount, InstrumentAUD)
}

// CAD creates a Money quantity in Canadian Dollars.
func CAD(amount decimal.Decimal) quantity.Money {
	return quantity.NewMoney(amount, InstrumentCAD)
}

// NZD creates a Money quantity in New Zealand Dollars.
func NZD(amount decimal.Decimal) quantity.Money {
	return quantity.NewMoney(amount, InstrumentNZD)
}
