// Package domain re-exports the shared Currency type for financial-accounting service.
package domain

import (
	"github.com/meridianhub/meridian/shared/domain/money"
)

// Currency is an alias for the shared money.Currency type.
type Currency = money.Currency

// Supported currency codes following ISO 4217 standard.
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
	return money.ParseCurrency(s)
}
